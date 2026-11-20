package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/master2sid2/sshm/internal/config"
)

// testServer is a minimal in-process SSH server: password or
// keyboard-interactive auth, and direct-tcpip channels (ssh -L).
type testServer struct {
	addr     string
	password string
	keys     []gossh.Signer // host keys
}

func newEd25519Signer(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// trustAll points the known_hosts database at a temporary file and accepts
// every new host key, for tests that are not about host keys.
func trustAll(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "known_hosts")
	old := defaultHostKeys
	defaultHostKeys = func() (*hostKeys, error) {
		h, err := loadHostKeys(file)
		if err == nil {
			h.confirm = func(string) (bool, error) { return true, nil }
		}
		return h, err
	}
	t.Cleanup(func() { defaultHostKeys = old })
	return file
}

func startTestServer(t *testing.T, network, listenAddr string, kbdOnly bool, hostKeys ...gossh.Signer) *testServer {
	t.Helper()
	if len(hostKeys) == 0 {
		hostKeys = []gossh.Signer{newEd25519Signer(t)}
	}
	srv := &testServer{password: "s3cret", keys: hostKeys}
	cfg := &gossh.ServerConfig{}
	if kbdOnly {
		cfg.KeyboardInteractiveCallback = func(_ gossh.ConnMetadata, ask gossh.KeyboardInteractiveChallenge) (*gossh.Permissions, error) {
			ans, err := ask("", "", []string{"Password: "}, []bool{false})
			if err == nil && len(ans) == 1 && ans[0] == srv.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		}
	} else {
		cfg.PasswordCallback = func(_ gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			if string(pw) == srv.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		}
	}
	for _, k := range hostKeys {
		cfg.AddHostKey(k)
	}

	ln, err := net.Listen(network, listenAddr)
	if err != nil {
		t.Skipf("cannot listen on %s %s: %v", network, listenAddr, err)
	}
	t.Cleanup(func() { ln.Close() })
	srv.addr = ln.Addr().String()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handle(c, cfg)
		}
	}()
	return srv
}

func (s *testServer) handle(c net.Conn, cfg *gossh.ServerConfig) {
	_, chans, reqs, err := gossh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go func() {
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(r.Type == "keepalive@openssh.com", nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			_ = nc.Reject(gossh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		// payload: host string, port uint32, origin host string, origin port uint32
		var req struct {
			Host       string
			Port       uint32
			OriginHost string
			OriginPort uint32
		}
		if err := gossh.Unmarshal(nc.ExtraData(), &req); err != nil {
			_ = nc.Reject(gossh.ConnectionFailed, "bad payload")
			continue
		}
		target, err := net.Dial("tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
		if err != nil {
			_ = nc.Reject(gossh.ConnectionFailed, err.Error())
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			target.Close()
			continue
		}
		go gossh.DiscardRequests(creqs)
		go func() {
			defer ch.Close()
			defer target.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(ch, target); done <- struct{}{} }()
			go func() { _, _ = io.Copy(target, ch); done <- struct{}{} }()
			<-done
		}()
	}
}

func (s *testServer) profile(t *testing.T) config.Profile {
	t.Helper()
	host, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatal(err)
	}
	p := config.NewProfile()
	p.Host = host
	p.Port, _ = strconv.Atoi(port)
	p.User = "tester"
	p.AuthType = config.AuthPassword
	p.Password = s.password
	return p
}

func TestDialPasswordAndKeyboardInteractive(t *testing.T) {
	trustAll(t)
	for _, kbd := range []bool{false, true} {
		srv := startTestServer(t, "tcp", "127.0.0.1:0", kbd)
		client, cleanup, err := dial(t.Context(), srv.profile(t))
		if err != nil {
			t.Fatalf("kbd=%v: dial: %v", kbd, err)
		}
		cleanup()
		client.Close()
	}
}

func TestDialIPv6(t *testing.T) {
	trustAll(t)
	srv := startTestServer(t, "tcp6", "[::1]:0", false)
	p := srv.profile(t)
	if p.Host != "::1" {
		t.Fatalf("host = %q", p.Host)
	}
	client, cleanup, err := dial(t.Context(), p)
	if err != nil {
		t.Fatalf("dial [::1]: %v", err)
	}
	cleanup()
	client.Close()
}

// A server that accepts TCP but never speaks SSH must not hang the dial
// forever, and cancelling the context must abort it at once.
func TestDialCancelDuringHandshake(t *testing.T) {
	trustAll(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold the connection open, say nothing
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p := config.NewProfile()
	p.Host, p.User, p.AuthType, p.Password = host, "x", config.AuthPassword, "x"
	p.Port, _ = strconv.Atoi(port)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = dial(ctx, p)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("dial took %v after cancellation", d)
	}
}

func TestLocalForwardAndBusyPort(t *testing.T) {
	trustAll(t)
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false)
	client, cleanup, err := dial(t.Context(), srv.profile(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer client.Close()

	// The service behind the tunnel: echoes one length-prefixed message.
	svc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	go func() {
		c, err := svc.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()
	_, svcPort, _ := net.SplitHostPort(svc.Addr().String())

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, localPort, _ := net.SplitHostPort(free.Addr().String())
	free.Close()

	tun := config.Tunnel{Direction: config.TunnelLocal, LocalHost: "127.0.0.1", RemoteHost: "127.0.0.1"}
	tun.LocalPort, _ = strconv.Atoi(localPort)
	tun.RemotePort, _ = strconv.Atoi(svcPort)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := startTunnel(ctx, client, tun); err != nil {
		t.Fatalf("start tunnel: %v", err)
	}

	// Binding the same local port again must fail synchronously.
	if err := startTunnel(ctx, client, tun); err == nil {
		t.Fatal("a second tunnel on the same port must report an error")
	}

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", localPort))
	if err != nil {
		t.Fatalf("connect to tunnel: %v", err)
	}
	defer c.Close()
	msg := []byte("hello through the tunnel")
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(msg)))
	if _, err := c.Write(append(hdr[:], msg...)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, 4+len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got[4:]) != string(msg) {
		t.Fatalf("echo = %q", got[4:])
	}

	// Cancelling the session context closes the listener.
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c2, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", localPort))
		if err != nil {
			break
		}
		c2.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after cancel")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	cases := map[string]string{
		"~":                 home,
		"~/.ssh/id_ed25519": filepath.Join(home, ".ssh/id_ed25519"),
		"/abs/key":          "/abs/key",
		"rel/key":           "rel/key",
		"~other/key":        "~other/key",
	}
	for in, want := range cases {
		if got := ExpandPath(in); got != want {
			t.Errorf("ExpandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeyboardInteractiveAnswersStoredPasswordOnce(t *testing.T) {
	ch := keyboardInteractive("pw")
	ans, err := ch("", "", []string{"Password: "}, []bool{false})
	if err != nil || len(ans) != 1 || ans[0] != "pw" {
		t.Fatalf("first answer = %v, %v", ans, err)
	}
	if ans, err := ch("", "", nil, nil); err != nil || ans != nil {
		t.Fatalf("empty round = %v, %v", ans, err)
	}
}
