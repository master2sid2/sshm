//go:build linux

package keyring

import (
	"errors"

	"golang.org/x/sys/unix"
)

const (
	keyType = "user"
	// keyPosAll grants every permission to the key's possessor and none to
	// user/group/other: only processes holding the session keyring can use it.
	keyPosAll = 0x3f000000
)

// Save stores key for vaultPath in the session keyring, replacing any
// existing entry.
func Save(vaultPath string, key []byte) error {
	id, err := unix.AddKey(keyType, description(vaultPath), key, unix.KEY_SPEC_SESSION_KEYRING)
	if err != nil {
		return err
	}
	return unix.KeyctlSetperm(id, keyPosAll)
}

// Load returns the cached key for vaultPath, or ErrNotFound.
func Load(vaultPath string) ([]byte, error) {
	id, err := find(vaultPath)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 64)
	n, err := unix.KeyctlBuffer(unix.KEYCTL_READ, id, buf, 0)
	if err != nil {
		if isGone(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if n > len(buf) {
		buf = make([]byte, n)
		if n, err = unix.KeyctlBuffer(unix.KEYCTL_READ, id, buf, 0); err != nil {
			return nil, err
		}
	}
	return buf[:n], nil
}

// Delete forgets the cached key for vaultPath. Nothing cached is not an error.
func Delete(vaultPath string) error {
	id, err := find(vaultPath)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = unix.KeyctlInt(unix.KEYCTL_REVOKE, id, 0, 0, 0)
	if isGone(err) {
		return nil
	}
	return err
}

func find(vaultPath string) (int, error) {
	id, err := unix.KeyctlSearch(unix.KEY_SPEC_SESSION_KEYRING, keyType, description(vaultPath), 0)
	if err != nil {
		if isGone(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return id, nil
}

func isGone(err error) bool {
	return errors.Is(err, unix.ENOKEY) || errors.Is(err, unix.EKEYREVOKED) || errors.Is(err, unix.EKEYEXPIRED)
}
