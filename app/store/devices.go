package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Devices added through the UI live in devices.json next to the commands
// file ({"<name>": "<host>"}); the config file stays read-only.

func (s *Store) devicesPath() string {
	if s.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.path), "devices.json")
}

func (s *Store) loadDevices() error {
	p := s.devicesPath()
	if p == "" {
		return nil
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	if err := json.Unmarshal(raw, &s.devices); err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}
	return nil
}

// Devices returns the UI-added devices (name -> host).
func (s *Store) Devices() map[string]string {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	out := make(map[string]string, len(s.devices))
	for k, v := range s.devices {
		out[k] = v
	}
	return out
}

// DeviceNames returns the UI-added device names in alphabetical order.
func (s *Store) DeviceNames() []string {
	m := s.Devices()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SetDevice adds or updates a UI-added device.
func (s *Store) SetDevice(name, host string) error {
	return s.mutateDevices(func(m map[string]string) error {
		m[name] = host
		return nil
	})
}

// DeleteDevice removes a UI-added device and all of its commands.
func (s *Store) DeleteDevice(name string) error {
	if err := s.mutateDevices(func(m map[string]string) error {
		if _, ok := m[name]; !ok {
			return ErrNotFound
		}
		delete(m, name)
		return nil
	}); err != nil {
		return err
	}
	return s.mutate(func(d data) error {
		delete(d, name)
		return nil
	})
}

func (s *Store) mutateDevices(fn func(map[string]string) error) error {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	next := make(map[string]string, len(s.devices)+1)
	for k, v := range s.devices {
		next[k] = v
	}
	if err := fn(next); err != nil {
		return err
	}
	if err := writeJSONAtomic(s.devicesPath(), next); err != nil {
		return err
	}
	s.devices = next
	return nil
}
