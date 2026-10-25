package runs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mostlydev/clawdapus/internal/cllama"
)

// Container is the read-only view of a worker container.
type Container struct {
	ID       string
	Running  bool
	Exited   bool
	ExitCode int
	ImageID  string
	Labels   map[string]string
}

// Executor performs container lifecycle through Docker Compose and reads
// container state through the Docker SDK. It never receives the orchestrator
// credential.
type Executor interface {
	Up(ctx context.Context, project, file string, env map[string]string) error
	Down(ctx context.Context, project, file string) error
	Containers(ctx context.Context, keyHash string) ([]Container, error)
	// Stdout returns at most limit bytes of stdout, and whether more existed.
	Stdout(ctx context.Context, containerID string, limit int) ([]byte, bool, error)
}

// Revoker closes a run's cllama identity (cllama#54). It must return nil only
// once cllama has durably revoked the agent and drained its dispatches.
type Revoker interface {
	Revoke(ctx context.Context, agentID string, generation uint64) error
}

// ErrAuthority means the orchestrator rejected the held credential (401/403).
var ErrAuthority = errors.New("orchestrator authority rejected")

// Notifier delivers lifecycle callbacks with the held orchestrator credential.
type Notifier interface {
	Started(ctx context.Context, rec *Record, credential string) error
	Heartbeat(ctx context.Context, rec *Record, credential string) error
	Ended(ctx context.Context, rec *Record, credential string, outcome, reason string, summary []byte) error
}

// Config configures a Supervisor.
type Config struct {
	PodDir            string
	Executor          Executor
	Revoker           Revoker
	Notifier          Notifier // nil disables credential custody
	HeartbeatInterval time.Duration
	Now               func() time.Time
	Logf              func(format string, args ...any)
}

// Supervisor owns per-run worker lifecycle for one pod.
//
// Each run key has two locks. The short admit lock covers only ledger reads
// and writes of the record and tombstone, so launch, cancel and credential
// replacement linearize without waiting on Docker, cllama or callbacks. The
// long step lock serializes advancement, which performs those external
// operations. Orchestrator-owned fields (generation, credential custody,
// authority expiry) are only written under the admit lock and are re-read and
// merged whenever advancement persists.
type Supervisor struct {
	cfg        Config
	podNS      string
	ledger     *Ledger
	runtimeDir string
	admitLocks sync.Map // key hash -> *sync.Mutex
	stepLocks  sync.Map // key hash -> *sync.Mutex
	changed    chan struct{}
	watchMu    sync.Mutex
	watchers   map[string][]chan struct{}

	testHookBeforeAccept func()
	testHookBeforeUp     func()
}

// Advancement bounds. Every external operation inherits a deadline.
const (
	// DefaultMaxRuntime applies only if a template carries no runtime limit.
	DefaultMaxRuntime = 30 * time.Minute

	advanceTimeout   = 3 * time.Minute
	advanceParallel  = 8
	maxStepsPerRound = 12
)

