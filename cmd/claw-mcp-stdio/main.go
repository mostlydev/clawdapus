package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultAddr             = ":8080"
	defaultMCPPath          = "/mcp"
	defaultRequestTimeoutMS = 60000
	defaultRestartBackoffMS = 1000
	defaultRestartMaxMS     = 15000
	defaultMaxBodyBytes     = 1 << 20
	lateResponseTTL         = 5 * time.Minute
)

type config struct {
	Command        string
	Args           []string
	Addr           string
	MCPPath        string
	AuthToken      string
	RequestTimeout time.Duration
	RestartBackoff time.Duration
	RestartMax     time.Duration
	MaxBodyBytes   int64
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
}

type childResponse struct {
	ID json.RawMessage `json:"id,omitempty"`
}

type pendingCall struct {
	originalID json.RawMessage
	ch         chan []byte
}

type lateResponse struct {
	reason string
	at     time.Time
}

type initCache struct {
	response []byte
}

type stdioBridge struct {
	cfg config

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	stdin       io.WriteCloser
	available   bool
	generation  int64
	pending     map[string]pendingCall
	late        map[string]lateResponse
	sessions    map[string]int64
	initialized bool
	init        *initCache

	initMu sync.Mutex
	nextID atomic.Int64
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("claw-mcp-stdio", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	healthcheck := fs.Bool("healthcheck", false, "check HTTP health endpoint and exit")
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := newStdioBridge(ctx, cfg)
	go bridge.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", bridge.handleHealth)
	mux.HandleFunc(cfg.MCPPath, bridge.handleMCP)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "claw-mcp-stdio listening on %s path %s\n", cfg.Addr, cfg.MCPPath)
		errCh <- server.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
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
	command := strings.TrimSpace(os.Getenv("CLAW_MCP_STDIO_COMMAND"))
	if command == "" {
		return config{}, fmt.Errorf("claw-mcp-stdio: CLAW_MCP_STDIO_COMMAND is required")
	}

	args, err := envJSONArray("CLAW_MCP_STDIO_ARGS")
	if err != nil {
		return config{}, err
	}
	addr := strings.TrimSpace(os.Getenv("CLAW_MCP_STDIO_ADDR"))
	if addr == "" {
		port := strings.TrimSpace(os.Getenv("CLAW_MCP_STDIO_PORT"))
		if port == "" {
			addr = defaultAddr
		} else {
			addr = ":" + strings.TrimPrefix(port, ":")
		}
	}

	mcpPath := strings.TrimSpace(os.Getenv("CLAW_MCP_STDIO_PATH"))
	if mcpPath == "" {
		mcpPath = defaultMCPPath
	}
	if !strings.HasPrefix(mcpPath, "/") {
		return config{}, fmt.Errorf("claw-mcp-stdio: CLAW_MCP_STDIO_PATH must start with '/'")
	}

	requestTimeout, err := envDurationMS("CLAW_MCP_STDIO_REQUEST_TIMEOUT_MS", defaultRequestTimeoutMS)
	if err != nil {
		return config{}, err
	}
	restartBackoff, err := envDurationMS("CLAW_MCP_STDIO_RESTART_BACKOFF_MS", defaultRestartBackoffMS)
	if err != nil {
		return config{}, err
	}
	restartMax, err := envDurationMS("CLAW_MCP_STDIO_RESTART_MAX_MS", defaultRestartMaxMS)
	if err != nil {
		return config{}, err
	}
	maxBodyBytes, err := envInt64("CLAW_MCP_STDIO_MAX_BODY_BYTES", defaultMaxBodyBytes)
	if err != nil {
		return config{}, err
	}
	if maxBodyBytes < 1 {
		return config{}, fmt.Errorf("claw-mcp-stdio: CLAW_MCP_STDIO_MAX_BODY_BYTES must be at least 1")
	}

	return config{
		Command:        command,
		Args:           args,
		Addr:           addr,
		MCPPath:        mcpPath,
		AuthToken:      strings.TrimSpace(os.Getenv("CLAW_MCP_STDIO_AUTH_TOKEN")),
		RequestTimeout: requestTimeout,
		RestartBackoff: restartBackoff,
		RestartMax:     restartMax,
		MaxBodyBytes:   maxBodyBytes,
	}, nil
}

func envJSONArray(key string) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("claw-mcp-stdio: %s must be a JSON string array: %w", key, err)
	}
	return out, nil
}

func envDurationMS(key string, fallback int) (time.Duration, error) {
	value, err := envInt64(key, int64(fallback))
	if err != nil {
		return 0, err
	}
	if value < 1 {
		return 0, fmt.Errorf("claw-mcp-stdio: %s must be at least 1", key)
	}
	return time.Duration(value) * time.Millisecond, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("claw-mcp-stdio: %s must be an integer: %w", key, err)
	}
	return value, nil
}

func newStdioBridge(ctx context.Context, cfg config) *stdioBridge {
	childCtx, cancel := context.WithCancel(ctx)
	return &stdioBridge{
		cfg:      cfg,
		ctx:      childCtx,
		cancel:   cancel,
		pending:  make(map[string]pendingCall),
		late:     make(map[string]lateResponse),
		sessions: make(map[string]int64),
	}
}

