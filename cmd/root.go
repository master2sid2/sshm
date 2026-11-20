// Package cmd is the command-line entry point: subcommands, unlocking the
// vault, and handing over to the TUI.
package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/master2sid2/sshm/internal/config"
	"github.com/master2sid2/sshm/internal/tui"
	"github.com/master2sid2/sshm/internal/version"
)

// Execute is the CLI entry point. It dispatches subcommands, or unlocks the
// vault and hands control to the TUI.
func Execute(ctx context.Context) error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "lock":
			return runLock(os.Args[2:])
		case "passwd":
			return runPasswd(os.Args[2:])
		case "import-ssh":
			return runImportSSH(os.Args[2:])
		case "export-ssh":
			return runExportSSH(os.Args[2:])
		case "--version", "-v", "version":
			fmt.Println("sshm", version.Version)
			return nil
		case "--help", "-h", "help":
			printUsage()
			return nil
		default:
			printUsage()
			return fmt.Errorf("unknown command %q", os.Args[1])
		}
	}
	return runTUI(ctx)
}

func runTUI(ctx context.Context) error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return fmt.Errorf("config path: %w", err)
	}
	store, err := openVault(cfgPath, true)
	if err != nil {
		return err
	}
	return tui.Run(ctx, store)
}

func printUsage() {
	fmt.Fprint(os.Stderr, `sshm — encrypted SSH profile manager

Usage:
  sshm                      Launch the interactive TUI
  sshm import-ssh [flags]   Import hosts from ~/.ssh/config (and its Includes)
  sshm export-ssh [flags]   Write the vault's hosts to ~/.ssh/config.d/sshm.conf
  sshm lock                 Forget the unlock cached for this login session
  sshm passwd [flags]       Change the vault's master password in place
  sshm --version            Print the version
  sshm --help               Show this message

Flags:
  -vault <path>    vault file (default: ~/.config/sshm/vault.enc)
  import-ssh:
    -config <path> ssh config to read (default: ~/.ssh/config)
    -n             dry run: show what would be imported
  export-ssh:
    -o <path>|-    output file, - for stdout (default: ~/.ssh/config.d/sshm.conf)
    -y             add "Include config.d/sshm.conf" to ~/.ssh/config without asking
    -locked        also export password-protected folders (asks their passwords)

Import and export never carry passwords or key passphrases: imported hosts
use their IdentityFile or the ssh-agent; exported ones get IdentityFile, and
ssh asks for passwords itself. On first run, sshm offers to import
~/.ssh/config when it has hosts.

Unlocking: the master password is asked once per login session (Linux); the
derived key is then held in the kernel session keyring, so later runs don't
ask. Logging out or "sshm lock" forgets it. "passwd" always asks.
`)
}

// vaultFlag registers -vault on fs and returns a function resolving it to
// the default path when unset.
func vaultFlag(fs *flag.FlagSet) func() (string, error) {
	v := fs.String("vault", "", "vault file (default: ~/.config/sshm/vault.enc)")
	return func() (string, error) {
		if *v != "" {
			return *v, nil
		}
		p, err := config.DefaultPath()
		if err != nil {
			return "", fmt.Errorf("resolve vault path: %w", err)
		}
		return p, nil
	}
}
