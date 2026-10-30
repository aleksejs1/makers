// Command makers runs the makers.lv web application.
//
// Usage:
//
//	makers [serve] [flags]     start the web server (default)
//	makers healthcheck         probe the local server (for Docker HEALTHCHECK)
//	makers backup <file>       write a consistent copy of the database
//	makers version             print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aleksejs1/makers/internal/config"
	"github.com/aleksejs1/makers/internal/db"
	"github.com/aleksejs1/makers/internal/mail"
	"github.com/aleksejs1/makers/internal/server"
	"github.com/aleksejs1/makers/internal/store"
	"github.com/aleksejs1/makers/ui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "healthcheck":
		err = healthcheck(args)
	case "backup":
		err = backup(args)
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (want serve, healthcheck, backup or version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "makers:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	if env := os.Getenv("MAKERS_PORT"); env != "" && env != strconv.Itoa(cfg.Port) {
		log.Warn("--port differs from MAKERS_PORT; the Docker health check uses MAKERS_PORT, so set the port there",
			"port", cfg.Port, "MAKERS_PORT", env)
	}
	if cfg.SecretGenerated {
		log.Warn("MAKERS_SECRET is not set; using a random secret (pending login codes stop working after a restart)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	var sender mail.Sender = mail.LogSender{Log: log}
	if cfg.SMTPHost != "" {
		sender = mail.SMTPSender{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom, TLS: cfg.SMTPTLS,
		}
	} else {
		log.Warn("SMTP_HOST is not set; login codes are written to the log instead of being emailed")
	}

	srv, err := server.New(server.Options{
		Config: cfg, Store: store.New(sqlDB), Mail: sender, Logger: log, UI: ui.FS, Version: version,
	})
	if err != nil {
		return err
	}
	go srv.RunMaintenance(ctx)

	httpSrv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", httpSrv.Addr, "base_url", cfg.BaseURL, "version", version, "env", cfg.Env)
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	if cfg.IsDev() {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

// healthcheck probes /healthz on the local server. It resolves the port
// exactly like serve does (MAKERS_PORT, overridden by --port).
func healthcheck(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // fixed local address
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // fixed local address
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}

func backup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: makers backup <destination file>")
	}
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := db.Backup(ctx, sqlDB, args[0]); err != nil {
		return err
	}
	fmt.Println("backup written to", args[0])
	return nil
}