// NewSupervisor opens the ledger. Callers must hold the pod lock.
func NewSupervisor(cfg Config) (*Supervisor, error) {
	if cfg.Executor == nil || cfg.Revoker == nil {
		return nil, errors.New("runs: executor and revoker are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	ledger, err := OpenLedger(cfg.PodDir)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"projects", "results"} {
		if err := os.MkdirAll(filepath.Join(cfg.PodDir, StateDirName, dir), 0o700); err != nil {
			return nil, err
		}
	}
	return &Supervisor{
		cfg:        cfg,
		podNS:      PodNamespace(cfg.PodDir),
		ledger:     ledger,
		runtimeDir: filepath.Join(cfg.PodDir, ".claw-runtime"),
		changed:    make(chan struct{}, 1),
		watchers:   map[string][]chan struct{}{},
	}, nil
}

func keyMutex(m *sync.Map, k Key) *sync.Mutex {
	v, _ := m.LoadOrStore(k.Hash(), &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Supervisor) admit(k Key) func() {
	mu := keyMutex(&s.admitLocks, k)
	mu.Lock()
	return mu.Unlock
}

// LaunchRequest is a validated-at-the-edge launch.
type LaunchRequest struct {
	Key                Key
	Generation         uint64
	SpecDigest         string
	Spec               json.RawMessage
	Credential         string
	AuthorityExpiresAt time.Time
}

// Launch accepts a run idempotently. It persists intent, including the run's
// identity and secrets, before any side effect, then advances asynchronously.
func (s *Supervisor) Launch(ctx context.Context, req LaunchRequest) (*Record, bool, error) {
	if err := req.Key.Validate(); err != nil {
		return nil, false, apiErr(http.StatusBadRequest, "key_invalid", "%v", err)
	}
	if req.Generation == 0 {
		return nil, false, apiErr(http.StatusBadRequest, "generation_invalid", "generation must be a positive integer")
	}
	_, spec, digest, err := CanonicalSpec(req.Spec)
	if err != nil {
		return nil, false, err
	}
	if req.SpecDigest != digest {
		return nil, false, apiErr(http.StatusBadRequest, "digest_invalid", "spec_digest does not match the canonical spec")
	}
	if req.Credential != "" && s.cfg.Notifier == nil {
		return nil, false, apiErr(http.StatusUnprocessableEntity, "callbacks_disabled", "this supervisor has no callback origin configured")
	}
	tpl, _, tplErr := LoadTemplate(s.cfg.PodDir, spec.Template)

	unlock := s.admit(req.Key)
	defer unlock()

	if _, cancelled, err := s.ledger.Tombstone(req.Key); err != nil {
		return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	} else if cancelled {
		view, _ := s.viewLocked(req.Key)
		return view, false, apiErr(http.StatusConflict, "cancelled", "run was cancelled; a new attempt needs a new run_id")
	}
	existing, err := s.ledger.Get(req.Key)
	if err != nil {
		return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	if existing != nil {
		if req.Generation < existing.Generation {
			view, _ := s.viewLocked(req.Key)
			return view, false, apiErr(http.StatusConflict, "stale_generation", "generation %d is below %d", req.Generation, existing.Generation)
		}
		if existing.SpecDigest != digest {
			view, _ := s.viewLocked(req.Key)
			return view, false, apiErr(http.StatusConflict, "digest_mismatch", "run exists with a different spec digest")
		}
		changed := req.Generation > existing.Generation
		existing.Generation = req.Generation
		if req.Credential != "" && !existing.State.Terminal() && s.credentialState(existing) == CredentialMissing {
			if err := s.ledger.PutSecret(req.Key, "credential", req.Credential); err != nil {
				return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "credential not persisted")
			}
			existing.Credential = CredentialHeld
			changed = true
		}
		if changed {
			if err := s.ledger.Put(existing); err != nil {
				return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger not writable")
			}
			s.kick()
		}
		view, _ := s.viewLocked(req.Key)
		return view, false, nil
	}

	if tplErr != nil {
		return nil, false, apiErr(http.StatusUnprocessableEntity, "template_unknown", "the requested template is not compiled for this pod")
	}
	if err := checkGrant(tpl, spec); err != nil {
		return nil, false, err
	}
	now := s.cfg.Now().UTC()
	if !req.AuthorityExpiresAt.After(now) {
		return nil, false, apiErr(http.StatusUnprocessableEntity, "authority_expired", "authority_expires_at is not in the future")
	}
	if req.AuthorityExpiresAt.After(now.Add(tpl.Limits.MaxAuthority())) {
		return nil, false, apiErr(http.StatusUnprocessableEntity, "grant_exceeded", "authority_expires_at exceeds the template max-authority")
	}

	secret, err := randomHex(24)
	if err != nil {
		return nil, false, apiErr(http.StatusServiceUnavailable, "entropy_unavailable", "cannot mint run identity")
	}
	idSuffix, err := randomHex(16)
	if err != nil {
		return nil, false, apiErr(http.StatusServiceUnavailable, "entropy_unavailable", "cannot mint run identity")
	}
	rec := &Record{
		Key:                req.Key,
		Generation:         req.Generation,
		SpecDigest:         digest,
		Spec:               spec,
		TemplateID:         tpl.ID,
		ImageID:            tpl.ImageID,
		RunAgentID:         tpl.Name + "-run-" + idSuffix,
		RunKey:             req.Key.RunKey(s.podNS),
		Project:            "clawrun-" + s.podNS + "-" + req.Key.RunKey(s.podNS)[:16],
		State:              StateAccepted,
		AuthorityExpiresAt: req.AuthorityExpiresAt.UTC(),
		Credential:         CredentialNone,
	}
	rec.Timestamps.Accepted = &now
	// Secrets first, then the record: a record never references a secret that
	// was not durably written.
	if err := s.ledger.PutSecret(req.Key, "cllama", rec.RunAgentID+":"+secret); err != nil {
		return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run identity not persisted")
	}
	if req.Credential != "" {
		if err := s.ledger.PutSecret(req.Key, "credential", req.Credential); err != nil {
			return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "credential not persisted")
		}
		rec.Credential = CredentialHeld
	}
	if s.testHookBeforeAccept != nil {
		s.testHookBeforeAccept()
	}
	if err := s.ledger.Put(rec); err != nil {
		return nil, false, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger not writable")
	}
	s.kick()
	view, _ := s.viewLocked(req.Key)
	return view, true, nil
}

func checkGrant(tpl *Template, spec *Spec) error {
	if spec.Template != tpl.Name {
		return apiErr(http.StatusUnprocessableEntity, "template_unknown", "template mismatch")
	}
	if strings.TrimSpace(spec.Task) == "" {
		return apiErr(http.StatusBadRequest, "spec_invalid", "spec.task is required")
	}
	if len(spec.Task) > tpl.Limits.MaxTaskBytes {
		return apiErr(http.StatusUnprocessableEntity, "grant_exceeded", "spec.task exceeds %d bytes", tpl.Limits.MaxTaskBytes)
	}
	if spec.MaxRuntimeSeconds < 0 || spec.MaxRuntimeSeconds > tpl.Limits.MaxRuntimeSeconds {
		return apiErr(http.StatusUnprocessableEntity, "grant_exceeded", "spec.max_runtime_seconds exceeds %d", tpl.Limits.MaxRuntimeSeconds)
	}
	grant := make(map[string]struct{}, len(tpl.ToolsGrant))
	for _, name := range tpl.ToolsGrant {
		grant[name] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, name := range spec.Tools {
		if _, ok := grant[name]; !ok {
			return apiErr(http.StatusUnprocessableEntity, "grant_exceeded", "tool %q is not in the template grant", name)
		}
		if _, dup := seen[name]; dup {
			return apiErr(http.StatusBadRequest, "spec_invalid", "tool %q listed twice", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// Get returns a run's status view, or nil when it was never accepted or
// cancelled.
func (s *Supervisor) Get(k Key) (*Record, error) {
	if err := k.Validate(); err != nil {
		return nil, apiErr(http.StatusBadRequest, "key_invalid", "%v", err)
	}
	unlock := s.admit(k)
	defer unlock()
	return s.viewLocked(k)
}

// viewLocked overlays the tombstone and derived custody state on the stored
// record. A cancelled run never reports a non-cancel state again, even before
// advancement observes the tombstone. Callers hold the admit lock.
func (s *Supervisor) viewLocked(k Key) (*Record, error) {
	rec, err := s.ledger.Get(k)
	if err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	tombGen, cancelled, err := s.ledger.Tombstone(k)
	if err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	if rec == nil {
		if !cancelled {
			return nil, nil
		}
		// No record under the admit lock proves no identity, context or
		// container was ever created for this key.
		return &Record{Key: k, Generation: tombGen, State: StateStopped, Cancelled: true, Credential: CredentialNone,
			Callbacks: Callbacks{End: EndSkipped}}, nil
	}
	if cancelled {
		rec.Cancelled = true
		if tombGen > rec.Generation {
			rec.Generation = tombGen
		}
		if rec.State == StateAccepted || rec.State == StateRunning {
			rec.State = StateRevoking
		}
	}
	rec.Credential = s.credentialState(rec)
	return rec, nil
}

func (s *Supervisor) credentialState(rec *Record) string {
	if rec.State.Terminal() || rec.Credential == CredentialNone {
		return CredentialNone
	}
	if _, ok, err := s.ledger.Secret(rec.Key, "credential"); err != nil || !ok {
		return CredentialMissing
	}
	return CredentialHeld
}

// Cancel tombstones a run and returns immediately; advancement revokes and
// removes it. The tombstone is written under the admit lock, which Launch
// holds across its final tombstone check and accepted write, so a cancel
// always beats a launch that has not been accepted yet. A key with no record
// is terminal at once.
func (s *Supervisor) Cancel(ctx context.Context, k Key, generation uint64) (*Record, error) {
	if err := k.Validate(); err != nil {
		return nil, apiErr(http.StatusBadRequest, "key_invalid", "%v", err)
	}
	if generation == 0 {
		return nil, apiErr(http.StatusBadRequest, "generation_invalid", "generation must be a positive integer")
	}
	unlock := s.admit(k)
	defer unlock()
	rec, err := s.ledger.Get(k)
	if err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	tombGen, _, err := s.ledger.Tombstone(k)
	if err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	floor := tombGen
	if rec != nil && rec.Generation > floor {
		floor = rec.Generation
	}
	if generation < floor {
		view, _ := s.viewLocked(k)
		return view, apiErr(http.StatusConflict, "stale_generation", "generation %d is below %d", generation, floor)
	}
	if err := s.ledger.PutTombstone(k, generation); err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "tombstone not persisted")
	}
	s.kick()
	return s.viewLocked(k)
}

// ReplaceCredential installs a reissued orchestrator credential and,
// optionally, a later authority expiry. It is persisted before the ACK.
func (s *Supervisor) ReplaceCredential(ctx context.Context, k Key, generation uint64, credential string, expires *time.Time) (*Record, error) {
	if s.cfg.Notifier == nil {
		return nil, apiErr(http.StatusUnprocessableEntity, "callbacks_disabled", "this supervisor has no callback origin configured")
	}
	if strings.TrimSpace(credential) == "" {
		return nil, apiErr(http.StatusBadRequest, "credential_invalid", "credential is required")
	}
	if err := k.Validate(); err != nil {
		return nil, apiErr(http.StatusBadRequest, "key_invalid", "%v", err)
	}
	unlock := s.admit(k)
	defer unlock()
	rec, err := s.viewLocked(k)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, apiErr(http.StatusNotFound, "not_found", "run was never accepted")
	}
	if generation < rec.Generation {
		return rec, apiErr(http.StatusConflict, "stale_generation", "generation %d is below %d", generation, rec.Generation)
	}
	if rec.Cancelled || rec.State.Terminal() || rec.tearingDown() {
		return rec, apiErr(http.StatusConflict, "run_ending", "run no longer accepts credentials")
	}
	stored, err := s.ledger.Get(k)
	if err != nil || stored == nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	if expires != nil {
		tpl, _, err := LoadTemplate(s.cfg.PodDir, stored.Spec.Template)
		if err != nil {
			return rec, apiErr(http.StatusServiceUnavailable, "template_unavailable", "template unreadable")
		}
		now := s.cfg.Now().UTC()
		if !expires.After(now) || expires.After(now.Add(tpl.Limits.MaxAuthority())) {
			return rec, apiErr(http.StatusUnprocessableEntity, "grant_exceeded", "authority_expires_at must be in the future and within max-authority")
		}
		stored.AuthorityExpiresAt = expires.UTC()
	}
	if err := s.ledger.PutSecret(k, "credential", credential); err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "credential not persisted")
	}
	stored.Generation = generation
	stored.Credential = CredentialHeld
	if err := s.ledger.Put(stored); err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger not writable")
	}
	s.kick()
	return s.viewLocked(k)
}

