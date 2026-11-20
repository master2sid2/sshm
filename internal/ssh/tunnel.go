package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	gossh "golang.org/x/crypto/ssh"

	"github.com/master2sid2/sshm/internal/config"
)

// startTunnel binds the tunnel's listener and serves it in the background
// until ctx is cancelled or the SSH client closes. Binding happens before it
// returns, so a port already in use or a forwarding the server refuses is
// reported to the caller instead of failing silently in a goroutine.
func startTunnel(ctx context.Context, client *gossh.Client, t config.Tunnel) error {
	local := net.JoinHostPort(t.LocalHost, strconv.Itoa(t.LocalPort))
	remote := net.JoinHostPort(t.RemoteHost, strconv.Itoa(t.RemotePort))

	var (
		ln   net.Listener
		err  error
		dial func(context.Context) (net.Conn, error)
	)
	switch t.Direction {
	case config.TunnelLocal:
		// ssh -L local:remote — listen here, connect from the server.
		ln, err = net.Listen("tcp", local)
		if err != nil {
			return fmt.Errorf("local forward %s → %s: %w", local, remote, err)
		}
		dial = func(context.Context) (net.Conn, error) { return client.Dial("tcp", remote) }
	case config.TunnelRemote:
		// ssh -R remote:local — the server listens, we connect locally.
		ln, err = client.Listen("tcp", remote)
		if err != nil {
			return fmt.Errorf("remote forward %s → %s: %w", remote, local, err)
		}
		dial = func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", local)
		}
	default:
		return fmt.Errorf("unknown tunnel direction: %s", t.Direction)
	}

	// Close the listener when ctx is cancelled so Accept unblocks.
	context.AfterFunc(ctx, func() { ln.Close() })
	go serve(ctx, ln, dial)
	return nil
}

// serve accepts connections on ln and pipes each one to a connection
// obtained from dial, until ln is closed.
func serve(ctx context.Context, ln net.Listener, dial func(context.Context) (net.Conn, error)) {
	for {
		in, err := ln.Accept()
		if err != nil {
			return // listener closed — session ended or context cancelled
		}
		go func() {
			defer in.Close()
			out, err := dial(ctx)
			if err != nil {
				return
			}
			defer out.Close()
			copyBidirectional(ctx, in, out)
		}()
	}
}

// copyBidirectional pipes data between two connections until either side
// closes or ctx is cancelled. Closing either connection unblocks both copies.
func copyBidirectional(ctx context.Context, a, b net.Conn) {
	stop := context.AfterFunc(ctx, func() {
		a.Close()
		b.Close()
	})
	defer stop()

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(a, b)
		a.Close() // unblock the other direction
	})
	wg.Go(func() {
		_, _ = io.Copy(b, a)
		b.Close()
	})
	wg.Wait()
}
