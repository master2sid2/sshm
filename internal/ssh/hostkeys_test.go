package ssh

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/master2sid2/sshm/internal/termutil"
)

// useKnownHosts points dial at file and answers new-host prompts with
// answer, counting the prompts.
func useKnownHosts(t *testing.T, file string, answer func() (bool, error)) *int {
	t.Helper()
	asked := new(int)
	old := defaultHostKeys
	defaultHostKeys = func() (*hostKeys, error) {
		h, err := loadHostKeys(file)
		if err == nil {
			h.confirm = func(q string) (bool, error) {
				*asked++
				if !strings.Contains(q, "SHA256:") {
					t.Errorf("the prompt must show the fingerprint: %q", q)
				}
				return answer()
			}
		}
		return h, err
	}
	t.Cleanup(func() { defaultHostKeys = old })
	return asked
}

func knownLine(t *testing.T, srv *testServer, key gossh.PublicKey) string {
	t.Helper()
	return knownhosts.Line([]string{knownhosts.Normalize(srv.addr)}, key) + "\n"
}

func dialOnce(t *testing.T, srv *testServer) error {
	t.Helper()
	client, cleanup, err := dial(t.Context(), srv.profile(t))
	if err == nil {
		cleanup()
		client.Close()
	}
	return err
}

func TestUnknownHostAcceptedIsRememberedWithPort(t *testing.T) {
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false)
	file := filepath.Join(t.TempDir(), "ssh", "known_hosts") // dir does not exist yet
	asked := useKnownHosts(t, file, func() (bool, error) { return true, nil })

	if err := dialOnce(t, srv); err != nil {
		t.Fatalf("first connection: %v", err)
	}
	if *asked != 1 {
		t.Fatalf("asked %d times, want 1", *asked)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Non-standard port: stored as [host]:port, exactly like OpenSSH.
	if want := knownLine(t, srv, srv.keys[0].PublicKey()); string(data) != want {
		t.Errorf("known_hosts =\n%s\nwant\n%s", data, want)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", fi.Mode().Perm())
	}

	if err := dialOnce(t, srv); err != nil {
		t.Fatalf("second connection: %v", err)
	}
	if *asked != 1 {
		t.Fatal("a remembered host must not be asked about again")
	}
}

func TestUnknownHostRejectedOrCancelled(t *testing.T) {
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false)
	file := filepath.Join(t.TempDir(), "known_hosts")

	useKnownHosts(t, file, func() (bool, error) { return false, nil })
	if err := dialOnce(t, srv); !errors.Is(err, ErrHostKeyRejected) {
		t.Fatalf("err = %v, want ErrHostKeyRejected", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("nothing may be written for a rejected key")
	}

	useKnownHosts(t, file, func() (bool, error) { return false, termutil.ErrCancelled })
	if err := dialOnce(t, srv); !errors.Is(err, ErrCancelled) {
		t.Fatalf("ctrl+c at the prompt: err = %v, want ErrCancelled", err)
	}
}

func TestChangedHostKeyIsRefused(t *testing.T) {
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false)
	file := filepath.Join(t.TempDir(), "known_hosts")
	other := newEd25519Signer(t).PublicKey()
	before := knownLine(t, srv, other)
	if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := useKnownHosts(t, file, func() (bool, error) { return true, nil })

	err := dialOnce(t, srv)
	hk, ok := errors.AsType[*HostKeyError](err)
	if !ok {
		t.Fatalf("err = %v, want *HostKeyError", err)
	}
	if *asked != 0 {
		t.Error("a changed key must never be offered for acceptance")
	}
	if len(hk.Known) != 1 || !strings.Contains(hk.Details(), "REMOTE HOST IDENTIFICATION HAS CHANGED") ||
		!strings.Contains(hk.Details(), "ssh-keygen -R") {
		t.Errorf("details:\n%s", hk.Details())
	}
	if after, _ := os.ReadFile(file); string(after) != before {
		t.Error("known_hosts must not change")
	}
}

func TestRevokedHostKeyIsRefused(t *testing.T) {
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false)
	file := filepath.Join(t.TempDir(), "known_hosts")
	line := "@revoked * " + string(gossh.MarshalAuthorizedKey(srv.keys[0].PublicKey()))
	if err := os.WriteFile(file, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	useKnownHosts(t, file, func() (bool, error) { return true, nil })
	if hk, ok := errors.AsType[*HostKeyError](dialOnce(t, srv)); !ok || !hk.Revoked {
		t.Fatalf("want a revoked HostKeyError, got %v", hk)
	}
}

// A server with several host keys, of which known_hosts has only the
// Ed25519 one: sshm must ask for Ed25519 rather than negotiate the library's
// first choice (ECDSA) and call it a changed key.
func TestOnlyKnownKeyTypesAreNegotiated(t *testing.T) {
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := gossh.NewSignerFromKey(ecPriv)
	if err != nil {
		t.Fatal(err)
	}
	ed := newEd25519Signer(t)
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false, ec, ed)
	file := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(file, []byte(knownLine(t, srv, ed.PublicKey())), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := useKnownHosts(t, file, func() (bool, error) { return false, nil })
	if err := dialOnce(t, srv); err != nil {
		t.Fatalf("dial: %v", err)
	}
	if *asked != 0 {
		t.Error("a known host must not be asked about")
	}
}

func TestHashedKnownHostsStayHashed(t *testing.T) {
	srv := startTestServer(t, "tcp", "127.0.0.1:0", false)
	file := filepath.Join(t.TempDir(), "known_hosts")
	// An existing hashed entry for some other host, without a final newline.
	other := knownhosts.HashHostname("elsewhere.example") + " " +
		strings.TrimSpace(string(gossh.MarshalAuthorizedKey(newEd25519Signer(t).PublicKey())))
	if err := os.WriteFile(file, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := useKnownHosts(t, file, func() (bool, error) { return true, nil })
	if err := dialOnce(t, srv); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	host, _, _ := net.SplitHostPort(srv.addr)
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "|1|") || strings.Contains(string(data), host) {
		t.Fatalf("known_hosts =\n%s", data)
	}
	if err := dialOnce(t, srv); err != nil || *asked != 1 {
		t.Fatalf("hashed entry not recognised: err=%v asked=%d", err, *asked)
	}
}

func TestKeyLabel(t *testing.T) {
	for typ, want := range map[string]string{
		"ssh-ed25519":         "ED25519",
		"ssh-rsa":             "RSA",
		"ecdsa-sha2-nistp256": "ECDSA",
	} {
		if got := keyLabel(fakeKey(typ)); got != want {
			t.Errorf("keyLabel(%s) = %s", typ, got)
		}
	}
}

type fakeKey string

func (k fakeKey) Type() string                          { return string(k) }
func (k fakeKey) Marshal() []byte                       { return nil }
func (k fakeKey) Verify([]byte, *gossh.Signature) error { return nil }
