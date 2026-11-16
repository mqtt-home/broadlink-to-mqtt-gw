package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistenceRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "commands.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("rm", "power", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("rm", "mute", []byte{9}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("rm", "mute", "silence"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("rm", "power"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", st, err)
	}

	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Names("rm"); len(got) != 1 || got[0] != "silence" {
		t.Errorf("names = %v", got)
	}
	if c, ok := s2.Get("rm", "silence"); !ok || len(c) != 1 || c[0] != 9 {
		t.Errorf("code = %v %v", c, ok)
	}
}

func TestErrors(t *testing.T) {
	s := NewMemory()
	_ = s.Set("rm", "a", []byte{1})
	_ = s.Set("rm", "b", []byte{2})
	if err := s.Delete("rm", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	if err := s.Rename("rm", "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename: %v", err)
	}
	if err := s.Rename("rm", "a", "b"); !errors.Is(err, ErrExists) {
		t.Errorf("rename onto existing: %v", err)
	}
	if c, _ := s.Get("rm", "a"); len(c) != 1 {
		t.Error("failed rename changed state")
	}
}

func TestFailedWriteKeepsState(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "commands.json")
	s, _ := Open(p)
	_ = s.Set("rm", "a", []byte{1})
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod")
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	if err := s.Set("rm", "b", []byte{2}); err == nil {
		t.Fatal("expected write error")
	}
	if _, ok := s.Get("rm", "b"); ok {
		t.Error("memory changed although the write failed")
	}
}

func TestCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	_ = os.WriteFile(p, []byte(`{"rm":{"a":"!!!"}}`), 0o600)
	if _, err := Open(p); err == nil {
		t.Error("expected base64 error")
	}
	_ = os.WriteFile(p, []byte(`nope`), 0o600)
	if _, err := Open(p); err == nil {
		t.Error("expected JSON error")
	}
}
