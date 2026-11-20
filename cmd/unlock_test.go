package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin runs f with os.Stdin reading the given lines.
func withStdin(t *testing.T, input string, f func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	f()
}

func TestFirstRunAsksTwiceAndCreatesNothingOnMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")

	withStdin(t, "one\ntwo\n", func() {
		if _, err := openVault(path, false); err == nil || !strings.Contains(err.Error(), "do not match") {
			t.Fatalf("err = %v, want a mismatch error", err)
		}
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("no vault may be created when the passwords differ")
	}

	withStdin(t, "\n", func() {
		if _, err := openVault(path, false); err == nil {
			t.Fatal("an empty master password must be refused for a new vault")
		}
	})

	withStdin(t, "good\ngood\n", func() {
		if _, err := openVault(path, false); err != nil {
			t.Fatalf("create: %v", err)
		}
	})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the new vault must be written right away: %v", err)
	}

	// Existing vault: asked once, wrong password fails.
	withStdin(t, "bad\n", func() {
		if _, err := openVault(path, false); err == nil {
			t.Fatal("wrong password must fail")
		}
	})
	withStdin(t, "good\n", func() {
		if _, err := openVault(path, false); err != nil {
			t.Fatalf("reopen: %v", err)
		}
	})
}
