package tui

import (
	"context"

	"github.com/master2sid2/sshm/internal/config"
	sshclient "github.com/master2sid2/sshm/internal/ssh"
)

// connectSSH is called after bubbletea has exited and released the terminal.
func connectSSH(ctx context.Context, p config.Profile) (sshclient.Result, error) {
	return sshclient.Connect(ctx, p)
}
