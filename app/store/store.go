// Package store persists IR commands managed through the web UI in a small
// JSON file ({"<device>": {"<command>": "<base64 code>"}}). The main config
// stays read-only, so ${ENV} placeholders in it are never rewritten.
package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	ErrNotFound = errors.New("command not found")
	ErrExists   = errors.New("command already exists")
)

type data = map[string]map[string][]byte

// Store is safe for concurrent use.
type Store struct {
	path string // empty: memory only
	mu   sync.RWMutex
	data data

	devMu   sync.Mutex
	devices map[string]string // devices added in the UI: name -> host
}

// NewMemory returns a store that is never written to disk.
func NewMemory() *Store { return &Store{data: data{}, devices: map[string]string{}} }

// Open loads the file at path; a missing file yields an empty store that
// creates the file on the first change.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: data{}, devices: map[string]string{}}
	if err := s.loadDevices(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var enc map[string]map[string]string
	if err := json.Unmarshal(raw, &enc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for dev, cmds := range enc {
		s.data[dev] = map[string][]byte{}
		for name, b64 := range cmds {
			code, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return nil, fmt.Errorf("%s: %s/%s: %w", path, dev, name, err)
			}
			s.data[dev][name] = code
		}
	}
	return s, nil
}

// Path returns the backing file ("" for a memory store).
func (s *Store) Path() string { return s.path }

func (s *Store) Get(device, name string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[device][name]
	return append([]byte(nil), c...), ok
}

// Names returns the sorted command names of a device.
func (s *Store) Names(device string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.data[device]))
	for n := range s.data[device] {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Set creates or overwrites a command.
func (s *Store) Set(device, name string, code []byte) error {
	return s.mutate(func(d data) error {
		if d[device] == nil {
			d[device] = map[string][]byte{}
		}
		d[device][name] = append([]byte(nil), code...)
		return nil
	})
}

func (s *Store) Delete(device, name string) error {
	return s.mutate(func(d data) error {
		if _, ok := d[device][name]; !ok {
			return ErrNotFound
		}
		delete(d[device], name)
		return nil
	})
}

// Rename moves a command; it fails if the target name is taken.
func (s *Store) Rename(device, from, to string) error {
	return s.mutate(func(d data) error {
		code, ok := d[device][from]
		if !ok {
			return ErrNotFound
		}
		if _, taken := d[device][to]; taken {
			return ErrExists
		}
		delete(d[device], from)
		d[device][to] = code
		return nil
	})
}

// mutate applies fn to a copy, persists it and only then swaps it in, so a
// failed write leaves the in-memory state unchanged.
func (s *Store) mutate(fn func(data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(data, len(s.data))
	for dev, cmds := range s.data {
		next[dev] = make(map[string][]byte, len(cmds))
		for n, c := range cmds {
			next[dev][n] = c
		}
	}
	if err := fn(next); err != nil {
		return err
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *Store) save(d data) error {
	if s.path == "" {
		return nil
	}
	enc := make(map[string]map[string]string, len(d))
	for dev, cmds := range d {
		if len(cmds) == 0 {
			continue
		}
		enc[dev] = make(map[string]string, len(cmds))
		for n, c := range cmds {
			enc[dev][n] = base64.StdEncoding.EncodeToString(c)
		}
	}
	return writeJSONAtomic(s.path, enc)
}

// writeJSONAtomic writes v to path through a temp file + rename (mode 0600).
// An empty path means memory only.
func writeJSONAtomic(path string, v any) error {
	if path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".store-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
