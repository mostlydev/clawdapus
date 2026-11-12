package runs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// State is the public run state.
type State string

const (
	StateAccepted State = "accepted"
	StateRunning  State = "running"
	StateRevoking State = "revoking"
	StateRevoked  State = "revoked"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
)

func (s State) Terminal() bool { return s == StateStopped || s == StateFailed }

// Failure reasons.
const (
	ReasonExitNonZero            = "exit_nonzero"
	ReasonTimeout                = "timeout"
	ReasonAuthorityExpired       = "authority_expired"
	ReasonContentSnapshotChanged = "content_snapshot_changed"
	ReasonSpawnError             = "spawn_error"
	ReasonContainerMissing       = "container_missing"
	ReasonDuplicate              = "duplicate"
	ReasonMismatch               = "mismatch"
	ReasonResultMissing          = "result_missing"
	ReasonHomeFull               = "home_full"
	ReasonHomeMeterUnavailable   = "home_meter_unavailable"
)

var (
	installationRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	runIDRe        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// Key identifies a run.
type Key struct {
	Installation string `json:"installation"`
	RunID        string `json:"run_id"`
}

func (k Key) Validate() error {
	if !installationRe.MatchString(k.Installation) {
		return fmt.Errorf("installation must match %s", installationRe)
	}
	if !runIDRe.MatchString(k.RunID) {
		return fmt.Errorf("run_id must match %s", runIDRe)
	}
	return nil
}

// Hash is the stable, label-safe digest of a key. Project names, file names
// and container labels derive from it, never from caller strings directly.
func (k Key) Hash() string {
	sum := sha256.Sum256([]byte(k.Installation + "/" + k.RunID))
	return hex.EncodeToString(sum[:16])
}

// PodNamespace is the server-owned identity of a pod on this host, derived
// from its absolute directory. Two pods with the same name (or the same run
// keys) never share a Compose project, label value or worker network.
func PodNamespace(podDir string) string {
	abs, err := filepath.Abs(podDir)
	if err != nil {
		abs = podDir
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:6])
}

// RunKey is the pod-scoped, label-safe identity of a run's containers.
func (k Key) RunKey(podNS string) string {
	sum := sha256.Sum256([]byte(podNS + "\x00" + k.Installation + "/" + k.RunID))
	return hex.EncodeToString(sum[:16])
}

// Spec is the immutable, digest-covered launch request body.
type Spec struct {
	Content           map[string]string `json:"content,omitempty"`
	Template          string            `json:"template"`
	Task              string            `json:"task"`
	Tools             []string          `json:"tools"`
	MaxRuntimeSeconds int64             `json:"max_runtime_seconds,omitempty"`
	// Home is the opaque task-home ID. It is required by a task-home template
	// and refused by a stateless one.
	Home string `json:"home,omitempty"`
	// TemplateID, when set, pins the launch to the compiled template the
	// caller built this spec against. A launch is refused if the template
	// named by Template has another ID by the time it is admitted.
	TemplateID string `json:"template_id,omitempty"`
}

// ModelRef is one entry of a template's compiled model chain: the slot and
// the concrete provider/model reference.
type ModelRef struct {
	Slot string `json:"slot"`
	Ref  string `json:"ref"`
	// Requested is the model family reference the pod wrote for this
	// entry, when the concrete Ref was resolved from one.
	Requested string `json:"requested,omitempty"`
}

// ExitInfo records an observed worker exit. Observing an exit is not terminal.
type ExitInfo struct {
	Code int       `json:"code"`
	At   time.Time `json:"at"`
	// RunnerCode is the exit code of the runner inside a task-home worker,
	// when the worker's wrapper reported that the runner itself failed.
	RunnerCode *int `json:"runner_code,omitempty"`
}

// Failure reasons a task-home worker's wrapper reports about itself. The
// worker is untrusted: its report can only choose among these names for its
// own non-zero exit. It grants nothing and replaces no supervisor finding.
const (
	ReasonWorkerUsage           = "worker_usage"
	ReasonWorkerHomeInvalid     = "worker_home_invalid"
	ReasonWorkerSessionLocator  = "worker_session_locator_malformed"
	ReasonWorkerRunnerExit      = "worker_runner_exit"
	ReasonWorkerTurnNotStored   = "worker_turn_not_stored"
	ReasonWorkerSessionUnproven = "worker_session_unproven"
	workerReasonPrefix          = "worker_"
)

var workerReasons = map[string]bool{
	ReasonWorkerUsage: true, ReasonWorkerHomeInvalid: true, ReasonWorkerSessionLocator: true,
	ReasonWorkerRunnerExit: true, ReasonWorkerTurnNotStored: true, ReasonWorkerSessionUnproven: true,
}