func (b *stdioBridge) Run() {
	backoff := b.cfg.RestartBackoff
	for {
		if err := b.ctx.Err(); err != nil {
			return
		}
		if backoff <= 0 {
			backoff = time.Second
		}
		if b.cfg.RestartMax > 0 && backoff > b.cfg.RestartMax {
			backoff = b.cfg.RestartMax
		}

		err := b.runChild()
		if err != nil && b.ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "claw-mcp-stdio child stopped: %v\n", err)
		}
		b.markUnavailable("stdio child restarting")

		timer := time.NewTimer(backoff)
		select {
		case <-b.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if b.cfg.RestartMax > 0 && backoff < b.cfg.RestartMax {
			backoff *= 2
			if backoff > b.cfg.RestartMax {
				backoff = b.cfg.RestartMax
			}
		}
	}
}

func (b *stdioBridge) runChild() error {
	cmd := exec.CommandContext(b.ctx, b.cfg.Command, b.cfg.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	generation := b.markAvailable(stdin)
	fmt.Fprintf(os.Stderr, "claw-mcp-stdio spawned %q generation=%d\n", b.cfg.Command, generation)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b.readStdout(generation, stdout)
	}()
	go func() {
		defer wg.Done()
		copyPrefixed(os.Stderr, "mcp-stdio stderr: ", stderr)
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	return waitErr
}

func (b *stdioBridge) markAvailable(stdin io.WriteCloser) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.generation++
	b.stdin = stdin
	b.available = true
	b.pending = make(map[string]pendingCall)
	b.late = make(map[string]lateResponse)
	b.sessions = make(map[string]int64)
	b.initialized = false
	b.init = nil
	return b.generation
}

func (b *stdioBridge) markUnavailable(message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pending := range b.pending {
		pending.ch <- rpcErrorResponse(pending.originalID, -32000, message)
	}
	b.pending = make(map[string]pendingCall)
	b.late = make(map[string]lateResponse)
	b.sessions = make(map[string]int64)
	b.initialized = false
	b.init = nil
	b.available = false
	b.stdin = nil
}

func (b *stdioBridge) readStdout(generation int64, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var resp childResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "claw-mcp-stdio ignored non-JSON child output: %s\n", string(line))
			continue
		}
		key := idKey(resp.ID)
		if key == "" {
			fmt.Fprintf(os.Stderr, "claw-mcp-stdio child notification: %s\n", string(line))
			continue
		}
		b.mu.Lock()
		pending, ok := b.pending[key]
		if ok {
			delete(b.pending, key)
		}
		late, wasLate := b.late[key]
		if wasLate {
			delete(b.late, key)
		}
		b.mu.Unlock()
		if !ok {
			if wasLate {
				fmt.Fprintf(os.Stderr, "claw-mcp-stdio received late response for canceled id %s (%s)\n", key, late.reason)
				continue
			}
			fmt.Fprintf(os.Stderr, "claw-mcp-stdio ignored response for unknown id %s\n", key)
			continue
		}
		pending.ch <- restoreResponseID(line, pending.originalID)
	}
	if err := scanner.Err(); err != nil && b.ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "claw-mcp-stdio stdout reader error: %v\n", err)
	}
	b.failGeneration(generation, "stdio child exited")
}

func (b *stdioBridge) failGeneration(generation int64, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if generation != b.generation {
		return
	}
	for _, pending := range b.pending {
		pending.ch <- rpcErrorResponse(pending.originalID, -32000, message)
	}
	b.pending = make(map[string]pendingCall)
	b.late = make(map[string]lateResponse)
	b.available = false
	b.stdin = nil
	b.sessions = make(map[string]int64)
	b.initialized = false
	b.init = nil
}

func (b *stdioBridge) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if b.isAvailable() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	http.Error(w, "stdio child unavailable", http.StatusServiceUnavailable)
}

