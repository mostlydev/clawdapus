package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// HomesDirName is the pod-relative directory holding task homes (ADR-027).
// It is private, and neither claw up nor claw down removes it.
const HomesDirName = ".claw-homes"

const (
	homeDataDir  = "home"
	homeMetaFile = "meta.json"
	homeTrashTag = ".deleting-"
)

var homeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// ValidHomeID reports whether id is a well-formed opaque task-home ID.
func ValidHomeID(id string) bool { return homeIDRe.MatchString(id) }

// HomeHash maps an installation and a home ID to the home's directory name.
// The caller's ID never appears in a path.
func HomeHash(installation, home string) string {
	sum := sha256.Sum256([]byte(installation + "\x00" + home))
	return hex.EncodeToString(sum[:16])
}

// HomeMeta is the supervisor-owned description of a home. It lives beside the
// mounted directory, never inside it.
type HomeMeta struct {
	Installation string    `json:"installation"`
	Home         string    `json:"home"`
	CreatedAt    time.Time `json:"created_at"`
	LastRunID    string    `json:"last_run_id,omitempty"`
}

// HomeStatus is the view returned by the home verbs.
type HomeStatus struct {
	Installation string     `json:"installation"`
	Home         string     `json:"home"`
	Exists       bool       `json:"exists"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	LastRunID    string     `json:"last_run_id,omitempty"`
	ReservedBy   string     `json:"reserved_by,omitempty"`
}

// HomeSweeper is optionally implemented by an Executor that can remove a
// directory whose entries belong to the worker user and so cannot be removed
// by the supervisor's own user.
type HomeSweeper interface {
	SweepDir(ctx context.Context, project, image, dir string) error
}

func (s *Supervisor) homesDir() string { return filepath.Join(s.cfg.PodDir, HomesDirName) }

func (s *Supervisor) homeRoot(hash string) string { return filepath.Join(s.homesDir(), hash) }

// homeMountSource is the host directory mounted into the worker. Its parent
// holds supervisor metadata and is never mounted.
func (s *Supervisor) homeMountSource(hash string) string {
	return filepath.Join(s.homeRoot(hash), homeDataDir)
}

// homeHolder returns the unfinished run that reserves a home, if any. A home
// is reserved exactly while a non-terminal record names it, so the ledger is
// the only source of truth. Callers hold homeMu.
func (s *Supervisor) homeHolder(hash string, except Key) (*Record, error) {
	recs, err := s.ledger.All()
	if err != nil {
		return nil, err
	}
	var holder *Record
	for _, r := range recs {
		if r.State.Terminal() {
			continue
		}
		if r.Spec == nil {
			return nil, fmt.Errorf("run %s: unfinished record has no spec", r.Key.RunID)
		}
		// An unfinished record whose reservation does not match its own spec
		// is ambiguous: it may hold any home. Fail closed for every home.
		want := ""
		if r.Spec != nil && r.Spec.Home != "" {
			want = HomeHash(r.Key.Installation, r.Spec.Home)
		}
		if r.HomeHash != want {
			return nil, fmt.Errorf("run %s: home reservation does not match its spec", r.Key.RunID)
		}
		if r.HomeHash == hash && r.Key != except && holder == nil {
			holder = r
		}
	}
	return holder, nil
}

// ensureHome creates a home's directories and records the run using it. It
// runs at spawn, while the run's record already reserves the home.
func (s *Supervisor) ensureHome(rec *Record) error {
	if err := EnsurePrivateDir(s.homesDir()); err != nil {
		return err
	}
	root := s.homeRoot(rec.HomeHash)
	if err := EnsurePrivateDir(root); err != nil {
		return err
	}
	data := filepath.Join(root, homeDataDir)
	if err := os.Mkdir(data, 0o777); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(data)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be a directory", data)
	}
	// The worker runs as a fixed unprivileged user; the 0700 parent is what
	// keeps other host users out.
	if info.Mode().Perm() != 0o777 {
		if err := os.Chmod(data, 0o777); err != nil {
			return err
		}
	}
	// The deliverables directory is the supervisor's, so it can always list
	// it; the worker can write into it. If the worker replaced it with
	// something of its own, that is left alone and read as found.
	deliverables := filepath.Join(data, DeliverablesDir)
	if err := os.Mkdir(deliverables, 0o777); err == nil {
		if err := os.Chmod(deliverables, 0o777); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	meta := HomeMeta{Installation: rec.Key.Installation, Home: rec.Spec.Home, CreatedAt: s.cfg.Now().UTC()}
	var existing HomeMeta
	if err := readJSONFile(filepath.Join(root, homeMetaFile), &existing); err == nil && !existing.CreatedAt.IsZero() {
		meta.CreatedAt = existing.CreatedAt
	}
	meta.LastRunID = rec.Key.RunID
	return writeJSONFile(filepath.Join(root, homeMetaFile), meta)
}

func validateHomeRef(installation, home string) error {
	if err := (Key{Installation: installation, RunID: "x"}).Validate(); err != nil {
		return apiErr(http.StatusBadRequest, "key_invalid", "%v", err)
	}
	if !ValidHomeID(home) {
		return apiErr(http.StatusBadRequest, "home_invalid", "home must be 1-128 characters of A-Z a-z 0-9 . _ - and not start with a dot")
	}
	return nil
}

// HomeStatus reports whether a home exists and which run, if any, reserves it.
func (s *Supervisor) HomeStatus(installation, home string) (*HomeStatus, error) {
	if err := validateHomeRef(installation, home); err != nil {
		return nil, err
	}
	hash := HomeHash(installation, home)
	s.homeMu.Lock()
	defer s.homeMu.Unlock()
	out := &HomeStatus{Installation: installation, Home: home}
	holder, err := s.homeHolder(hash, Key{})
	if err != nil {
		return nil, apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	if holder != nil {
		out.ReservedBy = holder.Key.RunID
	}
	if info, err := os.Lstat(s.homeRoot(hash)); err == nil && info.IsDir() {
		out.Exists = true
		var meta HomeMeta
		if err := readJSONFile(filepath.Join(s.homeRoot(hash), homeMetaFile), &meta); err == nil {
			if !meta.CreatedAt.IsZero() {
				created := meta.CreatedAt
				out.CreatedAt = &created
			}
			out.LastRunID = meta.LastRunID
		}
	}
	return out, nil
}

// DeleteHome removes a home. It is refused while any unfinished run reserves
// the home, and shares the admission lock with Launch, so a delete and a
// launch for the same home cannot interleave. Deleting an absent home
// succeeds.
func (s *Supervisor) DeleteHome(ctx context.Context, installation, home string) error {
	if err := validateHomeRef(installation, home); err != nil {
		return err
	}
	if err := s.detachHome(HomeHash(installation, home)); err != nil {
		return err
	}
	// Removal runs outside the admission lock: a large or hostile tree must
	// not delay admission for unrelated homes. The ID is already free.
	return s.sweepHomeTrash(ctx)
}

// detachHome renames a free home out of the way under the admission lock.
// The rename frees the home ID atomically: a launch admitted afterwards gets
// a new, empty home even while removal is still pending.
func (s *Supervisor) detachHome(hash string) error {
	s.homeMu.Lock()
	defer s.homeMu.Unlock()
	holder, err := s.homeHolder(hash, Key{})
	if err != nil {
		return apiErr(http.StatusServiceUnavailable, "ledger_unavailable", "run ledger unreadable")
	}
	if holder != nil {
		return apiErr(http.StatusConflict, "home_busy", "home is reserved by run %s", holder.Key.RunID)
	}
	root := s.homeRoot(hash)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return apiErr(http.StatusServiceUnavailable, "home_unavailable", "home directory is not removable")
	}
	suffix, err := randomHex(8)
	if err != nil {
		return apiErr(http.StatusServiceUnavailable, "entropy_unavailable", "cannot name the removal")
	}
	if err := os.Rename(root, root+homeTrashTag+suffix); err != nil {
		return apiErr(http.StatusServiceUnavailable, "home_unavailable", "home directory is not removable")
	}
	if err := syncDir(s.homesDir()); err != nil {
		return apiErr(http.StatusServiceUnavailable, "home_unavailable", "home removal not durable")
	}
	return nil
}

// sweepHomeTrash removes every renamed-away home. os.RemoveAll never follows
// a symlink. Entries the worker user created in its own subdirectories may
// not be removable by the supervisor's user; those go through the executor's
// sweeper, which removes them as the worker user through Compose.
func (s *Supervisor) sweepHomeTrash(ctx context.Context) error {
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	entries, err := os.ReadDir(s.homesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return apiErr(http.StatusServiceUnavailable, "home_unavailable", "homes directory unreadable")
	}
	var failed int
	for _, e := range entries {
		if !e.IsDir() || !strings.Contains(e.Name(), homeTrashTag) {
			continue
		}
		dir := filepath.Join(s.homesDir(), e.Name())
		if err := removeTree(dir); err == nil {
			continue
		}
		sweeper, ok := s.cfg.Executor.(HomeSweeper)
		if !ok {
			failed++
			continue
		}
		image := s.anyTemplateImage()
		if image == "" {
			failed++
			continue
		}
		if err := sweeper.SweepDir(ctx, "clawsweep-"+s.podNS+"-"+e.Name()[len(e.Name())-16:], image, filepath.Join(dir, homeDataDir)); err != nil {
			failed++
			continue
		}
		if err := removeTree(dir); err != nil {
			failed++
		}
	}
	if failed > 0 {
		return apiErr(http.StatusServiceUnavailable, "home_unavailable", "%d removed home(s) still hold files; the home ID is free and removal will be retried", failed)
	}
	return nil
}

// anyTemplateImage returns the pinned image of a compiled task-home template,
// used to remove worker-owned files as the worker user.
func (s *Supervisor) anyTemplateImage() string {
	names, err := ListTemplates(s.cfg.PodDir)
	if err != nil {
		return ""
	}
	for _, name := range names {
		tpl, _, err := LoadTemplate(s.cfg.PodDir, name)
		if err == nil && tpl.Worker.Home != nil {
			return tpl.ImageID
		}
	}
	return ""
}

// removeTree removes dir and everything under it without following symlinks.
// A worker may leave directories it made unreadable; those owned by the
// supervisor's user are reopened first. Only directories are chmodded, and
// only as reported by Lstat, so a symlink is never the target.
func removeTree(dir string) error {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			if info, lerr := os.Lstat(path); lerr == nil && info.IsDir() && info.Mode().Perm()&0o700 != 0o700 {
				_ = os.Chmod(path, 0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(dir)
}
