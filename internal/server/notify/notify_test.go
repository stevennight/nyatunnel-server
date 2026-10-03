package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nyatunnel-server/internal/server/secrets"
	"nyatunnel-server/internal/server/store"
)

func TestDeliver(t *testing.T) {
	st, _ := store.Open(context.Background(), ":memory:")
	defer st.Close()
	box, _ := secrets.New(bytes.Repeat([]byte{3}, 32))
	n := &Notifier{Store: st, Secrets: box, Log: slog.New(slog.DiscardHandler), ServerName: func(context.Context) string { return "Nya" }}

	type got struct {
		path, sig string
		body      map[string]any
	}
	hits := make(chan got, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(b)
		sig := ""
		if r.Header.Get("X-NyaTunnel-Signature") == "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			sig = "valid"
		}
		hits <- got{r.URL.Path, sig, m}
		if strings.Contains(r.URL.Path, "broken") {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	cfg, _ := n.Seal(Config{URL: srv.URL + "/hook", Secret: "s3cret"})
	hook := &store.Channel{ID: "ch_1", Kind: "webhook", Name: "hook", Config: cfg, Enabled: true}
	st.SaveChannel(context.Background(), hook)
	if err := n.Deliver(context.Background(), hook, Event{Kind: EventQuota, Title: "t", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	h := <-hits
	if h.path != "/hook" || h.sig != "valid" || h.body["event"] != EventQuota || h.body["server"] != "Nya" {
		t.Fatalf("webhook %+v", h)
	}

	tcfg, _ := n.Seal(Config{BotToken: "123:abc", ChatID: "42", APIBase: srv.URL})
	tg := &store.Channel{ID: "ch_2", Kind: "telegram", Name: "tg", Config: tcfg, Enabled: true}
	if err := n.post(context.Background(), tg.Kind, mustOpen(t, n, tcfg), Event{Title: "hello", Text: "world"}); err != nil {
		t.Fatal(err)
	}
	h = <-hits
	if h.path != "/bot123:abc/sendMessage" || h.body["chat_id"] != "42" || !strings.Contains(h.body["text"].(string), "hello") {
		t.Fatalf("telegram %+v", h)
	}

	if !wants(&store.Channel{Events: []string{EventSurge}}, EventSurge) || wants(&store.Channel{Events: []string{EventSurge}}, EventQuota) {
		t.Fatal("event filter")
	}
	if (&Config{URL: "ftp://x"}).Validate("webhook") == nil || (&Config{BotToken: "x"}).Validate("telegram") == nil {
		t.Fatal("validation")
	}
}

func mustOpen(t *testing.T, n *Notifier, sealed []byte) Config {
	c, err := n.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
