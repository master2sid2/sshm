package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/master2sid2/sshm/internal/keyring"
)

// runPasswd changes the vault's master password in place: the current
// password to unlock it, the new one (typed twice) to re-encrypt it.
// Plaintext never touches disk. Folder passwords are separate and unchanged.
func runPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	vault := vaultFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	vaultPath, err := vault()
	if err != nil {
		return err
	}
	if _, err := os.Stat(vaultPath); err != nil {
		return fmt.Errorf("vault not found at %s: %w", vaultPath, err)
	}

	// Changing the master password re-proves the current one: never
	// satisfied from the session cache.
	store, err := openVault(vaultPath, false)
	if err != nil {
		return err
	}

	newPass, err := readPassword("New master password: ")
	if err != nil {
		return err
	}
	if newPass == "" {
		return errors.New("the master password cannot be empty")
	}
	again, err := readPassword("Confirm new password: ")
	if err != nil {
		return err
	}
	if newPass != again {
		return errors.New("passwords do not match; nothing was changed")
	}

	if err := store.Rekey([]byte(newPass)); err != nil {
		return fmt.Errorf("rekey vault: %w", err)
	}
	_ = keyring.Delete(vaultPath) // the cached key belongs to the old password

	fmt.Printf("Password changed for %s\n", vaultPath)
	return nil
}
