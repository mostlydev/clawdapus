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
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/docker/docker/client"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

type config struct {
	Addr           string
	ManifestPath   string
	SchedulePath   string
	PrincipalsPath string
	GovernanceDir  string
	ContextRoot    string
	CllamaAPIURL   string
	CllamaAPIToken string
}

type quietExitError struct{}

func (quietExitError) Error() string { return "" }

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if _, ok := err.(quietExitError); ok {
			os.Exit(1)
		}
		log.Fatalf("claw-api: %v", err)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("claw-api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	healthcheck := fs.Bool("healthcheck", false, "check HTTP server health and exit")
	requestMethod := fs.String("request-method", "", "issue a local authenticated request instead of serving HTTP")
	requestPath := fs.String("request-path", "", "path to request in local client mode")
	requestBody := fs.String("request-body", "", "raw JSON request body for local client mode")
	requestPrincipal := fs.String("request-principal", "claw-scheduler", "principal name to use for local client mode")
	requestTimeout := fs.Duration("request-timeout", 10*time.Second, "timeout for local client mode requests")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := configFromEnv()
	if *healthcheck {
		return runHealthcheck(cfg.Addr)
	}
	if strings.TrimSpace(*requestMethod) != "" || strings.TrimSpace(*requestPath) != "" || strings.TrimSpace(*requestBody) != "" {
		if strings.TrimSpace(*requestMethod) == "" || strings.TrimSpace(*requestPath) == "" {
			return fmt.Errorf("request-method and request-path are both required for local client mode")
		}
		return runLocalRequest(cfg, stdout, *requestMethod, *requestPath, *requestBody, *requestPrincipal, *requestTimeout)
	}

	manifest, err := loadManifest(cfg.ManifestPath)
	if err != nil {
		return err
	}
	scheduleManifest, err := loadScheduleManifest(cfg.SchedulePath)
	if err != nil {
		return err
	}
	store, warnings, err := clawapi.LoadStoreWithWarnings(cfg.PrincipalsPath)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		fmt.Fprintf(stderr, "claw-api warning: %s\n", warning)
	}
	var scheduleState *scheduleStateStore
	if scheduleManifest != nil {
		scheduleState, err = newScheduleStateStore(cfg.GovernanceDir, scheduleManifest)
		if err != nil {
			return err
		}
	}
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer docker.Close()

	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()

	scheduler, err := newScheduler(scheduleManifest, docker, scheduleState, stderr)
	if err != nil {
		return err
	}
	if scheduler != nil {
		go scheduler.Run(runtimeCtx)
	}

	handler := newHandler(
		manifest,
		scheduleManifest,
		scheduleState,
		scheduler,
		store,
		docker,
		stdout,
		clawapi.ThresholdsFromEnv(),
		cfg.GovernanceDir,
		withAgentContextConfig(cfg.ContextRoot, cfg.CllamaAPIURL, cfg.CllamaAPIToken),
	)
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
	var serveErr error
	select {
	case sig := <-sigCh:
		fmt.Fprintf(stderr, "received signal %s, shutting down\n", sig)
	case err := <-errCh:
		serveErr = err
	}

	stopRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverErr := server.Shutdown(ctx)
	schedulerErr := scheduler.Wait(ctx)
	return errors.Join(serveErr, serverErr, schedulerErr)
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

func loadScheduleManifest(path string) (*schedulepkg.Manifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schedule manifest: %w", err)
	}
	var manifest schedulepkg.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse schedule manifest: %w", err)
	}
	return &manifest, nil
}

func configFromEnv() config {
	return config{
		Addr:           envOr("CLAW_API_ADDR", ":8080"),
		ManifestPath:   envOr("CLAW_API_MANIFEST", "/claw/pod-manifest.json"),
		SchedulePath:   envOr("CLAW_API_SCHEDULE_MANIFEST", ""),
		PrincipalsPath: envOr("CLAW_API_PRINCIPALS", "/claw/principals.json"),
		GovernanceDir:  envOr("CLAW_GOVERNANCE_DIR", "/claw-governance"),
		ContextRoot:    envOr("CLAW_CONTEXT_ROOT", "/claw/context"),
		CllamaAPIURL:   envOr("CLAW_CLLAMA_API_URL", ""),
		CllamaAPIToken: envOr("CLAW_CLLAMA_API_TOKEN", ""),
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
