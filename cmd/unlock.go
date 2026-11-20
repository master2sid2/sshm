package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"

	"github.com/master2sid2/sshm/internal/config"
	"github.com/master2sid2/sshm/internal/keyring"
	"github.com/master2sid2/sshm/internal/termutil"
)

// openVault unlocks the vault at vaultPath:
//
//  1. (only if useCache) with the key cached in this login session's kernel
//     keyring by an earlier unlock — a stale one (vault re-keyed) just fails
//     authentication and is discarded;
//  2. otherwise with a password prompt, after which the derived key is
//     cached for the rest of the session so later runs don't ask again.
//
// The cache holds the derived key, never the password itself, and lives only
// in kernel memory: it disappears at logout or with `sshm lock`. useCache is
// false for actions that must re-prove knowledge of the password.
//
// A vault that does not exist yet is created: its password is asked twice,
// since a typo would lock the new vault for good.
func openVault(vaultPath string, useCache bool) (*config.Store, error) {
	if useCache {
		if key, err := keyring.Load(vaultPath); err == nil {
			if store, err := config.OpenWithKey(vaultPath, key); err == nil {
				return store, nil
			}
			_ = keyring.Delete(vaultPath) // stale: drop it and ask normally
		}
	}

	_, statErr := os.Stat(vaultPath)
	creating := errors.Is(statErr, fs.ErrNotExist)
	prompt := "Master password: "
	if creating {
		fmt.Fprintf(os.Stderr, "No vault at %s yet: choose a master password for the new one.\n", vaultPath)
		prompt = "New master password: "
	}
	pw, err := readPassword(prompt)
	if err != nil {
		return nil, err
	}
	if creating {
		if len(pw) == 0 {
			return nil, errors.New("the master password cannot be empty; no vault was created")
		}
		again, err := readPassword("Confirm master password: ")
		if err != nil {
			return nil, err
		}
		if pw != again {
			return nil, errors.New("passwords do not match; no vault was created")
		}
	}

	store, err := config.Open(vaultPath, []byte(pw))
	if err != nil {
		return nil, fmt.Errorf("open vault: %w", err)
	}
	if creating {
		// Write the empty vault now: the confirmed password must not be
		// lost if the user quits before adding anything.
		if err := store.Save(); err != nil {
			return nil, fmt.Errorf("create vault: %w", err)
		}
		offerFirstRunImport(store)
	}

	if useCache {
		if err := keyring.Save(vaultPath, store.SessionKey()); err == nil {
			fmt.Fprintln(os.Stderr, "Unlocked for this session — `sshm lock` forgets it earlier.")
		}
	}
	return store, nil
}

// readPassword prompts on stderr and reads a line without echoing it.
// ctrl+c at the prompt aborts the command.
func readPassword(prompt string) (string, error) {
	pw, err := termutil.ReadSecret(prompt)
	if errors.Is(err, termutil.ErrCancelled) {
		return "", errors.New("cancelled")
	}
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return pw, nil
}

// runLock forgets the key cached for the vault in this login session, so the
// next command asks for the master password again.
func runLock(args []string) error {
	fs := flag.NewFlagSet("lock", flag.ExitOnError)
	vault := vaultFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	vaultPath, err := vault()
	if err != nil {
		return err
	}

	err = keyring.Delete(vaultPath)
	switch {
	case errors.Is(err, keyring.ErrUnsupported):
		fmt.Println("Nothing to forget: this platform doesn't cache the unlock key.")
		return nil
	case err != nil:
		return fmt.Errorf("forget cached key: %w", err)
	}
	fmt.Println("Locked: the cached key for this session was forgotten.")
	return nil
}
