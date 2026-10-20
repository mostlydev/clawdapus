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
	ReasonExitNonZero      = "exit_nonzero"
	ReasonTimeout          = "timeout"
	ReasonAuthorityExpired = "authority_expired"
	ReasonSpawnError       = "spawn_error"
	ReasonContainerMissing = "container_missing"
	ReasonDuplicate        = "duplicate"
	ReasonMismatch         = "mismatch"
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

// ProjectName is the deterministic Compose project for a run.
func (k Key) ProjectName() string { return "clawrun-" + k.Hash() }

// Spec is the immutable, digest-covered launch request body.
type Spec struct {
	Template          string   `json:"template"`
	Task              string   `json:"task"`
	Tools             []string `json:"tools"`
	MaxRuntimeSeconds int64    `json:"max_runtime_seconds,omitempty"`
}

// ExitInfo records an observed worker exit. Observing an exit is not terminal.
type ExitInfo struct {
	Code int       `json:"code"`
	At   time.Time `json:"at"`
}

// ResultInfo describes the stored result.
type ResultInfo struct {
	Bytes     int  `json:"bytes"`
	Truncated bool `json:"truncated"`
}

// Timestamps records state transitions.
type Timestamps struct {
	Accepted *time.Time `json:"accepted,omitempty"`
	Running  *time.Time `json:"running,omitempty"`
	Revoked  *time.Time `json:"revoked,omitempty"`
	Stopped  *time.Time `json:"stopped,omitempty"`
}

// Credential custody states reported to the orchestrator.
const (
	CredentialNone    = "none"
	CredentialHeld    = "held"
	CredentialMissing = "missing"
)

// Record is one run's durable ledger entry.
type Record struct {
	Version            int         `json:"version"`
	Key                Key         `json:"key"`
	Generation         uint64      `json:"generation"`
	SpecDigest         string      `json:"spec_digest,omitempty"`
	Spec               *Spec       `json:"spec,omitempty"`
	TemplateID         string      `json:"template_id,omitempty"`
	ImageID            string      `json:"image_id,omitempty"`
	RunAgentID         string      `json:"run_agent_id,omitempty"`
	Project            string      `json:"project,omitempty"`
	State              State       `json:"state"`
	Cancelled          bool        `json:"cancelled"`
	Revoked            bool        `json:"revoked"`
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
}

// Callbacks records lifecycle callback acknowledgement.
type Callbacks struct {
	Started       bool       `json:"started"`
	Ended         bool       `json:"ended"`
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
	dir := filepath.Join(podDir, StateDirName, "ledger")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create ledger: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
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
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
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
