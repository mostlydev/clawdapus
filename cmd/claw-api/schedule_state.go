package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

const scheduleStateVersion = 1

type scheduleStateStore struct {
	path string

	mu    sync.RWMutex
	state schedulepkg.StateFile
}

func newScheduleStateStore(governanceDir string, manifest *schedulepkg.Manifest) (*scheduleStateStore, error) {
	governanceDir = filepath.Clean(governanceDir)
	store := &scheduleStateStore{
		path: filepath.Join(governanceDir, "schedule-state.json"),
		state: schedulepkg.StateFile{
			Version:     scheduleStateVersion,
			Invocations: make(map[string]schedulepkg.InvocationState),
		},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	if err := store.syncManifest(manifest); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *scheduleStateStore) Snapshot() schedulepkg.StateFile {
	if s == nil {
		return schedulepkg.StateFile{Version: scheduleStateVersion}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Clone()
}

func (s *scheduleStateStore) Invocation(id string) (schedulepkg.InvocationState, bool) {
	if s == nil {
		return schedulepkg.InvocationState{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.state.Invocations[id]
	if !ok {
		return schedulepkg.InvocationState{}, false
	}
	return state.Clone(), true
}

func (s *scheduleStateStore) Update(mutator func(*schedulepkg.StateFile)) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizeScheduleState(&s.state)
	if mutator != nil {
		mutator(&s.state)
	}
	now := time.Now().UTC()
	s.state.Version = scheduleStateVersion
	s.state.UpdatedAt = &now
	return s.persistLocked()
}

func (s *scheduleStateStore) UpdateInvocation(id string, mutator func(*schedulepkg.InvocationState) error) (schedulepkg.InvocationState, error) {
	if s == nil {
		return schedulepkg.InvocationState{}, fmt.Errorf("schedule state unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizeScheduleState(&s.state)
	state, ok := s.state.Invocations[id]
	if !ok {
		return schedulepkg.InvocationState{}, fmt.Errorf("schedule %q not found", id)
	}
	if mutator != nil {
		if err := mutator(&state); err != nil {
			return schedulepkg.InvocationState{}, err
		}
	}
	now := time.Now().UTC()
	s.state.Version = scheduleStateVersion
	s.state.UpdatedAt = &now
	s.state.Invocations[id] = state
	if err := s.persistLocked(); err != nil {
		return schedulepkg.InvocationState{}, err
	}
	return state.Clone(), nil
}

func (s *scheduleStateStore) syncManifest(manifest *schedulepkg.Manifest) error {
	return s.Update(func(state *schedulepkg.StateFile) {
		if manifest == nil {
			state.Pod = ""
			state.Invocations = make(map[string]schedulepkg.InvocationState)
			return
		}
		state.Pod = manifest.Pod
		if state.Invocations == nil {
			state.Invocations = make(map[string]schedulepkg.InvocationState, len(manifest.Invocations))
		}
		keep := make(map[string]struct{}, len(manifest.Invocations))
		for _, inv := range manifest.Invocations {
			keep[inv.ID] = struct{}{}
			if _, ok := state.Invocations[inv.ID]; !ok {
				state.Invocations[inv.ID] = schedulepkg.InvocationState{}
			}
		}
		for id := range state.Invocations {
			if _, ok := keep[id]; !ok {
				delete(state.Invocations, id)
			}
		}
	})
}

func (s *scheduleStateStore) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read schedule state %q: %w", s.path, err)
	}
	var state schedulepkg.StateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("parse schedule state %q: %w", s.path, err)
	}
	normalizeScheduleState(&state)
	s.state = state
	return nil
}

func (s *scheduleStateStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o777); err != nil {
		return fmt.Errorf("create schedule state dir: %w", err)
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schedule state: %w", err)
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o666); err != nil {
		return fmt.Errorf("write schedule state temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace schedule state: %w", err)
	}
	return nil
}

func normalizeScheduleState(state *schedulepkg.StateFile) {
	if state == nil {
		return
	}
	state.Version = scheduleStateVersion
	if state.Invocations == nil {
		state.Invocations = make(map[string]schedulepkg.InvocationState)
	}
}
