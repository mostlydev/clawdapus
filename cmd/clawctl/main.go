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
	Addr         string
	ManifestPath string
}

func loadConfig() config {
	return config{
		Addr:         envOr("CLAWCTL_ADDR", ":8082"),
		ManifestPath: envOr("CLAWCTL_MANIFEST", "/claw/pod-manifest.json"),
	}
}

func run(cfg config) error {
	manifest, err := readManifest(cfg.ManifestPath)
	if err != nil {
		return fmt.Errorf("clawctl: read manifest: %w", err)
	}

	source, err := newDockerStatusSource(manifest.PodName)
	if err != nil {
		return fmt.Errorf("clawctl: docker client: %w", err)
	}
	defer source.Close()

	h := newHandler(manifest, source)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "clawctl ui listening on %s\n", cfg.Addr)
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
		return fmt.Errorf("clawctl healthcheck: read manifest: %w", err)
	}
	if strings.TrimSpace(manifest.PodName) == "" {
		return fmt.Errorf("clawctl healthcheck: manifest podName is empty")
	}
	source, err := newDockerStatusSource(manifest.PodName)
	if err != nil {
		return fmt.Errorf("clawctl healthcheck: docker client: %w", err)
	}
	defer source.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := source.Ping(ctx); err != nil {
		return fmt.Errorf("clawctl healthcheck: docker ping failed: %w", err)
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
