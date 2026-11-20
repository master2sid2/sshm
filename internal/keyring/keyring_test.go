package keyring

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

func TestDescriptionIsStablePerPathAndDistinctAcrossPaths(t *testing.T) {
	a := description("/tmp/a/vault.enc")
	if a != description("/tmp/a/vault.enc") {
		t.Fatal("description must be stable for the same path")
	}
	if a == description("/tmp/b/vault.enc") {
		t.Fatal("different vaults must not share a cache entry")
	}
}

func TestSaveLoadDeleteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	key := bytes.Repeat([]byte{7}, 32)

	if err := Save(path, key); err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Skip("no session keyring on this platform")
		}
		t.Skipf("session keyring unavailable here (sandbox/seccomp?): %v", err)
	}
	t.Cleanup(func() { _ = Delete(path) })

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("round-trip mismatch: got %x want %x", got, key)
	}

	if err := Delete(path); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := Load(path); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := Delete(path); err != nil {
		t.Fatalf("deleting something absent must not error, got %v", err)
	}
}

func TestSaveReplacesExistingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	if err := Save(path, []byte("old-key-old-key-old-key-old-key!")); err != nil {
		t.Skipf("session keyring unavailable: %v", err)
	}
	t.Cleanup(func() { _ = Delete(path) })
	newKey := []byte("new-key-new-key-new-key-new-key!")
	if err := Save(path, newKey); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil || !bytes.Equal(got, newKey) {
		t.Fatalf("expected the replaced key, got %q err=%v", got, err)
	}
}
