package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/muesli/cancelreader"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/master2sid2/sshm/internal/config"
	"github.com/master2sid2/sshm/internal/termutil"
)

const (
	// dialTimeout bounds the TCP connect and the SSH handshake. Without it an
	// unreachable host blocked for the kernel's TCP timeout (about two
	// minutes) on a blank screen.
	dialTimeout = 15 * time.Second

	// keepaliveInterval is how often the server is pinged; after
	// keepaliveMaxMissed unanswered pings the connection is closed, so a dead
	// network ends the session instead of freezing it.
	keepaliveInterval  = 30 * time.Second
	keepaliveMaxMissed = 3
)

// ErrCancelled is returned when the user aborts the connection with ctrl+c.
var ErrCancelled = errors.New("connection cancelled")

// Result reports what went wrong around an otherwise working session.
type Result struct {
	// TunnelErrors lists the tunnels that could not be set up (port in use,
	// forwarding refused by the server, ...). The session runs without them.
	TunnelErrors []error
}

// Connect establishes an SSH connection according to the profile, starts any
// configured tunnels, then runs an interactive PTY session. It blocks until the
// session ends. Tunnels are cancelled when the session returns.
//
// It must be called with the terminal released by the TUI: it may prompt for
// a key passphrase or password that the profile does not store.
func Connect(ctx context.Context, p config.Profile) (Result, error) {
	// Until the shell starts, the terminal is in cooked mode and ctrl+c is a
	// SIGINT: let it cancel this attempt (and only this attempt).
	dialCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	client, cleanup, err := dial(dialCtx, p)
	cancelled := dialCtx.Err() != nil && ctx.Err() == nil
	stop()
	if err != nil {
		if cancelled || errors.Is(err, termutil.ErrCancelled) {
			return Result{}, ErrCancelled
		}
		return Result{}, err
	}
	defer cleanup()
	defer client.Close()

	// sessionCtx is cancelled once the interactive session ends, which causes
	// all tunnel listeners to close and their goroutines to exit cleanly.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var res Result
	for _, t := range p.Tunnels {
		if err := startTunnel(sessionCtx, client, t); err != nil {
			res.TunnelErrors = append(res.TunnelErrors, err)
			fmt.Fprintf(os.Stderr, "sshm: tunnel not started: %v\n", err)
		}
	}
	go keepalive(sessionCtx, client)

	return res, runSession(client)
}

// dial connects and authenticates, honouring ctx and dialTimeout.
func dial(ctx context.Context, p config.Profile) (*gossh.Client, func(), error) {
	authMethods, cleanup, err := buildAuthMethods(p)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: %w", err)
	}

	keys, err := defaultHostKeys()
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	// The handshake has no context of its own: bound it with a deadline and
	// close the connection if ctx is cancelled meanwhile. The deadline is
	// lifted while the user reads a new host's fingerprint.
	_ = conn.SetDeadline(time.Now().Add(dialTimeout))
	cfg := &gossh.ClientConfig{
		User: p.User,
		Auth: authMethods,
		HostKeyCallback: keys.callback(
			func() { _ = conn.SetDeadline(time.Time{}) },
			func() { _ = conn.SetDeadline(time.Now().Add(dialTimeout)) },
		),
		HostKeyAlgorithms: keys.algorithms(addr, conn.RemoteAddr()),
		Timeout:           dialTimeout,
	}
	stopClose := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := gossh.NewClientConn(conn, addr, cfg)
	stopClose()
	if err != nil {
		conn.Close()
		cleanup()
		if ctx.Err() != nil || errors.Is(err, termutil.ErrCancelled) {
			return nil, nil, ErrCancelled
		}
		if hk, ok := errors.AsType[*HostKeyError](err); ok {
			return nil, nil, hk
		}
		if errors.Is(err, ErrHostKeyRejected) {
			return nil, nil, ErrHostKeyRejected
		}
		return nil, nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return gossh.NewClient(c, chans, reqs), cleanup, nil
}

// buildAuthMethods constructs auth methods based on profile settings. The
// returned cleanup closes the ssh-agent connection, if one was opened.
func buildAuthMethods(p config.Profile) ([]gossh.AuthMethod, func(), error) {
	var methods []gossh.AuthMethod
	var cleanups []func()
	cleanup := func() {
		for _, c := range cleanups {
			c()
		}
	}

	switch p.AuthType {
	case config.AuthPassword:
		password := p.Password
		if password == "" {
			pw, err := termutil.ReadSecret(fmt.Sprintf("Password for %s@%s: ", p.User, p.Host))
			if err != nil {
				return nil, nil, err
			}
			password = pw
		}
		// Many servers offer only keyboard-interactive, not "password".
		methods = append(methods,
			gossh.Password(password),
			gossh.KeyboardInteractive(keyboardInteractive(password)))

	case config.AuthKey:
		signer, err := loadKey(p)
		if err != nil {
			return nil, nil, err
		}
		methods = append(methods, gossh.PublicKeys(signer))

		// Also try ssh-agent as fallback.
		if m, c, err := agentAuthMethod(); err == nil {
			methods = append(methods, m)
			cleanups = append(cleanups, c)
		}

	case config.AuthAgent:
		m, c, err := agentAuthMethod()
		if err != nil {
			return nil, nil, fmt.Errorf("ssh-agent: %w", err)
		}
		methods = append(methods, m)
		cleanups = append(cleanups, c)

	default:
		return nil, nil, fmt.Errorf("unknown auth type: %s", p.AuthType)
	}

	return methods, cleanup, nil
}