// Result returns a stopped run's stored answer.
func (s *Supervisor) Result(k Key) ([]byte, *Record, error) {
	rec, err := s.Get(k)
	if err != nil {
		return nil, nil, err
	}
	if rec == nil {
		return nil, nil, apiErr(http.StatusNotFound, "not_found", "run was never accepted")
	}
	if rec.Result == nil {
		return nil, rec, apiErr(http.StatusNotFound, "no_result", "run has no result")
	}
	raw, err := readRegularFile(s.resultPath(k), int64(rec.Result.Bytes)+1)
	if err != nil {
		return nil, rec, apiErr(http.StatusServiceUnavailable, "result_unavailable", "result unreadable")
	}
	return raw, rec, nil
}

func (s *Supervisor) projectFile(k Key) string {
	return filepath.Join(s.cfg.PodDir, StateDirName, "projects", k.Hash(), "compose.yml")
}
func (s *Supervisor) resultPath(k Key) string {
	return filepath.Join(s.cfg.PodDir, StateDirName, "results", k.Hash())
}
func (s *Supervisor) contextDir(agentID string) string {
	return filepath.Join(s.runtimeDir, "context", agentID)
}

func (s *Supervisor) kick() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Wait blocks until the run's record changes or ctx ends.
func (s *Supervisor) Wait(ctx context.Context, k Key) {
	ch := make(chan struct{})
	s.watchMu.Lock()
	s.watchers[k.Hash()] = append(s.watchers[k.Hash()], ch)
	s.watchMu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
		s.watchMu.Lock()
		list := s.watchers[k.Hash()]
		for i, c := range list {
			if c == ch {
				s.watchers[k.Hash()] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(s.watchers[k.Hash()]) == 0 {
			delete(s.watchers, k.Hash())
		}
		s.watchMu.Unlock()
	}
}

func (s *Supervisor) notifyWatchers(k Key) {
	s.watchMu.Lock()
	chans := s.watchers[k.Hash()]
	delete(s.watchers, k.Hash())
	s.watchMu.Unlock()
	for _, ch := range chans {
		close(ch)
	}
}

// Run advances every non-terminal run until ctx ends. It wakes on any launch
// or cancel and otherwise ticks at interval.
func (s *Supervisor) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.AdvanceAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.changed:
		}
	}
}

