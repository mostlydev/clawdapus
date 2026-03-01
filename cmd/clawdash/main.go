package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	cfg := loadConfig()

	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) == "-healthcheck" {
		if err := runHealthcheck(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

type config struct {
	Addr            string
	ManifestPath    string
	CllamaCostsURL  string
	CostLogFallback bool
}

func loadConfig() config {
	return config{
		Addr:           envOr("CLAWDASH_ADDR", ":8082"),
		ManifestPath:   envOr("CLAWDASH_MANIFEST", "/claw/pod-manifest.json"),
		CllamaCostsURL: strings.TrimSpace(os.Getenv("CLAWDASH_CLLAMA_COSTS_URL")),
		CostLogFallback: envBool(
			"CLAWDASH_COST_LOG_FALLBACK",
		),
	}
}

func run(cfg config) error {
	manifest, err := readManifest(cfg.ManifestPath)
	if err != nil {
		return fmt.Errorf("clawdash: read manifest: %w", err)
	}

	source, err := newDockerStatusSource(manifest.PodName)
	if err != nil {
		return fmt.Errorf("clawdash: docker client: %w", err)
	}
	defer source.Close()

	h := newHandler(manifest, source, cfg.CllamaCostsURL, cfg.CostLogFallback)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "clawdash ui listening on %s\n", cfg.Addr)
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sig
		return srv.Shutdown(ctx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func runHealthcheck(cfg config) error {
	manifest, err := readManifest(cfg.ManifestPath)
	if err != nil {
		return fmt.Errorf("clawdash healthcheck: read manifest: %w", err)
	}
	if strings.TrimSpace(manifest.PodName) == "" {
		return fmt.Errorf("clawdash healthcheck: manifest podName is empty")
	}
	source, err := newDockerStatusSource(manifest.PodName)
	if err != nil {
		return fmt.Errorf("clawdash healthcheck: docker client: %w", err)
	}
	defer source.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := source.Ping(ctx); err != nil {
		return fmt.Errorf("clawdash healthcheck: docker ping failed: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
