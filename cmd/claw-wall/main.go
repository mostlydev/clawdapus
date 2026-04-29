package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Addr         string
	TokenPairs   string
	BufferLimit  int
	PollInterval time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("claw-wall", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	healthcheck := fs.Bool("healthcheck", false, "check HTTP server health and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *healthcheck {
		return runHealthcheck(cfg.Addr)
	}

	targets, err := parseTokenPairs(cfg.TokenPairs)
	if err != nil {
		return fmt.Errorf("claw-wall: parse CLAW_WALL_TOKENS: %w", err)
	}

	store := newConversationStore(cfg.BufferLimit)
	poller := newDiscordPoller(&http.Client{Timeout: 10 * time.Second}, store, targets, cfg.BufferLimit)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poller.Run(ctx, cfg.PollInterval, os.Stderr)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newHandler(store),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "claw-wall listening on %s\n", cfg.Addr)
		errCh <- server.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		_ = sig
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	return server.Shutdown(shutdownCtx)
}

func loadConfig() (config, error) {
	bufferLimit, err := envInt("CLAW_WALL_LIMIT", 500)
	if err != nil {
		return config{}, err
	}
	if bufferLimit < 1 {
		return config{}, fmt.Errorf("claw-wall: CLAW_WALL_LIMIT must be at least 1")
	}

	pollSeconds, err := envInt("CLAW_WALL_POLL_INTERVAL", 30)
	if err != nil {
		return config{}, err
	}
	if pollSeconds < 1 {
		return config{}, fmt.Errorf("claw-wall: CLAW_WALL_POLL_INTERVAL must be at least 1")
	}

	tokenPairs := strings.TrimSpace(os.Getenv("CLAW_WALL_TOKENS"))
	if tokenPairs == "" {
		return config{}, fmt.Errorf("claw-wall: CLAW_WALL_TOKENS is required")
	}

	return config{
		Addr:         envOr("CLAW_WALL_ADDR", ":8080"),
		TokenPairs:   tokenPairs,
		BufferLimit:  bufferLimit,
		PollInterval: time.Duration(pollSeconds) * time.Second,
	}, nil
}

func envOr(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("claw-wall: %s must be an integer: %w", key, err)
	}
	return parsed, nil
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
	if strings.HasPrefix(addr, ":") {
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
