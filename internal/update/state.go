package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State is what survives a restart: when the last check ran, how it ended, and the newest release it found.
type State struct {
	LastCheckAt time.Time `json:"lastCheckAt"`
	LastError   string    `json:"lastError,omitempty"`
	Latest      *Found    `json:"latest,omitempty"`
}

// Found is a newer release with this platform's asset and its checksum file (public URLs only).
type Found struct {
	Version     string    `json:"version"`
	PageURL     string    `json:"pageUrl"`
	PublishedAt time.Time `json:"publishedAt"`
	Kind        Kind      `json:"kind"`
	Asset       Asset     `json:"asset"`
	Sums        Asset     `json:"sums"`
	Sig         *Asset    `json:"sig,omitempty"`
}

// StateStore persists State.
type StateStore interface {
	Load() (State, error)
	Save(State) error
}

// FileState keeps State in a small JSON file (update.json in the data folder).
type FileState struct{ Path string }

func (f FileState) Load() (State, error) {
	var s State
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, nil // a damaged file only costs one extra check
	}
	return s, nil
}

// Save writes atomically (temp file + rename in the same folder).
func (f FileState) Save(s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".update-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, f.Path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// MemState is an in-memory StateStore for tests and fake modes.
type MemState struct{ S State }

func (m *MemState) Load() (State, error) { return m.S, nil }
func (m *MemState) Save(s State) error   { m.S = s; return nil }
