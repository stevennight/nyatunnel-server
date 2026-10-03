// Command server is the NyaTunnel server: the admin console, accounts, device sessions and the
// tunnel edge (HTTP ingress and TCP/UDP ports).
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/api"
	"nyatunnel-server/internal/server/config"
	"nyatunnel-server/internal/server/edge"
	"nyatunnel-server/internal/server/hub"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/realip"
	"nyatunnel-server/internal/server/secrets"
	"nyatunnel-server/internal/server/store"
	"nyatunnel-server/internal/shared/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")
	healthcheck := fs.Bool("healthcheck", false, "probe the local /healthz endpoint and exit 0 when healthy (for container health checks)")
	debug := fs.Bool("debug", false, "log every request")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "nyatunnel-server", version.String())
		return 0
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	if *healthcheck {
		return probe(cfg.Listen, stderr)
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	if err := serve(cfg, log); err != nil {
		log.Error("server stopped", "err", err)
		return 1
	}
	return 0
}

func serve(cfg config.Config, log *slog.Logger) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	box, err := secrets.LoadOrCreate(cfg.SecretsKeyFile)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "nyatunnel.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	setupToken, err := setupTokenIfNeeded(ctx, st, log)
	if err != nil {
		return err
	}

	resolver := &realip.Resolver{Trusted: cfg.TrustedProxies}
	tcpHost := cfg.PublicHost
	if h, _, err := net.SplitHostPort(tcpHost); err == nil {
		tcpHost = h
	}
	var ed *edge.Edge
	var console *api.Handler
	h := hub.New(hub.Options{
		Store: st, Log: log, PublicHost: cfg.PublicHost, TCPHost: tcpHost,
		OnChange: func() {
			if err := ed.Reload(context.Background()); err != nil {
				log.Error("edge reload", "err", err)
			}
		},
		Audit: api.AuditFunc(st, log, time.Now),
		OnRequest: func(ctx context.Context, deviceID string, req tunnelproto.TunnelRequest) error {
			return console.DeviceRequest(ctx, deviceID, req)
		},
		MinClientVersion: func(ctx context.Context) string { return console.MinClientVersion(ctx) },
	})
	notifier := &notify.Notifier{Store: st, Secrets: box, Log: log, ServerName: func(ctx context.Context) string {
		v, _ := st.Setting(ctx, "server_name")
		if v == "" {
			v = "NyaTunnel"
		}
		return v
	}}
	gateKey, err := gateKey(ctx, st)
	if err != nil {
		return err
	}
	ed = edge.New(edge.Options{
		Store: st, Dialer: h, Log: log, RealIP: resolver, BindAddr: cfg.PortBindAddr, GateKey: gateKey,
		ConsoleURL: cfg.PublicURL, Audit: api.AuditFunc(st, log, time.Now), Notify: notifier.Send,
		SurgeMBPerHour: func(ctx context.Context) int {
			v, _ := st.Setting(ctx, "surge_mb_per_hour")
			n, _ := strconv.Atoi(v)
			return n
		},
	})
	if err := ed.Reload(ctx); err != nil {
		return err
	}
	go ed.Run(ctx)
	go housekeeping(ctx, st, log)

	console = api.New(api.Options{Config: cfg, Store: st, Hub: h, Edge: ed, Secrets: box, Log: log, SetupToken: setupToken,
		Notify: notifier, Background: ctx})
	consoleSrv := &http.Server{Addr: cfg.Listen, ReadHeaderTimeout: 10 * time.Second, Handler: console}
	ingress := &http.Server{Addr: cfg.IngressListen, Handler: ed, ReadHeaderTimeout: 30 * time.Second}
	internal := &http.Server{Addr: cfg.InternalListen, Handler: ed.AskHandler(), ReadHeaderTimeout: 5 * time.Second}

	errc := make(chan error, 3)
	for _, srv := range []*http.Server{consoleSrv, ingress, internal} {
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("listen %s: %w", srv.Addr, err)
			}
		}()
	}
	log.Info("nyatunnel-server started", "version", version.String(), "public", cfg.PublicURL,
		"console", cfg.Listen, "ingress", cfg.IngressListen, "internal", cfg.InternalListen, "data", cfg.DataDir)

	var runErr error
	select {
	case runErr = <-errc:
	case <-ctx.Done():
	}
	log.Info("shutting down")
	h.Close()
	ed.Close()
	ed.Flush(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range []*http.Server{consoleSrv, ingress, internal} {
		_ = srv.Shutdown(shutdownCtx)
	}
	return runErr
}

// setupTokenIfNeeded prints a one-off token while no account exists; the first-run setup page asks
// for it, so whoever reaches a fresh console first cannot take it over.
func setupTokenIfNeeded(ctx context.Context, st *store.Store, log *slog.Logger) (string, error) {
	n, err := st.UserCount(ctx)
	if err != nil || n > 0 {
		return "", err
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := strings.ToLower(base64.RawURLEncoding.EncodeToString(b))
	log.Warn("no account yet: open the console and create the administrator with this setup token", "setup_token", token)
	return token, nil
}

// gateKey returns the key that signs tunnel access cookies, creating it on first start.
func gateKey(ctx context.Context, st *store.Store) ([]byte, error) {
	v, err := st.Setting(ctx, "gate_key")
	if err != nil {
		return nil, err
	}
	if key, err := base64.StdEncoding.DecodeString(v); err == nil && len(key) == 32 {
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, st.SetSetting(ctx, "gate_key", base64.StdEncoding.EncodeToString(key))
}

func housekeeping(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now()
		if err := st.DeleteExpiredSessions(ctx, now.UnixMilli()); err != nil && ctx.Err() == nil {
			log.Warn("housekeeping: sessions", "err", err)
		}
		_ = st.DeleteStaleEnrollments(ctx, now.Add(-7*24*time.Hour).UnixMilli())
		_ = st.DeleteAuditBefore(ctx, now.Add(-180*24*time.Hour).UnixMilli())
		_ = st.DeleteTrafficBefore(ctx, now.Add(-400*24*time.Hour).UnixMilli())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func probe(listen string, stderr io.Writer) int {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck: bad listen address:", err)
		return 2
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: status", resp.Status)
		return 1
	}
	return 0
}