// ResultInfo describes the stored result.
type ResultInfo struct {
	Bytes     int  `json:"bytes"`
	Truncated bool `json:"truncated"`
	// Outcome and Session come from a task-home worker's result header.
	Outcome string `json:"outcome,omitempty"`
	Session string `json:"session,omitempty"`
}

// Timestamps records state transitions.
type Timestamps struct {
	Accepted *time.Time `json:"accepted,omitempty"`
	Running  *time.Time `json:"running,omitempty"`
	Revoked  *time.Time `json:"revoked,omitempty"`
	Stopped  *time.Time `json:"stopped,omitempty"`
}

// Revocation proofs. A run is revoked only with one of these.
const (
	// ProofCllamaAck: cllama durably revoked the identity and drained it.
	ProofCllamaAck = "cllama_ack"
	// ProofProxyStopped: the whole pod (proxy included) was removed and the
	// run's context deleted, so no process can hold or regain its authority.
	ProofProxyStopped = "proxy_stopped"
)

// Credential custody states reported to the orchestrator.
const (
	CredentialNone    = "none"
	CredentialHeld    = "held"
	CredentialMissing = "missing"
)

// Record is one run's durable ledger entry.
type Record struct {
	Content    []ContentSelection `json:"content,omitempty"`
	Version    int                `json:"version"`
	Key        Key                `json:"key"`
	Generation uint64             `json:"generation"`
	SpecDigest string             `json:"spec_digest,omitempty"`
	Spec       *Spec              `json:"spec,omitempty"`
	TemplateID string             `json:"template_id,omitempty"`
	// Models is the concrete model chain of the template the run was
	// admitted with, in routing order.
	Models []ModelRef `json:"models,omitempty"`
	// PrivateSubnet is the address block of a task-home run's own network.
	PrivateSubnet string `json:"private_subnet,omitempty"`
	ImageID       string `json:"image_id,omitempty"`
	RunAgentID    string `json:"run_agent_id,omitempty"`
	RunKey        string `json:"run_key,omitempty"` // pod-scoped container label value
	Project       string `json:"project,omitempty"`
	// HomeHash names the task home this run reserves, from accept until the
	// run is terminal. Empty for a stateless run.
	HomeHash           string      `json:"home_hash,omitempty"`
	State              State       `json:"state"`
	Cancelled          bool        `json:"cancelled"`
	Revoked            bool        `json:"revoked"`
	RevocationProof    string      `json:"revocation_proof,omitempty"`
	SpawnAttempted     bool        `json:"spawn_attempted"`
	Exit               *ExitInfo   `json:"exit_observed,omitempty"`
	Failure            string      `json:"failure,omitempty"`
	AuthorityExpiresAt time.Time   `json:"authority_expires_at"`
	Deadline           *time.Time  `json:"deadline,omitempty"`
	Credential         string      `json:"credential"`
	Callbacks          Callbacks   `json:"callbacks"`
	Result             *ResultInfo `json:"result,omitempty"`
	Timestamps         Timestamps  `json:"timestamps"`
	LastError          string      `json:"last_error,omitempty"`

	// The trace capture epoch cllama had last written when the worker was
	// started. Observed once at spawn; it does not prove the live process.
	CaptureEpoch           string     `json:"capture_epoch,omitempty"`
	CaptureEpochObservedAt *time.Time `json:"capture_epoch_observed_at,omitempty"`

	// ProxyBoot names the cllama process that answered the revocation
	// self-test just before the worker was started.
	ProxyBoot string `json:"proxy_boot,omitempty"`
	// Served is what cllama reported about the run's model access when it
	// acknowledged the revocation.
	Served *ServedInfo `json:"served,omitempty"`
}

// ServedInfo says which credential classes served a run's model requests.
type ServedInfo struct {
	// By counts served requests by credential class.
	By map[string]int `json:"by,omitempty"`
	// Complete is true when the proxy process that reported the counts is
	// the one that was running when the worker started. Otherwise the proxy
	// restarted during the run and the counts are partial.
	Complete bool `json:"complete"`
	// PlanFailure is the run's most recent plan-route failure reason. It can
	// be set on a run that a later entry of the chain then served.
	PlanFailure string `json:"plan_failure,omitempty"`
}

// Callback outcomes. Only CallbackAcked means the orchestrator acknowledged.
const (
	CallbackAcked     = "acked"
	CallbackRejected  = "rejected"  // 401/403: the orchestrator's authority ended
	CallbackAbandoned = "abandoned" // undeliverable before authority expired
	CallbackSkipped   = "skipped"   // no credential held, or the run was cancelled

	// End aliases kept for readability at end-callback call sites.
	EndAcked     = CallbackAcked
	EndRejected  = CallbackRejected
	EndAbandoned = CallbackAbandoned
	EndSkipped   = CallbackSkipped
)