// AdvanceAll advances every unfinished run concurrently, bounded, so one
// slow run cannot delay another run's cancel, expiry or teardown. A run whose
// step lock is busy is skipped this round.
func (s *Supervisor) AdvanceAll(ctx context.Context) {
	recs, err := s.ledger.All()
	if err != nil {
		s.cfg.Logf("[claw runs] ledger unreadable: %v", err)
		return
	}
	sem := make(chan struct{}, advanceParallel)
	var wg sync.WaitGroup
	for _, rec := range recs {
		if rec.State.Terminal() {
			continue
		}
		mu := keyMutex(&s.stepLocks, rec.Key)
		if !mu.TryLock() {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(k Key) {
			defer wg.Done()
			defer func() { <-sem }()
			defer mu.Unlock()
			s.advanceLocked(ctx, k)
		}(rec.Key)
	}
	wg.Wait()
}

// Advance drives one run's state machine as far as it can go now. Every step
// is idempotent, so restart reconciliation is the same code path.
func (s *Supervisor) Advance(ctx context.Context, k Key) {
	mu := keyMutex(&s.stepLocks, k)
	mu.Lock()
	defer mu.Unlock()
	s.advanceLocked(ctx, k)
}

func (s *Supervisor) advanceLocked(parent context.Context, k Key) {
	ctx, cancel := context.WithTimeout(parent, advanceTimeout)
	defer cancel()
	defer s.notifyWatchers(k)
	for i := 0; i < maxStepsPerRound; i++ {
		rec, err := s.loadForStep(k)
		if err != nil || rec == nil || rec.State.Terminal() {
			return
		}
		progressed, stepErr := s.step(ctx, rec)
		if stepErr != nil {
			rec.LastError = stepErr.Error()
		} else {
			rec.LastError = ""
		}
		if err := s.persist(rec); err != nil {
			s.cfg.Logf("[claw runs] %s: ledger write failed: %v", k.Hash(), err)
			return
		}
		if stepErr != nil {
			s.cfg.Logf("[claw runs] %s: %v", k.Hash(), stepErr)
			return
		}
		if !progressed {
			return
		}
	}
}

// loadForStep reads the record and folds in the tombstone.
func (s *Supervisor) loadForStep(k Key) (*Record, error) {
	unlock := s.admit(k)
	defer unlock()
	rec, err := s.ledger.Get(k)
	if err != nil || rec == nil {
		return rec, err
	}
	if gen, ok, err := s.ledger.Tombstone(k); err != nil {
		return nil, err
	} else if ok {
		rec.Cancelled = true
		if gen > rec.Generation {
			rec.Generation = gen
		}
	}
	return rec, nil
}

// persist writes advancement's changes, keeping orchestrator-owned fields as
// last written under the admit lock.
func (s *Supervisor) persist(rec *Record) error {
	unlock := s.admit(rec.Key)
	defer unlock()
	disk, err := s.ledger.Get(rec.Key)
	if err != nil {
		return err
	}
	if disk != nil {
		if disk.Generation > rec.Generation {
			rec.Generation = disk.Generation
		}
		rec.AuthorityExpiresAt = disk.AuthorityExpiresAt
		if !rec.State.Terminal() {
			rec.Credential = disk.Credential
		}
	}
	return s.ledger.Put(rec)
}

func (s *Supervisor) step(ctx context.Context, rec *Record) (bool, error) {
	now := s.cfg.Now().UTC()
	if !rec.tearingDown() {
		if !now.Before(rec.AuthorityExpiresAt) {
			rec.Failure = ReasonAuthorityExpired
			return true, nil
		}
		if rec.Deadline != nil && !now.Before(*rec.Deadline) {
			rec.Failure = ReasonTimeout
			return true, nil
		}
		switch rec.State {
		case StateAccepted:
			return s.stepSpawn(ctx, rec, now)
		case StateRunning:
			return s.stepMonitor(ctx, rec, now)
		}
		return false, fmt.Errorf("unexpected state %q", rec.State)
	}
	return s.stepTeardown(ctx, rec, now)
}

func (s *Supervisor) stepSpawn(ctx context.Context, rec *Record, now time.Time) (bool, error) {
	containers, err := s.cfg.Executor.Containers(ctx, rec.RunKey)
	if err != nil {
		return false, fmt.Errorf("list containers: %w", err)
	}
	if len(containers) > 0 {
		// A previous spawn created the container before its state was
		// recorded. Verify it, then adopt it; never start a second one.
		if reason := verifyContainers(rec, containers); reason != "" {
			rec.Failure = reason
			return true, nil
		}
		if !containers[0].Running && !containers[0].Exited {
			// Created but never started (a crash between create and start):
			// it never executed, so it is neither adopted nor started now.
			rec.Failure = ReasonSpawnError
			return true, nil
		}
		s.markRunning(rec, now)
		return true, nil
	}
	if rec.SpawnAttempted {
		// An earlier `compose up` left no container. Starting again could run
		// the task twice if the first attempt is merely slow to appear, so fail
		// closed.
		rec.Failure = ReasonSpawnError
		return true, nil
	}
	tpl, ctxTpl, err := LoadTemplate(s.cfg.PodDir, rec.Spec.Template)
	if err != nil || tpl.ID != rec.TemplateID {
		rec.Failure = ReasonMismatch
		return true, nil
	}
	// Never start a worker the proxy could not later revoke. The run stays
	// accepted and retries until its authority expires.
	if err := s.CheckRevocationSupport(ctx); err != nil {
		return false, err
	}
	token, ok, err := s.ledger.Secret(rec.Key, "cllama")
	if err != nil || !ok {
		rec.Failure = ReasonSpawnError
		return true, nil
	}
	if err := writeRunContext(s.runtimeDir, ctxTpl, rec, token); err != nil {
		rec.Failure = ReasonSpawnError
		return true, fmt.Errorf("write run context: %w", err)
	}
	project, err := renderProject(tpl, rec)
	if err != nil {
		rec.Failure = ReasonSpawnError
		return true, err
	}
	file := s.projectFile(rec.Key)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return false, err
	}
	if err := writeFileAtomic(file, project, 0o600); err != nil {
		return false, err
	}
	s.observeCaptureEpoch(rec)
	rec.SpawnAttempted = true
	s.setDeadline(rec, now)
	if err := s.persist(rec); err != nil {
		return false, err
	}
	env := inputEnv(tpl, rec, rec.Spec.Task, token)
	if s.testHookBeforeUp != nil {
		s.testHookBeforeUp()
	}
	if err := s.cfg.Executor.Up(ctx, rec.Project, file, env); err != nil {
		rec.Failure = ReasonSpawnError
		return true, fmt.Errorf("compose up failed")
	}
	return true, nil
}