func (b *stdioBridge) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !b.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := readLimited(r.Body, b.cfg.MaxBodyBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, http.StatusBadRequest, -32700, "parse error")
		return
	}
	method := strings.TrimSpace(req.Method)
	if method == "" {
		writeRPCError(w, req.ID, http.StatusBadRequest, -32600, "missing method")
		return
	}

	if method == "initialize" {
		b.handleInitialize(w, body, req.ID)
		return
	}

	sessionID := strings.TrimSpace(r.Header.Get("MCP-Session-Id"))
	if !b.validSession(sessionID) {
		writeRPCError(w, req.ID, http.StatusNotFound, -32001, "unknown MCP session")
		return
	}
	w.Header().Set("MCP-Session-Id", sessionID)

	if idKey(req.ID) == "" {
		if err := b.forwardNotification(body, method); err != nil {
			writeRPCError(w, nil, http.StatusServiceUnavailable, -32000, err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp, err := b.forwardRequest(r.Context(), body, req.ID)
	if err != nil {
		writeRPCError(w, req.ID, http.StatusServiceUnavailable, -32000, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (b *stdioBridge) handleInitialize(w http.ResponseWriter, body []byte, originalID json.RawMessage) {
	b.initMu.Lock()
	defer b.initMu.Unlock()

	if b.init != nil {
		sessionID, err := b.newSession()
		if err != nil {
			writeRPCError(w, originalID, http.StatusInternalServerError, -32000, err.Error())
			return
		}
		w.Header().Set("MCP-Session-Id", sessionID)
		writeJSON(w, http.StatusOK, restoreResponseID(b.init.response, originalID))
		return
	}

	resp, err := b.forwardRequest(context.Background(), body, originalID)
	if err != nil {
		writeRPCError(w, originalID, http.StatusServiceUnavailable, -32000, err.Error())
		return
	}
	if responseHasError(resp) {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	b.init = &initCache{response: append([]byte(nil), resp...)}

	sessionID, err := b.newSession()
	if err != nil {
		writeRPCError(w, originalID, http.StatusInternalServerError, -32000, err.Error())
		return
	}
	w.Header().Set("MCP-Session-Id", sessionID)
	writeJSON(w, http.StatusOK, resp)
}

func (b *stdioBridge) authorized(r *http.Request) bool {
	if b.cfg.AuthToken == "" {
		return true
	}
	return strings.TrimSpace(r.Header.Get("Authorization")) == "Bearer "+b.cfg.AuthToken
}

func (b *stdioBridge) isAvailable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.available
}

func (b *stdioBridge) validSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.sessions[sessionID]
	return ok
}

func (b *stdioBridge) newSession() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions[token] = b.generation
	return token, nil
}

func (b *stdioBridge) forwardNotification(body []byte, method string) error {
	if method == "notifications/initialized" {
		b.mu.Lock()
		if b.initialized {
			b.mu.Unlock()
			return nil
		}
		b.initialized = true
		b.mu.Unlock()
	}
	_, err := b.writeToChild(body, nil)
	return err
}

func (b *stdioBridge) forwardRequest(ctx context.Context, body []byte, originalID json.RawMessage) ([]byte, error) {
	childID := b.nextID.Add(1)
	childIDRaw := json.RawMessage(strconv.FormatInt(childID, 10))
	rewritten, err := rewriteRequestID(body, childIDRaw)
	if err != nil {
		return nil, err
	}

	ch := make(chan []byte, 1)
	key, err := b.writeToChild(rewritten, &pendingCall{originalID: append([]byte(nil), originalID...), ch: ch})
	if err != nil {
		return nil, err
	}

	timeout := b.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		b.removePending(key, "request context canceled")
		return nil, ctx.Err()
	case <-timer.C:
		b.removePending(key, "request timeout")
		return nil, fmt.Errorf("stdio child request timed out after %s", timeout)
	}
}

func (b *stdioBridge) writeToChild(body []byte, pending *pendingCall) (string, error) {
	var key string
	if pending != nil {
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return "", err
		}
		key = idKey(req.ID)
		if key == "" {
			return "", fmt.Errorf("request id is required")
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.available || b.stdin == nil {
		return "", fmt.Errorf("stdio child unavailable")
	}
	if pending != nil {
		b.pending[key] = *pending
	}
	if _, err := b.stdin.Write(append(body, '\n')); err != nil {
		if pending != nil {
			delete(b.pending, key)
		}
		return "", err
	}
	return key, nil
}

func (b *stdioBridge) removePending(key string, reason string) {
	if key == "" {
		return
	}
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		delete(b.pending, key)
		b.rememberLateResponseLocked(key, reason)
	}
	b.mu.Unlock()
}

func (b *stdioBridge) rememberLateResponseLocked(key string, reason string) {
	if b.late == nil {
		b.late = make(map[string]lateResponse)
	}
	now := time.Now()
	for existing, late := range b.late {
		if now.Sub(late.at) > lateResponseTTL {
			delete(b.late, existing)
		}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "request canceled"
	}
	b.late[key] = lateResponse{reason: reason, at: now}
}

func rewriteRequestID(body []byte, id json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj["id"] = id
	return json.Marshal(obj)
}

func restoreResponseID(body []byte, id json.RawMessage) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return append([]byte(nil), body...)
	}
	if idKey(id) == "" {
		delete(obj, "id")
	} else {
		obj["id"] = id
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return append([]byte(nil), body...)
	}
	return out
}

func responseHasError(body []byte) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return false
	}
	errRaw := bytes.TrimSpace(obj["error"])
	return len(errRaw) > 0 && string(errRaw) != "null"
}

func idKey(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	return string(raw)
}

func rpcErrorResponse(id json.RawMessage, code int, message string) []byte {
	if idKey(id) == "" {
		id = json.RawMessage("null")
	}
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(body)
	return data
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, status int, code int, message string) {
	writeJSON(w, status, rpcErrorResponse(id, code, message))
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	return data, nil
}

func randomToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func copyPrefixed(w io.Writer, prefix string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Fprintln(w, prefix+scanner.Text())
	}
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
		addr = defaultAddr
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr + "/healthz"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port + "/healthz"
}
