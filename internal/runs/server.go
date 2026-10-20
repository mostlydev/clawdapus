package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// ProtocolVersion is the run supervisor wire protocol version.
const ProtocolVersion = 1

const (
	SocketName      = "claw-runs.sock"
	maxRequestBytes = 64 << 10
	requestDeadline = 10 * time.Second
	maxWait         = 60 * time.Second
)

// CheckSocketDir verifies the socket directory is a real directory (not a
// symlink) owned by uid with mode 0700 or 0750. Any ancestor writable by group
// or others must carry the sticky bit.
func CheckSocketDir(dir string, uid uint32) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot read ownership", dir)
	}
	if st.Uid != uid {
		return fmt.Errorf("%s is owned by uid %d, expected %d", dir, st.Uid, uid)
	}
	if perm := info.Mode().Perm(); perm != 0o700 && perm != 0o750 {
		return fmt.Errorf("%s must have mode 0700 or 0750 (has %o)", dir, perm)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for parent := filepath.Dir(abs); ; parent = filepath.Dir(parent) {
		pinfo, err := os.Stat(parent)
		if err != nil {
			return err
		}
		if pinfo.Mode().Perm()&0o022 != 0 && pinfo.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("ancestor %s is group- or world-writable without the sticky bit", parent)
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return nil
}

// peerListener rejects connections from UIDs outside the allowlist before
// any request byte is read.
type peerListener struct {
	*net.UnixListener
	allowed map[uint32]struct{}
	logf    func(string, ...any)
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.UnixListener.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err != nil {
			conn.Close()
			continue
		}
		if _, ok := l.allowed[uid]; !ok {
			l.logf("[claw runs] rejected connection from uid %d", uid)
			conn.Close()
			continue
		}
		return conn, nil
	}
}

// Server exposes a Supervisor over a Unix socket. Trust model: any process
// running as an allowed UID (or root) holds every template and installation
// grant of this pod. Peer-UID checks authenticate a Unix account, not a
// program.
type Server struct {
	Sup        *Supervisor
	Pod        string
	AllowedUID []uint32
	Logf       func(string, ...any)
}

// Listen creates the socket in dir (which must pass CheckSocketDir) and
// serves until ctx ends.
func (srv *Server) Listen(ctx context.Context, dir string) error {
	if srv.Logf == nil {
		srv.Logf = func(string, ...any) {}
	}
	self := uint32(os.Geteuid())
	if err := CheckSocketDir(dir, self); err != nil {
		return fmt.Errorf("socket directory: %w", err)
	}
	path := filepath.Join(dir, SocketName)
	if info, err := os.Lstat(path); err == nil {
		st, _ := info.Sys().(*syscall.Stat_t)
		if info.Mode()&os.ModeSocket == 0 || st == nil || st.Uid != self {
			return fmt.Errorf("%s exists and is not this user's socket; refusing to replace it", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	ln.SetUnlinkOnClose(true)
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return err
	}
	allowed := map[uint32]struct{}{self: {}}
	for _, uid := range srv.AllowedUID {
		allowed[uid] = struct{}{}
	}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       requestDeadline,
		WriteTimeout:      maxWait + requestDeadline,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	err = hs.Serve(&peerListener{UnixListener: ln, allowed: allowed, logf: srv.Logf})
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Handler returns the v1 API.
func (srv *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /v1/identity", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"protocol": ProtocolVersion, "pod": srv.Pod, "uid": os.Geteuid()})
	})
	mux.HandleFunc("POST /v1/runs", srv.handleLaunch)
	mux.HandleFunc("GET /v1/runs/{installation}/{run}", srv.handleStatus)
	mux.HandleFunc("POST /v1/runs/{installation}/{run}/cancel", srv.handleCancel)
	mux.HandleFunc("POST /v1/runs/{installation}/{run}/credential", srv.handleCredential)
	mux.HandleFunc("GET /v1/runs/{installation}/{run}/result", srv.handleResult)
	return mux
}

type launchBody struct {
	Installation       string          `json:"installation"`
	RunID              string          `json:"run_id"`
	Generation         uint64          `json:"generation"`
	SpecDigest         string          `json:"spec_digest"`
	Spec               json.RawMessage `json:"spec"`
	Credential         string          `json:"credential,omitempty"`
	AuthorityExpiresAt time.Time       `json:"authority_expires_at"`
}

func (srv *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var body launchBody
	if !decodeBody(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
	defer cancel()
	rec, created, err := srv.Sup.Launch(ctx, LaunchRequest{
		Key:                Key{Installation: body.Installation, RunID: body.RunID},
		Generation:         body.Generation,
		SpecDigest:         body.SpecDigest,
		Spec:               body.Spec,
		Credential:         body.Credential,
		AuthorityExpiresAt: body.AuthorityExpiresAt,
	})
	if err != nil {
		writeError(w, err, rec)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, statusView(rec))
}

func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	k := Key{Installation: r.PathValue("installation"), RunID: r.PathValue("run")}
	rec, err := srv.Sup.Get(k)
	if err != nil {
		writeError(w, err, nil)
		return
	}
	want, timeout, err := parseWait(r)
	if err != nil {
		writeError(w, err, nil)
		return
	}
	if want != "" && rec != nil {
		if rec, err = srv.waitFor(r.Context(), k, rec, want, timeout); err != nil {
			writeError(w, err, nil)
			return
		}
	}
	if rec == nil {
		writeError(w, apiErr(http.StatusNotFound, "not_found", "run was never accepted"), nil)
		return
	}
	writeJSON(w, http.StatusOK, statusView(rec))
}

var waitableStates = map[State]bool{StateRunning: true, StateRevoked: true, StateStopped: true, StateFailed: true}

// parseWait validates the optional ?wait=<state>&timeout=<duration> pair.
func parseWait(r *http.Request) (State, time.Duration, error) {
	q := r.URL.Query()
	want := State(q.Get("wait"))
	timeout := maxWait
	if raw := q.Get("timeout"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 || d > maxWait {
			return "", 0, apiErr(http.StatusBadRequest, "request_invalid", "timeout must be a duration in (0, %s]", maxWait)
		}
		timeout = d
	}
	if want != "" && !waitableStates[want] {
		return "", 0, apiErr(http.StatusBadRequest, "request_invalid", "wait must be one of running, revoked, stopped, failed")
	}
	return want, timeout, nil
}

func (srv *Server) waitFor(parent context.Context, k Key, rec *Record, want State, timeout time.Duration) (*Record, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	for rec != nil && !reached(rec, want) && ctx.Err() == nil {
		srv.Sup.Wait(ctx, k)
		var err error
		if rec, err = srv.Sup.Get(k); err != nil {
			return nil, err
		}
	}
	if rec == nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run record disappeared")
	}
	return rec, nil
}

