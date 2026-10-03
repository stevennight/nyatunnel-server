package api

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/store"
)

type channelView struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Events    []string `json:"events"`
	Enabled   bool     `json:"enabled"`
	Target    string   `json:"target"` // a safe summary: webhook host, Telegram chat id
	LastError string   `json:"lastError"`
	LastSent  *int64   `json:"lastSent"`
	CreatedAt int64    `json:"createdAt"`
}

func (s *server) viewChannel(c *store.Channel) channelView {
	v := channelView{ID: c.ID, Kind: c.Kind, Name: c.Name, Events: c.Events, Enabled: c.Enabled, LastError: c.LastError, LastSent: c.LastSent, CreatedAt: c.CreatedAt}
	if v.Events == nil {
		v.Events = []string{}
	}
	if cfg, err := s.Notify.Open(c.Config); err == nil {
		switch c.Kind {
		case "webhook":
			if u, err := url.Parse(cfg.URL); err == nil {
				v.Target = u.Scheme + "://" + u.Host
			}
		case "telegram":
			v.Target = "chat " + cfg.ChatID
		}
	}
	return v
}

func (s *server) handleListChannels(w http.ResponseWriter, r *http.Request, p *principal) {
	channels, err := s.Store.Channels(r.Context())
	if err != nil {
		s.fail(w, "list channels", err)
		return
	}
	out := make([]channelView, 0, len(channels))
	for _, c := range channels {
		out = append(out, s.viewChannel(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out, "events": notify.AllEvents})
}

type channelInput struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
	// Config replaces the stored secrets when given (omitted on edits that keep them).
	Config *notify.Config `json:"config"`
}

func (s *server) saveChannel(w http.ResponseWriter, r *http.Request, p *principal, c *store.Channel, in *channelInput) bool {
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 64 {
		writeError(w, http.StatusBadRequest, "invalid_name", "名称为 1–64 个字符")
		return false
	}
	valid := map[string]bool{}
	for _, e := range notify.AllEvents {
		valid[e] = true
	}
	events := []string{}
	for _, e := range in.Events {
		if !valid[e] {
			writeError(w, http.StatusBadRequest, "invalid_event", "未知事件 "+e)
			return false
		}
		events = append(events, e)
	}
	if in.Config != nil {
		if err := in.Config.Validate(c.Kind); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
			return false
		}
		sealed, err := s.Notify.Seal(*in.Config)
		if err != nil {
			s.fail(w, "seal channel", err)
			return false
		}
		c.Config = sealed
	}
	if len(c.Config) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_config", "请填写渠道配置")
		return false
	}
	c.Name, c.Events, c.Enabled = name, events, in.Enabled
	if err := s.Store.SaveChannel(r.Context(), c); err != nil {
		s.fail(w, "save channel", err)
		return false
	}
	return true
}

func (s *server) handleCreateChannel(w http.ResponseWriter, r *http.Request, p *principal) {
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	if in.Kind != "webhook" && in.Kind != "telegram" {
		writeError(w, http.StatusBadRequest, "invalid_kind", "渠道类型必须是 webhook 或 telegram")
		return
	}
	c := &store.Channel{ID: auth.NewID("ch_"), Kind: in.Kind, CreatedAt: s.now().UnixMilli()}
	if !s.saveChannel(w, r, p, c, &in) {
		return
	}
	s.audit(r, p, "channel.create", c.ID, c.Kind+" "+c.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"channel": s.viewChannel(c)})
}

func (s *server) channelByID(w http.ResponseWriter, r *http.Request) *store.Channel {
	channels, err := s.Store.Channels(r.Context())
	if err != nil {
		s.fail(w, "channel", err)
		return nil
	}
	for _, c := range channels {
		if c.ID == r.PathValue("id") {
			return c
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "")
	return nil
}

func (s *server) handleUpdateChannel(w http.ResponseWriter, r *http.Request, p *principal) {
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	c := s.channelByID(w, r)
	if c == nil {
		return
	}
	if !s.saveChannel(w, r, p, c, &in) {
		return
	}
	s.audit(r, p, "channel.update", c.ID, c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"channel": s.viewChannel(c)})
}

func (s *server) handleDeleteChannel(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if err := s.Store.DeleteChannel(r.Context(), id); err != nil {
		s.fail(w, "delete channel", err)
		return
	}
	s.audit(r, p, "channel.delete", id, "")
	writeOK(w)
}

// handleTestChannel sends a test message synchronously and reports the result.
func (s *server) handleTestChannel(w http.ResponseWriter, r *http.Request, p *principal) {
	c := s.channelByID(w, r)
	if c == nil {
		return
	}
	err := s.Notify.Deliver(r.Context(), c, notify.Event{Kind: notify.EventTest, Title: "测试消息", Text: "这是一条来自 NyaTunnel 的测试消息。", At: s.now()})
	if err != nil {
		writeError(w, http.StatusBadGateway, "delivery_failed", err.Error())
		return
	}
	writeOK(w)
}