// Trace journal epoch record that cllama writes in the pod's session history
// directory (tracejournal.EpochFileName and EpochSchema).
const (
	traceEpochFile   = ".trace-journal-epoch.json"
	traceEpochSchema = "cllama.trace.epoch/v1"
)

// errCaptureEpochInvalid is the fixed warning for an unreadable epoch record;
// request-supplied content never reaches it.
var errCaptureEpochInvalid = errors.New("trace capture epoch record is invalid")

// ReadCaptureEpoch returns the capture epoch cllama last wrote. It reports
// what the file says, not which proxy process is live. An empty epoch with a
// nil error means there is no epoch file; a malformed record yields
// errCaptureEpochInvalid. No value is ever guessed.
func (s *Supervisor) ReadCaptureEpoch() (string, error) {
	raw, err := readRegularFile(filepath.Join(s.cfg.PodDir, ".claw-session-history", traceEpochFile), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errCaptureEpochInvalid
	}
	var file struct {
		Schema       string `json:"schema"`
		CaptureEpoch string `json:"capture_epoch"`
		StartedAt    string `json:"started_at"`
	}
	if json.Unmarshal(raw, &file) != nil || file.Schema != traceEpochSchema || !validCaptureEpoch(file.CaptureEpoch) {
		return "", errCaptureEpochInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, file.StartedAt); err != nil {
		return "", errCaptureEpochInvalid
	}
	return file.CaptureEpoch, nil
}

// observeCaptureEpoch records the capture epoch once, immediately before the
// worker starts. It is never rewritten, and neither an absent nor a malformed
// record fails the launch.
func (s *Supervisor) observeCaptureEpoch(rec *Record) {
	if rec.CaptureEpoch != "" {
		return
	}
	epoch, err := s.ReadCaptureEpoch()
	if err != nil {
		s.cfg.Logf("runs: run %s: %v; capture epoch not recorded", rec.Key.RunID, err)
		return
	}
	if epoch == "" {
		return
	}
	// Stamp the read itself: the step's clock predates the container lookup
	// and revocation self-test.
	observed := s.cfg.Now().UTC()
	rec.CaptureEpoch = epoch
	rec.CaptureEpochObservedAt = &observed
}