// reached reports whether rec is at or past want, or terminal.
func reached(rec *Record, want State) bool {
	if rec.State == want || rec.State.Terminal() {
		return true
	}
	return want == StateRevoked && rec.Revoked && rec.Cancelled
}

type cancelBody struct {
	Generation uint64 `json:"generation"`
}

func (srv *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	var body cancelBody
	if !decodeBody(w, r, &body) {
		return
	}
	want, timeout, err := parseWait(r)
	if err != nil {
		writeError(w, err, nil)
		return
	}
	k := Key{Installation: r.PathValue("installation"), RunID: r.PathValue("run")}
	rec, err := srv.Sup.Cancel(r.Context(), k, body.Generation)
	if err != nil {
		writeError(w, err, rec)
		return
	}
	if want != "" {
		if rec, err = srv.waitFor(r.Context(), k, rec, want, timeout); err != nil {
			writeError(w, err, nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, statusView(rec))
}

type credentialBody struct {
	Generation         uint64     `json:"generation"`
	Credential         string     `json:"credential"`
	AuthorityExpiresAt *time.Time `json:"authority_expires_at,omitempty"`
}

func (srv *Server) handleCredential(w http.ResponseWriter, r *http.Request) {
	var body credentialBody
	if !decodeBody(w, r, &body) {
		return
	}
	k := Key{Installation: r.PathValue("installation"), RunID: r.PathValue("run")}
	rec, err := srv.Sup.ReplaceCredential(r.Context(), k, body.Generation, body.Credential, body.AuthorityExpiresAt)
	if err != nil {
		writeError(w, err, rec)
		return
	}
	writeJSON(w, http.StatusOK, statusView(rec))
}

func (srv *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	k := Key{Installation: r.PathValue("installation"), RunID: r.PathValue("run")}
	raw, rec, err := srv.Sup.Result(k)
	if err != nil {
		writeError(w, err, rec)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Claw-Result-Truncated", strconv.FormatBool(rec.Result.Truncated))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// StatusView is the public run status.
type StatusView struct {
	Installation       string      `json:"installation"`
	RunID              string      `json:"run_id"`
	Generation         uint64      `json:"generation"`
	SpecDigest         string      `json:"spec_digest,omitempty"`
	Template           string      `json:"template,omitempty"`
	RunAgentID         string      `json:"run_agent_id,omitempty"`
	State              State       `json:"state"`
	Cancelled          bool        `json:"cancelled"`
	Revoked            bool        `json:"revoked"`
	ExitCode           *int        `json:"exit_code"`
	Failure            string      `json:"failure,omitempty"`
	Result             *ResultInfo `json:"result,omitempty"`
	Credential         string      `json:"credential"`
	Callbacks          Callbacks   `json:"callbacks"`
	AuthorityExpiresAt *time.Time  `json:"authority_expires_at,omitempty"`
	Deadline           *time.Time  `json:"deadline,omitempty"`
	Timestamps         Timestamps  `json:"timestamps"`
	Pending            string      `json:"pending,omitempty"`
}

func statusView(rec *Record) *StatusView {
	if rec == nil {
		return nil
	}
	v := &StatusView{
		Installation: rec.Key.Installation, RunID: rec.Key.RunID, Generation: rec.Generation,
		SpecDigest: rec.SpecDigest, RunAgentID: rec.RunAgentID, State: rec.State, Cancelled: rec.Cancelled,
		Revoked: rec.Revoked, Failure: rec.Failure, Result: rec.Result, Credential: rec.Credential,
		Callbacks: rec.Callbacks, Deadline: rec.Deadline, Timestamps: rec.Timestamps, Pending: rec.LastError,
	}
	if rec.Spec != nil {
		v.Template = rec.Spec.Template
	}
	if rec.Exit != nil {
		code := rec.Exit.Code
		v.ExitCode = &code
	}
	if !rec.AuthorityExpiresAt.IsZero() {
		t := rec.AuthorityExpiresAt
		v.AuthorityExpiresAt = &t
	}
	return v
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, apiErr(http.StatusBadRequest, "request_invalid", "request body must be a single JSON object of at most %d bytes with known fields", maxRequestBytes), nil)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, apiErr(http.StatusBadRequest, "request_invalid", "request body must be a single JSON object"), nil)
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error, rec *Record) {
	var apiError *APIError
	if !errors.As(err, &apiError) {
		apiError = apiErr(http.StatusInternalServerError, "internal", "internal error")
	}
	writeJSON(w, apiError.Status, map[string]any{"error": apiError, "run": statusView(rec)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