// Callbacks records lifecycle callback delivery. Start is always resolved
// before End for a worker that spawned.
type Callbacks struct {
	Start         string     `json:"start,omitempty"`
	End           string     `json:"end,omitempty"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
}

// tearingDown reports whether the run must be revoked and removed.
func (r *Record) tearingDown() bool {
	return r.Cancelled || r.Exit != nil || r.Failure != ""
}

// Ledger is the supervisor's durable store: one record file per run, plus
// separate 0600 secret files that are never mounted anywhere.
type Ledger struct {
	dir string
}

const recordVersion = 1

// OpenLedger opens (creating if absent) the ledger under podDir.
func OpenLedger(podDir string) (*Ledger, error) {
	if err := EnsurePrivateDir(filepath.Join(podDir, StateDirName)); err != nil {
		return nil, fmt.Errorf("run state dir: %w", err)
	}
	dir := filepath.Join(podDir, StateDirName, "ledger")
	if err := EnsurePrivateDir(dir); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	return &Ledger{dir: dir}, nil
}

func (l *Ledger) recordPath(k Key) string { return filepath.Join(l.dir, k.Hash()+".json") }
func (l *Ledger) secretPath(k Key, kind string) string {
	return filepath.Join(l.dir, k.Hash()+"."+kind)
}

// Get returns the record for k, or (nil, nil) when none exists.
func (l *Ledger) Get(k Key) (*Record, error) {
	var rec Record
	err := readJSONFile(l.recordPath(k), &rec)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rec.Version != recordVersion || rec.Key != k {
		return nil, fmt.Errorf("ledger record for %s/%s is corrupt", k.Installation, k.RunID)
	}
	return &rec, nil
}

// Put durably writes rec.
func (l *Ledger) Put(rec *Record) error {
	rec.Version = recordVersion
	return writeJSONFile(l.recordPath(rec.Key), rec)
}

// All returns every record, sorted by key hash.
func (l *Ledger) All() ([]*Record, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".cllama") || strings.HasSuffix(name, ".credential") || strings.HasSuffix(name, ".tombstone") {
			// Atomic-write temp files and 0600 secret files.
			continue
		}
		if !strings.HasSuffix(name, ".json") || !e.Type().IsRegular() {
			// Anything else in the ledger dir is unexpected; never let a
			// replaced or foreign entry silently drop a live run.
			return nil, fmt.Errorf("ledger entry %s is not a regular run record", name)
		}
		var rec Record
		if err := readJSONFile(filepath.Join(l.dir, name), &rec); err != nil {
			return nil, err
		}
		if rec.Version != recordVersion || rec.Key.Hash()+".json" != name {
			return nil, fmt.Errorf("ledger record %s is corrupt", name)
		}
		out = append(out, &rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.Hash() < out[j].Key.Hash() })
	return out, nil
}

// HasLiveRuns reports whether any record is non-terminal. A missing ledger
// has no live runs.
func HasLiveRuns(podDir string) (bool, error) {
	dir := filepath.Join(podDir, StateDirName, "ledger")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	l := &Ledger{dir: dir}
	recs, err := l.All()
	if err != nil {
		return false, err
	}
	for _, r := range recs {
		if !r.State.Terminal() {
			return true, nil
		}
	}
	return false, nil
}

// PutSecret durably stores a secret (kind "cllama" or "credential").
func (l *Ledger) PutSecret(k Key, kind, value string) error {
	return writeFileAtomic(l.secretPath(k, kind), []byte(value), 0o600)
}

// Secret reads a stored secret; ok is false when absent.
func (l *Ledger) Secret(k Key, kind string) (string, bool, error) {
	raw, err := readRegularFile(l.secretPath(k, kind), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

// DeleteSecret removes a stored secret.
func (l *Ledger) DeleteSecret(k Key, kind string) error {
	err := os.Remove(l.secretPath(k, kind))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// PutTombstone durably records a cancel. It never lowers a recorded
// generation. Tombstones are written without the run's key lock so a cancel
// is never delayed behind a slow lifecycle operation.
func (l *Ledger) PutTombstone(k Key, generation uint64) error {
	if old, ok, err := l.Tombstone(k); err != nil {
		return err
	} else if ok && old >= generation {
		return nil
	}
	return writeFileAtomic(l.secretPath(k, "tombstone"), []byte(strconv.FormatUint(generation, 10)), 0o600)
}

// Tombstone reports whether k was cancelled, and at which generation.
func (l *Ledger) Tombstone(k Key) (uint64, bool, error) {
	raw, err := readRegularFile(l.secretPath(k, "tombstone"), 32)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	gen, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || gen == 0 {
		return 0, false, fmt.Errorf("tombstone for %s is corrupt", k.Hash())
	}
	return gen, true, nil
}

// Tombstoned lists keys hashes with a tombstone but no record.
func (l *Ledger) orphanTombstones() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tombstone") && !strings.HasPrefix(name, ".") {
			out = append(out, strings.TrimSuffix(name, ".tombstone"))
		}
	}
	return out, nil
}
