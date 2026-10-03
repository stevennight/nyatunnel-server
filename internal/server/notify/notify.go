// Package notify delivers operational events (tunnel requests, quota alerts, traffic surges,
// brute-force attempts…) to webhook and Telegram channels.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nyatunnel-server/internal/server/secrets"
	"nyatunnel-server/internal/server/store"
)

// Event kinds a channel can subscribe to.
const (
	EventRequestCreated = "request.created"
	EventDeviceEnrolled = "device.enrolled"
	EventQuota          = "quota.exceeded"
	EventSurge          = "traffic.surge"
	EventBruteForce     = "auth.bruteforce"
	EventDomain         = "domain.changed"
	EventAbuseReport    = "abuse.report"
	EventTest           = "test"
)

// AllEvents lists the kinds for the console.
var AllEvents = []string{EventRequestCreated, EventDeviceEnrolled, EventQuota, EventSurge, EventBruteForce, EventDomain, EventAbuseReport}

// Event is one notification.
type Event struct {
	Kind  string    `json:"event"`
	Title string    `json:"title"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
}

// Config is the sealed part of a channel.
type Config struct {
	// Webhook
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"` // signs the body: X-NyaTunnel-Signature: sha256=<hex hmac>
	// Telegram
	BotToken string `json:"botToken,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
	APIBase  string `json:"apiBase,omitempty"` // default https://api.telegram.org (for a reverse proxy where Telegram is blocked)
}

// Validate checks a channel configuration.
func (c *Config) Validate(kind string) error {
	switch kind {
	case "webhook":
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("Webhook 地址必须是 http(s) URL")
		}
	case "telegram":
		if c.BotToken == "" || c.ChatID == "" {
			return errors.New("请填写 Bot Token 和 Chat ID")
		}
		if c.APIBase != "" {
			if u, err := url.Parse(c.APIBase); err != nil || u.Scheme != "https" {
				return errors.New("Telegram API 地址必须是 https URL")
			}
		}
	default:
		return errors.New("渠道类型必须是 webhook 或 telegram")
	}
	return nil
}

// Notifier fans events out to channels.
type Notifier struct {
	Store      *store.Store
	Secrets    *secrets.Box
	Log        *slog.Logger
	ServerName func(context.Context) string
	Client     *http.Client
	Now        func() time.Time
}

// Seal encrypts a channel configuration.
func (n *Notifier) Seal(c Config) ([]byte, error) {
	b, _ := json.Marshal(c)
	return n.Secrets.Seal(b)
}

// Open decrypts a channel configuration.
func (n *Notifier) Open(sealed []byte) (Config, error) {
	var c Config
	b, err := n.Secrets.Open(sealed)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func wants(ch *store.Channel, kind string) bool {
	if kind == EventTest || len(ch.Events) == 0 {
		return true
	}
	for _, e := range ch.Events {
		if e == kind {
			return true
		}
	}
	return false
}

// Send delivers an event to every enabled, subscribed channel in the background.
func (n *Notifier) Send(kind, title, text string) {
	if n == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		channels, err := n.Store.Channels(ctx)
		if err != nil {
			n.Log.Warn("notify: list channels", "err", err)
			return
		}
		for _, ch := range channels {
			if ch.Enabled && wants(ch, kind) {
				_ = n.Deliver(ctx, ch, Event{Kind: kind, Title: title, Text: text, At: n.now()})
			}
		}
	}()
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// Deliver sends one event to one channel with retries and records the outcome.
func (n *Notifier) Deliver(ctx context.Context, ch *store.Channel, ev Event) error {
	cfg, err := n.Open(ch.Config)
	if err == nil {
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
				}
			}
			if err = n.post(ctx, ch.Kind, cfg, ev); err == nil || ctx.Err() != nil {
				break
			}
		}
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		n.Log.Warn("notify: delivery failed", "channel", ch.Name, "err", err)
	}
	_ = n.Store.ChannelResult(context.WithoutCancel(ctx), ch.ID, msg, n.now().UnixMilli())
	return err
}

func (n *Notifier) client() *http.Client {
	if n.Client != nil {
		return n.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (n *Notifier) post(ctx context.Context, kind string, cfg Config, ev Event) error {
	server := "NyaTunnel"
	if n.ServerName != nil {
		server = n.ServerName(ctx)
	}
	var req *http.Request
	switch kind {
	case "webhook":
		body, _ := json.Marshal(map[string]any{"event": ev.Kind, "title": ev.Title, "text": ev.Text, "at": ev.At.UTC().Format(time.RFC3339), "server": server})
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("User-Agent", "NyaTunnel-Webhook")
		if cfg.Secret != "" {
			m := hmac.New(sha256.New, []byte(cfg.Secret))
			m.Write(body)
			r.Header.Set("X-NyaTunnel-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
		}
		req = r
	case "telegram":
		base := strings.TrimRight(cfg.APIBase, "/")
		if base == "" {
			base = "https://api.telegram.org"
		}
		text := fmt.Sprintf("[%s] %s\n%s", server, ev.Title, ev.Text)
		body, _ := json.Marshal(map[string]any{"chat_id": cfg.ChatID, "text": text, "disable_web_page_preview": true})
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+cfg.BotToken+"/sendMessage", bytes.NewReader(body))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
		req = r
	default:
		return fmt.Errorf("unknown channel kind %q", kind)
	}
	resp, err := n.client().Do(req)
	if err != nil {
		// Never echo the URL: a Telegram URL contains the bot token.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("request failed: %v", ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