// loadKey reads and parses the profile's private key. An encrypted key with
// no stored passphrase (typical after importing ~/.ssh/config) is unlocked by
// asking for the passphrase on the terminal.
func loadKey(p config.Profile) (gossh.Signer, error) {
	if p.KeyPath == "" {
		return nil, fmt.Errorf("key auth: key path is empty")
	}
	path := ExpandPath(p.KeyPath)
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", p.KeyPath, err)
	}

	if p.KeyPassword != "" {
		signer, err := gossh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(p.KeyPassword))
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", p.KeyPath, err)
		}
		return signer, nil
	}

	signer, err := gossh.ParsePrivateKey(keyBytes)
	if _, missing := errors.AsType[*gossh.PassphraseMissingError](err); missing {
		pass, perr := termutil.ReadSecret(fmt.Sprintf("Passphrase for %s: ", p.KeyPath))
		if perr != nil {
			return nil, perr
		}
		signer, err = gossh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(pass))
	}
	if err != nil {
		return nil, fmt.Errorf("parse key %s: %w", p.KeyPath, err)
	}
	return signer, nil
}

// keyboardInteractive answers the server's first single hidden question (the
// usual "Password:") with the stored password, and asks the user for
// everything else — OTP codes, a second factor, or a retry.
func keyboardInteractive(password string) gossh.KeyboardInteractiveChallenge {
	used := false
	return func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		if len(questions) == 0 {
			return nil, nil
		}
		if !used && len(questions) == 1 && !echos[0] {
			used = true
			return []string{password}, nil
		}
		if name != "" {
			fmt.Fprintln(os.Stderr, name)
		}
		if instruction != "" {
			fmt.Fprintln(os.Stderr, instruction)
		}
		answers := make([]string, len(questions))
		for i, q := range questions {
			read := termutil.ReadSecret
			if echos[i] {
				read = termutil.ReadLine
			}
			a, err := read(q)
			if err != nil {
				return nil, err
			}
			answers[i] = a
		}
		return answers, nil
	}
}

// ExpandPath expands a leading ~ or ~/ to the current user's home directory.
func ExpandPath(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
}

// keepalive pings the server until ctx is done and closes the client when
// it stops answering.
func keepalive(ctx context.Context, client *gossh.Client) {
	t := time.NewTicker(keepaliveInterval)
	defer t.Stop()
	missed := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		reply := make(chan error, 1)
		go func() {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		select {
		case <-ctx.Done():
			return
		case err := <-reply:
			if err != nil {
				client.Close()
				return
			}
			missed = 0
		case <-time.After(keepaliveInterval):
			if missed++; missed >= keepaliveMaxMissed {
				client.Close()
				return
			}
		}
	}
}

// runSession opens a PTY session and wires up stdin/stdout/stderr.
func runSession(client *gossh.Client) error {
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	w, h, err := term.GetSize(fd)
	if err != nil {
		w, h = 80, 24
	}

	modes := gossh.TerminalModes{
		gossh.ECHO:          1,
		gossh.ICRNL:         1,
		gossh.IXON:          0,
		gossh.IXOFF:         0,
		gossh.TTY_OP_ISPEED: 115200,
		gossh.TTY_OP_OSPEED: 115200,
	}
	termType := os.Getenv("TERM")
	if termType == "" {
		termType = "xterm-256color"
	}
	if err := sess.RequestPty(termType, h, w, modes); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}

	// Given os.Stdin, the library would copy it in a goroutine that outlives
	// the session: left blocked in Read, it swallows the first key typed in
	// the list afterwards. Copy through a cancelable reader instead, and
	// close it only once the copy has stopped (closing earlier lets a late
	// Read wait on a reused descriptor and still take a key).
	in, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		return fmt.Errorf("stdin: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		in.Close()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(stdin, in)
	}()
	defer func() {
		in.Cancel()
		select {
		case <-copied:
			in.Close()
		case <-time.After(time.Second): // should not happen; leak rather than race
		}
	}()

	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	if err := sess.Shell(); err != nil {
		return fmt.Errorf("start shell: %w", err)
	}

	// Handle terminal resize (SIGWINCH).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	stopResize := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigCh:
				if w, h, err := term.GetSize(fd); err == nil {
					_ = sess.WindowChange(h, w)
				}
			case <-stopResize:
				return
			}
		}
	}()
	defer func() {
		close(stopResize)
		signal.Stop(sigCh)
	}()

	err = sess.Wait()
	// A remote shell that exits non-zero (the last command failed, or the
	// user typed "exit 1") is a normal end of session, not a failure.
	if _, ok := errors.AsType[*gossh.ExitError](err); ok {
		return nil
	}
	if _, ok := errors.AsType[*gossh.ExitMissingError](err); ok {
		return errors.New("connection lost")
	}
	return err
}
