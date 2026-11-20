package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/master2sid2/sshm/cmd"
)

func main() {
	// SIGINT is deliberately not here: ctrl+c is a key in the TUI and in the
	// remote shell, cancels a password prompt, and cancels a connection
	// attempt in progress (see ssh.Connect) without quitting sshm.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if err := cmd.Execute(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