func validCaptureEpoch(epoch string) bool {
	if epoch == "" || len(epoch) > 128 {
		return false
	}
	for _, r := range epoch {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (s *Supervisor) markRunning(rec *Record, now time.Time) {
	rec.State = StateRunning
	if rec.Timestamps.Running == nil {
		rec.Timestamps.Running = &now
	}
	s.setDeadline(rec, now)
}

// setDeadline fixes the absolute runtime deadline once, before the worker is
// started; reconciliation never moves it.
func (s *Supervisor) setDeadline(rec *Record, now time.Time) {
	if rec.Deadline != nil {
		return
	}
	runtime := rec.Spec.MaxRuntimeSeconds
	if tpl, _, err := LoadTemplate(s.cfg.PodDir, rec.Spec.Template); err == nil && (runtime == 0 || runtime > tpl.Limits.MaxRuntimeSeconds) {
		runtime = tpl.Limits.MaxRuntimeSeconds
	}
	if runtime <= 0 {
		runtime = int64(DefaultMaxRuntime.Seconds())
	}
	deadline := now.Add(time.Duration(runtime) * time.Second)
	rec.Deadline = &deadline
}

func verifyContainers(rec *Record, containers []Container) string {
	if len(containers) > 1 {
		return ReasonDuplicate
	}
	c := containers[0]
	l := c.Labels
	if l[LabelRunKey] != rec.RunKey || l[LabelPodNS] != podNamespaceOf(rec) || l[LabelInstall] != rec.Key.Installation || l[LabelRunID] != rec.Key.RunID ||
		l[LabelTemplateID] != rec.TemplateID || l[LabelDigest] != rec.SpecDigest || c.ImageID != rec.ImageID {
		return ReasonMismatch
	}
	if gen, err := strconv.ParseUint(l[LabelGeneration], 10, 64); err != nil || gen > rec.Generation {
		return ReasonMismatch
	}
	return ""
}

func (s *Supervisor) stepMonitor(ctx context.Context, rec *Record, now time.Time) (bool, error) {
	containers, err := s.cfg.Executor.Containers(ctx, rec.RunKey)
	if err != nil {
		return false, fmt.Errorf("list containers: %w", err)
	}
	if len(containers) == 0 {
		rec.Failure = ReasonContainerMissing
		return true, nil
	}
	if reason := verifyContainers(rec, containers); reason != "" {
		rec.Failure = reason
		return true, nil
	}
	c := containers[0]
	if !c.Running && !c.Exited {
		rec.Failure = ReasonSpawnError
		return true, nil
	}
	if c.Exited {
		if err := s.captureResult(ctx, rec, c); err != nil {
			return false, err
		}
		rec.Exit = &ExitInfo{Code: c.ExitCode, At: now}
		if c.ExitCode != 0 {
			rec.Failure = ReasonExitNonZero
		}
		return true, nil
	}
	return s.deliverProgress(ctx, rec, now)
}

// deliverProgress sends start and heartbeat callbacks while running.
func (s *Supervisor) deliverProgress(ctx context.Context, rec *Record, now time.Time) (bool, error) {
	if s.cfg.Notifier == nil || rec.Credential != CredentialHeld {
		return false, nil
	}
	credential, ok, err := s.ledger.Secret(rec.Key, "credential")
	if err != nil || !ok {
		// Reported as "missing" by the status view until the orchestrator
		// reissues; authority_expires_at still bounds the run.
		return false, nil
	}
	if rec.Callbacks.Start == "" {
		if err := s.cfg.Notifier.Started(ctx, rec, credential); err != nil {
			return s.callbackFailed(rec, err)
		}
		rec.Callbacks.Start = CallbackAcked
		rec.Callbacks.LastHeartbeat = &now
		return true, nil
	}
	if rec.Callbacks.LastHeartbeat == nil || now.Sub(*rec.Callbacks.LastHeartbeat) >= s.cfg.HeartbeatInterval {
		if err := s.cfg.Notifier.Heartbeat(ctx, rec, credential); err != nil {
			return s.callbackFailed(rec, err)
		}
		rec.Callbacks.LastHeartbeat = &now
		return true, nil
	}
	return false, nil
}

func (s *Supervisor) callbackFailed(rec *Record, err error) (bool, error) {
	if errors.Is(err, ErrAuthority) {
		rec.Failure = ReasonAuthorityExpired
		return true, nil
	}
	// Transient: authority_expires_at still bounds the run.
	return false, fmt.Errorf("callback delivery failed")
}

func (s *Supervisor) captureResult(ctx context.Context, rec *Record, c Container) error {
	if rec.Result != nil || rec.Failure == ReasonResultMissing {
		return nil
	}
	tpl, _, err := LoadTemplate(s.cfg.PodDir, rec.Spec.Template)
	if err != nil {
		return fmt.Errorf("load template: %w", err)
	}
	limit := tpl.Limits.MaxResultBytes
	// Read past the answer limit to allow for the runner banner, but bound it.
	raw, sourceTruncated, err := s.cfg.Executor.Stdout(ctx, c.ID, limit+resultPreambleBytes)
	if err != nil {
		return fmt.Errorf("read worker stdout: %w", err)
	}
	answer, truncated, found := extractAnswer(raw, tpl.Worker.Result.Marker, limit)
	if !found {
		// No answer marker in the readable stream: the result is missing,
		// never a known-complete empty answer.
		if c.ExitCode == 0 {
			rec.Failure = ReasonResultMissing
		}
		return nil
	}
	if err := writeFileAtomic(s.resultPath(rec.Key), answer, 0o600); err != nil {
		return err
	}
	rec.Result = &ResultInfo{Bytes: len(answer), Truncated: truncated || sourceTruncated}
	return nil
}

// resultPreambleBytes bounds runner output (banner, notices) read before the
// answer marker.
const resultPreambleBytes = 64 << 10

// extractAnswer returns the bytes after the first marker. Markers inside the
// answer itself are content, never control. found is false when the stream
// has no marker. An answer longer than limit is cut at a rune boundary.
func extractAnswer(stdout []byte, marker string, limit int) (answer []byte, truncated, found bool) {
	answer = stdout
	if marker != "" {
		idx := bytes.Index(stdout, []byte(marker))
		if idx < 0 {
			return nil, false, false
		}
		answer = stdout[idx+len(marker):]
	}
	answer = bytes.TrimSuffix(answer, []byte("\n"))
	if len(answer) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(answer[cut]) {
			cut--
		}
		return append([]byte(nil), answer[:cut]...), true, true
	}
	return append([]byte(nil), answer...), false, true
}

func (s *Supervisor) stepTeardown(ctx context.Context, rec *Record, now time.Time) (bool, error) {
	if rec.State != StateRevoking && rec.State != StateRevoked {
		rec.State = StateRevoking
		return true, nil
	}
	var revokeErr error
	if !rec.Revoked {
		if rec.RunAgentID != "" {
			if err := emptyRunTools(s.contextDir(rec.RunAgentID)); err != nil {
				return false, fmt.Errorf("empty run tools: %w", err)
			}
			revokeErr = s.cfg.Revoker.Revoke(ctx, rec.RunAgentID, rec.Generation)
		}
		if revokeErr == nil {
			rec.Revoked = true
			rec.RevocationProof = ProofCllamaAck
			rec.State = StateRevoked
			rec.Timestamps.Revoked = &now
			return true, nil
		}
	}
	containers, err := s.cfg.Executor.Containers(ctx, rec.RunKey)
	if err != nil {
		return false, fmt.Errorf("list containers: %w", err)
	}
	if len(containers) > 0 {
		if !rec.Cancelled && rec.Result == nil && len(containers) == 1 && containers[0].Exited {
			if err := s.captureResult(ctx, rec, containers[0]); err != nil {
				return false, err
			}
		}
		if err := s.cfg.Executor.Down(ctx, rec.Project, s.projectFile(rec.Key)); err != nil {
			return false, fmt.Errorf("compose down failed")
		}
		remaining, err := s.cfg.Executor.Containers(ctx, rec.RunKey)
		if err != nil {
			return false, fmt.Errorf("list containers: %w", err)
		}
		if len(remaining) > 0 {
			return false, fmt.Errorf("%d labelled containers remain after compose down", len(remaining))
		}
		if revokeErr != nil {
			return true, fmt.Errorf("worker removed; cllama revoke not acknowledged: %w", revokeErr)
		}
		return true, nil
	}
	if revokeErr != nil {
		return false, fmt.Errorf("cllama revoke not acknowledged: %w", revokeErr)
	}
	if rec.Callbacks.End == "" {
		rec.Callbacks.End = s.deliverEnd(ctx, rec, now)
		if rec.Callbacks.End == "" {
			return false, fmt.Errorf("end callback delivery failed")
		}
		return true, nil
	}
	if rec.RunAgentID != "" {
		if err := os.RemoveAll(s.contextDir(rec.RunAgentID)); err != nil {
			return false, err
		}
	}
	_ = os.RemoveAll(filepath.Dir(s.projectFile(rec.Key)))
	if err := s.ledger.DeleteSecret(rec.Key, "cllama"); err != nil {
		return false, err
	}
	if err := s.ledger.DeleteSecret(rec.Key, "credential"); err != nil {
		return false, err
	}
	rec.Credential = CredentialNone
	if rec.Failure != "" {
		rec.State = StateFailed
	} else {
		rec.State = StateStopped
	}
	rec.Timestamps.Stopped = &now
	return true, nil
}

// deliverEnd resolves the start callback (for a worker that spawned) and
// then the end callback for a run that ended on its own. It returns "" to
// retry later.
func (s *Supervisor) deliverEnd(ctx context.Context, rec *Record, now time.Time) string {
	if rec.Cancelled || s.cfg.Notifier == nil || rec.Credential == CredentialNone || rec.Failure == ReasonAuthorityExpired {
		if rec.Callbacks.Start == "" {
			rec.Callbacks.Start = CallbackSkipped
		}
		return CallbackSkipped
	}
	credential, ok, err := s.ledger.Secret(rec.Key, "credential")
	if err != nil || !ok {
		if rec.Callbacks.Start == "" {
			rec.Callbacks.Start = CallbackSkipped
		}
		return CallbackSkipped
	}
	expired := !now.Before(rec.AuthorityExpiresAt)
	if rec.Callbacks.Start == "" {
		if rec.Timestamps.Running == nil {
			// The worker never ran (spawn failed or it was never started).
			rec.Callbacks.Start = CallbackSkipped
		} else {
			switch err := s.cfg.Notifier.Started(ctx, rec, credential); {
			case err == nil:
				rec.Callbacks.Start = CallbackAcked
			case errors.Is(err, ErrAuthority):
				rec.Callbacks.Start = CallbackRejected
				return CallbackRejected
			case expired:
				rec.Callbacks.Start = CallbackAbandoned
				return CallbackAbandoned
			default:
				return ""
			}
		}
	}
	outcome, reason := "succeeded", ""
	if rec.Failure != "" {
		outcome, reason = "failed", rec.Failure
	}
	var summary []byte
	if rec.Result != nil {
		summary, _ = readRegularFile(s.resultPath(rec.Key), int64(rec.Result.Bytes)+1)
	}
	err = s.cfg.Notifier.Ended(ctx, rec, credential, outcome, reason, summary)
	switch {
	case err == nil:
		return CallbackAcked
	case errors.Is(err, ErrAuthority):
		return CallbackRejected
	case expired:
		return CallbackAbandoned
	}
	return ""
}

// writeRunContext clones the template's cllama context for a run: a fresh
// identity and token, model policy as compiled for the template, and managed
// tools filtered to the run's allowlist. An empty allowlist writes an explicit
// empty manifest.
func writeRunContext(runtimeDir string, ctxTpl *ContextTemplate, rec *Record, token string) error {
	in := ctxTpl.Input
	in.AgentID = rec.RunAgentID
	meta := make(map[string]any, len(in.Metadata)+3)
	for k, v := range in.Metadata {
		meta[k] = v
	}
	meta["token"] = token
	meta["run_key"] = rec.RunKey
	meta["run_installation"] = rec.Key.Installation
	meta["run_id"] = rec.Key.RunID
	in.Metadata = meta
	allowed := make(map[string]struct{}, len(rec.Spec.Tools))
	for _, name := range rec.Spec.Tools {
		allowed[name] = struct{}{}
	}
	tools := make([]cllama.ToolManifestEntry, 0, len(rec.Spec.Tools))
	services := map[string]struct{}{}
	for _, tool := range in.Tools {
		if _, ok := allowed[tool.Name]; ok {
			tools = append(tools, tool)
			services[tool.Execution.Service] = struct{}{}
		}
	}
	in.Tools = tools
	for _, feed := range in.Feeds {
		services[feed.Source] = struct{}{}
	}
	auth := make([]cllama.ServiceAuthEntry, 0, len(in.ServiceAuth))
	for _, entry := range in.ServiceAuth {
		if _, ok := services[entry.Service]; ok {
			auth = append(auth, entry)
		}
	}
	in.ServiceAuth = auth
	if err := os.MkdirAll(filepath.Join(runtimeDir, "context"), 0o755); err != nil {
		return err
	}
	if err := cllama.GenerateContextDir(runtimeDir, []cllama.AgentContextInput{in}); err != nil {
		return err
	}
	if len(tools) == 0 {
		return writeEmptyTools(filepath.Join(runtimeDir, "context", rec.RunAgentID), ctxTpl.Input.ToolPolicy)
	}
	return nil
}

func writeEmptyTools(dir string, policy *cllama.ToolPolicy) error {
	p := cllama.DefaultToolPolicy
	if policy != nil {
		p = *policy
	}
	raw, err := json.MarshalIndent(cllama.ToolManifest{Version: 1, Tools: []cllama.ToolManifestEntry{}, Policy: p}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "tools.json"), append(raw, '\n'), 0o644)
}

