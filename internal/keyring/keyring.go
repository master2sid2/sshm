// Package keyring caches a vault's derived key for the length of a login
// session so the master password is typed once, not on every invocation.
//
// On Linux it uses the kernel session keyring: no daemon, no file, nothing
// in the environment. The key lives in kernel memory and disappears when the
// session ends, so logging out (or `sshm lock`) forgets it. Other
// platforms have no equivalent here; every call reports ErrUnsupported and
// callers fall back to prompting.
package keyring

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
)

var (
	// ErrUnsupported is returned on platforms without a session keyring.
	ErrUnsupported = errors.New("session keyring not supported on this platform")
	// ErrNotFound means nothing is cached for that vault in this session.
	ErrNotFound = errors.New("no cached key for this vault")
)

// description names a vault's cache entry. It's derived from the absolute
// vault path so different vaults never share (or overwrite) an entry, and so
// the path itself isn't exposed by listing the keyring.
func description(vaultPath string) string {
	abs, err := filepath.Abs(vaultPath)
	if err != nil {
		abs = vaultPath
	}
	sum := sha256.Sum256([]byte(abs))
	return "sshm:vault:" + hex.EncodeToString(sum[:8])
}
