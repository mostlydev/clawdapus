package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/client"

	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	"github.com/mostlydev/clawdapus/internal/clawapi"
)

type config struct {
	Addr           string
	ManifestPath   string
	PrincipalsPath string
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		log.Fatalf("claw-api: %v", err)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("claw-api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	healthcheck := fs.Bool("healthcheck", false, "check HTTP server health and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := configFromEnv()
	if *healthcheck {
		return runHealthcheck(cfg.Addr)
	}

	manifest, err := loadManifest(cfg.ManifestPath)
	if err != nil {
		return err
	}
	store, err := clawapi.LoadStore(cfg.PrincipalsPath)
	if err != nil {
		return err
	}
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer docker.Close()

	handler := newHandler(manifest, store, docker, stdout, clawapi.ThresholdsFromEnv())
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(stderr, "claw-api listening on %s\n", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		fmt.Fprintf(stderr, "received signal %s, shutting down\n", sig)
	case err := <-errCh:
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func loadManifest(path string) (*manifestpkg.PodManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pod manifest: %w", err)
	}
	var manifest manifestpkg.PodManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse pod manifest: %w", err)
	}
	return &manifest, nil
}

func configFromEnv() config {
	return config{
		Addr:           envOr("CLAW_API_ADDR", ":8080"),
		ManifestPath:   envOr("CLAW_API_MANIFEST", "/claw/pod-manifest.json"),
		PrincipalsPath: envOr("CLAW_API_PRINCIPALS", "/claw/principals.json"),
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func runHealthcheck(addr string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthcheckURL(addr))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	return nil
}

func healthcheckURL(addr string) string {
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		return "http://127.0.0.1" + addr + "/health"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/health"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port + "/health"
}