// emptyRunTools removes every managed tool from a live run context before
// revocation, so the next request already sees no tools.
func emptyRunTools(dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return writeEmptyTools(dir, nil)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WorkerLister lists every run-worker container for a pod.
type WorkerLister interface {
	RunWorkers(ctx context.Context, podNS string) ([]Container, error)
}

// Orphans returns labelled run workers with no ledger record or tombstone.
// They can only exist after ledger loss, because a run's project is recorded
// before `compose create`; the supervisor refuses to serve while any exist.
func (s *Supervisor) Orphans(ctx context.Context) ([]Container, error) {
	lister, ok := s.cfg.Executor.(WorkerLister)
	if !ok {
		return nil, nil
	}
	workers, err := lister.RunWorkers(ctx, s.podNS)
	if err != nil {
		return nil, err
	}
	recs, err := s.ledger.All()
	if err != nil {
		return nil, err
	}
	known := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		if r.RunKey != "" {
			known[r.RunKey] = struct{}{}
		}
	}
	var orphans []Container
	for _, w := range workers {
		if _, ok := known[w.Labels[LabelRunKey]]; !ok {
			orphans = append(orphans, w)
		}
	}
	return orphans, nil
}

// LiveKeys returns every unfinished run.
func (s *Supervisor) LiveKeys() ([]Key, error) {
	recs, err := s.ledger.All()
	if err != nil {
		return nil, err
	}
	var out []Key
	for _, r := range recs {
		if !r.State.Terminal() {
			out = append(out, r.Key)
		}
	}
	return out, nil
}

