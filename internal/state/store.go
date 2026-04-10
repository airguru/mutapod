package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// stateDir returns the directory where state files are kept.
func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state: get home dir: %w", err)
	}
	return filepath.Join(home, ".mutapod", "state"), nil
}

func statePath(name string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// Load reads state for the given workspace name.
// If no state file exists, a fresh State is returned (no error).
func Load(name string) (*State, error) {
	path, err := statePath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{
				SchemaVersion: SchemaVersion,
				Name:          name,
			}, nil
		}
		return nil, fmt.Errorf("state: read %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("state: parse %s: %w", path, err)
	}
	return &s, nil
}

// Save writes state atomically under a file lock.
func Save(s *State) error {
	path, err := statePath(s.Name)
	if err != nil {
		return err
	}
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("state: mkdir: %w", err)
	}

	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("state: acquire lock: %w", err)
	}
	defer lock.Unlock()

	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}

	// Write to temp file then rename for atomicity
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("state: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("state: rename: %w", err)
	}
	return nil
}

// Delete removes the state file for the given workspace name.
func Delete(name string) error {
	path, err := statePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("state: delete: %w", err)
	}
	// Clean up lock file too
	_ = os.Remove(path + ".lock")
	return nil
}
