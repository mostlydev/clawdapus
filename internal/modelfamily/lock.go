package modelfamily

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LockFileName is the lock's name beside the pod file.
const LockFileName = "models.lock"

// Lock records which concrete model each family resolved to.
type Lock struct {
	Version  int                  `json:"version"`
	Families map[string]LockEntry `json:"families"`
}

// LockEntry is one family's resolution and the evidence it rests on.
type LockEntry struct {
	Provider string `json:"provider"`
	Pattern  string `json:"pattern"`
	ID       string `json:"id"`
	// PreviousID is the model the family resolved to before this one.
	PreviousID string `json:"previous_id,omitempty"`
	// MaxInput and MaxOutput are the listed maximum rates the choice was
	// bounded with, in USD per million tokens.
	MaxInput  float64 `json:"max_input"`
	MaxOutput float64 `json:"max_output"`
	// CatalogAt is when the provider's complete listing was read.
	CatalogAt time.Time `json:"catalog_at"`
	// CllamaTag and CllamaImageID name the image whose price table was used.
	CllamaTag     string `json:"cllama_tag"`
	CllamaImageID string `json:"cllama_image_id"`
}

// LockKey is the key of a family in the lock: its provider and name.
func LockKey(provider, name string) string { return provider + "/@" + name }

// LoadLock reads a lock file. A missing file is an empty lock.
func LoadLock(path string) (*Lock, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Lock{Version: 1, Families: map[string]LockEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var lock Lock
	if err := json.Unmarshal(raw, &lock); err != nil || lock.Version != 1 {
		return nil, fmt.Errorf("%s is not a version 1 model lock", filepath.Base(path))
	}
	if lock.Families == nil {
		lock.Families = map[string]LockEntry{}
	}
	return &lock, nil
}

// Save writes the lock atomically. It holds no secret.
func (l *Lock) Save(path string) error {
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".models.lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Reuse reports whether a locked choice may stand when the catalog cannot
// be read. Only the catalog's freshness is waived. The entry must have been
// made for this same provider and pattern, and its model must still be a
// member that is exactly priced within the current ceilings and passes the
// current data policy. It returns "" when the entry may be reused.
func (f Family) Reuse(entry LockEntry, prices *Prices, policy func(id string) error) string {
	if entry.ID == "" {
		return ReasonNoMember
	}
	if entry.Provider != f.Provider || entry.Pattern != f.Pattern {
		return "family_changed"
	}
	return f.Eligible(entry.ID, prices, policy)
}