// FinalizeWithoutProxy ends a run whose cllama acknowledgement is unavailable
// during a full pod teardown. The caller must have removed the pod (proxy
// included) and verified it is gone; proxyGone re-checks that. With no worker
// container and no proxy process, nothing can hold the run's authority, and
// deleting its context means a restarted proxy cannot authenticate it.
// Remote effects that were already dispatched are not undone.
func (s *Supervisor) FinalizeWithoutProxy(ctx context.Context, k Key, proxyGone func(context.Context) (bool, error)) error {
	mu := keyMutex(&s.stepLocks, k)
	mu.Lock()
	defer mu.Unlock()
	defer s.notifyWatchers(k)
	rec, err := s.loadForStep(k)
	if err != nil || rec == nil || rec.State.Terminal() {
		return err
	}
	if !rec.Cancelled {
		return fmt.Errorf("run %s/%s is not cancelled", k.Installation, k.RunID)
	}
	containers, err := s.cfg.Executor.Containers(ctx, rec.RunKey)
	if err != nil {
		return err
	}
	if len(containers) > 0 {
		return fmt.Errorf("run %s/%s still has %d worker container(s)", k.Installation, k.RunID, len(containers))
	}
	gone, err := proxyGone(ctx)
	if err != nil {
		return err
	}
	if !gone {
		return fmt.Errorf("the pod's proxy is still present; it must be removed before finalizing without its acknowledgement")
	}
	now := s.cfg.Now().UTC()
	if !rec.Revoked {
		rec.Revoked = true
		rec.RevocationProof = ProofProxyStopped
		rec.Timestamps.Revoked = &now
	}
	if rec.RunAgentID != "" {
		if err := os.RemoveAll(s.contextDir(rec.RunAgentID)); err != nil {
			return err
		}
	}
	_ = os.RemoveAll(filepath.Dir(s.projectFile(k)))
	if err := s.ledger.DeleteSecret(k, "cllama"); err != nil {
		return err
	}
	if err := s.ledger.DeleteSecret(k, "credential"); err != nil {
		return err
	}
	if rec.Callbacks.End == "" {
		rec.Callbacks.End = CallbackSkipped
	}
	if rec.Callbacks.Start == "" {
		rec.Callbacks.Start = CallbackSkipped
	}
	rec.Credential = CredentialNone
	if rec.Failure != "" {
		rec.State = StateFailed
	} else {
		rec.State = StateStopped
	}
	rec.Timestamps.Stopped = &now
	rec.LastError = ""
	return s.persist(rec)
}

// RemoveOrphan removes one orphaned run worker through Compose. The project
// must be a run project of this pod's namespace (clawrun-<ns>-<key>); the
// executor passes an empty project on stdin, never an ambient file.
func (s *Supervisor) RemoveOrphan(ctx context.Context, c Container) error {
	project := c.Labels["com.docker.compose.project"]
	if c.Labels[LabelPodNS] != s.podNS || !strings.HasPrefix(project, "clawrun-"+s.podNS+"-") || c.Labels[LabelRunKey] == "" {
		return fmt.Errorf("container %s is not a run worker of this pod", c.ID)
	}
	recs, err := s.ledger.All()
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.RunKey == c.Labels[LabelRunKey] {
			return fmt.Errorf("container %s belongs to a known run; it is not an orphan", c.ID)
		}
	}
	return s.cfg.Executor.Down(ctx, project, "")
}

// probeAgentID is a reserved identity that can never belong to a worker or a
// declared claw: agent IDs derive from Compose service names, which cannot
// start with "_". Revoking it is idempotent and proves the proxy implements
// the durable revocation fence.
func (s *Supervisor) probeAgentID() string { return "_claw-runs-probe-" + s.podNS }

// CheckRevocationSupport proves the running proxy acknowledges durable
// revocation (cllama#54) before any worker is started. An older or
// misconfigured proxy (no endpoint, no revocation dir) fails this check.
func (s *Supervisor) CheckRevocationSupport(ctx context.Context) error {
	if err := s.cfg.Revoker.Revoke(ctx, s.probeAgentID(), 1); err != nil {
		return fmt.Errorf("the pod's cllama does not acknowledge durable revocation (needs cllama with revocation support and CLAW_REVOCATION_DIR): %w", err)
	}
	return nil
}
