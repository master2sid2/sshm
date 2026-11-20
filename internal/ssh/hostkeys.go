package ssh

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/master2sid2/sshm/internal/termutil"
)

// Host keys are verified against the same files OpenSSH uses, so a host
// already trusted with `ssh` is trusted by sshm and vice versa:
//
//   - a known host presenting its known key connects silently;
//   - a known host presenting a different key is refused (HostKeyError);
//   - an unknown host shows its fingerprint and needs a typed "yes", after
//     which the key is appended to ~/.ssh/known_hosts.
//
// Before 0.2.0 sshm accepted any host key, so a man-in-the-middle could
// collect the password of every password-authenticated host.

// ErrHostKeyRejected is returned when the user does not accept the key of
// a host seen for the first time.
var ErrHostKeyRejected = errors.New("host key not accepted")

// HostKeyError reports a host whose key differs from the one on record (or
// was revoked). It is the case worth a loud warning: the server was
// reinstalled, or the connection is being intercepted.
type HostKeyError struct {
	Host    string          // as written in known_hosts, e.g. "[db]:2222"
	Key     gossh.PublicKey // what the server presented
	Known   []knownhosts.KnownKey
	Revoked bool
}

func (e *HostKeyError) Error() string {
	if e.Revoked {
		return fmt.Sprintf("host key for %s is marked @revoked in %s", e.Host, e.Known[0].Filename)
	}
	return fmt.Sprintf("host key for %s has CHANGED (now %s %s) — possible man-in-the-middle; refusing to connect",
		e.Host, e.Key.Type(), gossh.FingerprintSHA256(e.Key))
}

// Details is the full warning shown before returning to the TUI.
func (e *HostKeyError) Details() string {
	var b strings.Builder
	if e.Revoked {
		fmt.Fprintf(&b, "The host key presented by %s is REVOKED:\n", e.Host)
		fmt.Fprintf(&b, "  %s %s  (%s:%d)\n", e.Key.Type(), gossh.FingerprintSHA256(e.Key), e.Known[0].Filename, e.Known[0].Line)
		b.WriteString("Connection refused.\n")
		return b.String()
	}
	b.WriteString("@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n")
	b.WriteString("@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\n")
	b.WriteString("@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\n")
	b.WriteString("Someone could be eavesdropping on you right now (man-in-the-middle),\n")
	b.WriteString("or the host key has just been changed (server reinstalled).\n\n")
	fmt.Fprintf(&b, "%s now presents:\n  %s %s\n", e.Host, e.Key.Type(), gossh.FingerprintSHA256(e.Key))
	b.WriteString("Known keys for this host:\n")
	for _, k := range e.Known {
		fmt.Fprintf(&b, "  %s %s  (%s:%d)\n", k.Key.Type(), gossh.FingerprintSHA256(k.Key), k.Filename, k.Line)
	}
	fmt.Fprintf(&b, "\nIf the change is expected, remove the old key with:\n  ssh-keygen -R '%s'\n", e.Host)
	b.WriteString("then connect again to review and accept the new one.\n")
	return b.String()
}

// hostKeys is the known_hosts database used for one connection.
type hostKeys struct {
	userFile string   // read, and appended to when a new key is accepted
	files    []string // every file read (user + system)
	db       gossh.HostKeyCallback
	// confirm asks whether to trust an unknown key; nil refuses.
	confirm func(question string) (bool, error)
}

// loadHostKeys reads userFile and the given system files (missing ones are
// skipped).
func loadHostKeys(userFile string, systemFiles ...string) (*hostKeys, error) {
	h := &hostKeys{userFile: userFile, confirm: askYes}
	for _, f := range append([]string{userFile}, systemFiles...) {
		if fi, err := os.Stat(f); err == nil && fi.Mode().IsRegular() {
			h.files = append(h.files, f)
		}
	}
	db, err := knownhosts.New(h.files...)
	if err != nil {
		return nil, fmt.Errorf("read known_hosts: %w", err)
	}
	h.db = db
	return h, nil
}

// defaultHostKeys uses ~/.ssh/known_hosts (and /etc/ssh/ssh_known_hosts).
// A variable so tests can point it elsewhere.
var defaultHostKeys = func() (*hostKeys, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return loadHostKeys(filepath.Join(home, ".ssh", "known_hosts"), "/etc/ssh/ssh_known_hosts")
}

// placeholderKey can never be a host's key; checking it against the
// database reveals which keys are on record for a host.
var placeholderKey = func() gossh.PublicKey {
	k, err := gossh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		panic(err)
	}
	return k
}()

// algorithms returns the host key algorithms to offer for addr: those of
// the keys on record, like OpenSSH does. Otherwise a server with several
// keys could present one of a type we have never seen and be reported as
// changed. Nil (library default) for an unknown host.
func (h *hostKeys) algorithms(addr string, remote net.Addr) []string {
	keyErr, ok := errors.AsType[*knownhosts.KeyError](h.db(addr, remote, placeholderKey))
	if !ok {
		return nil
	}
	var algos []string
	for _, k := range keyErr.Want {
		switch t := k.Key.Type(); t {
		case gossh.KeyAlgoRSA:
			algos = append(algos, gossh.KeyAlgoRSASHA512, gossh.KeyAlgoRSASHA256, gossh.KeyAlgoRSA)
		default:
			algos = append(algos, t)
		}
	}
	slices.Sort(algos)
	return slices.Compact(algos)
}

// callback returns the HostKeyCallback for one connection. beforePrompt and
// afterPrompt let the caller lift the handshake deadline while the user
// reads the fingerprint.
func (h *hostKeys) callback(beforePrompt, afterPrompt func()) gossh.HostKeyCallback {
	return func(addr string, remote net.Addr, key gossh.PublicKey) error {
		err := h.db(addr, remote, key)
		if err == nil {
			return nil
		}
		host := knownhosts.Normalize(addr)
		if rev, ok := errors.AsType[*knownhosts.RevokedError](err); ok {
			return &HostKeyError{Host: host, Key: key, Known: []knownhosts.KnownKey{rev.Revoked}, Revoked: true}
		}
		keyErr, ok := errors.AsType[*knownhosts.KeyError](err)
		if !ok {
			return err
		}
		if len(keyErr.Want) > 0 {
			return &HostKeyError{Host: host, Key: key, Known: keyErr.Want}
		}

		// Unknown host: ask, like ssh's StrictHostKeyChecking=ask.
		if h.confirm == nil {
			return fmt.Errorf("%w: %s is not in %s", ErrHostKeyRejected, host, h.userFile)
		}
		where := host
		if ip, _, err := net.SplitHostPort(remote.String()); err == nil && !strings.Contains(host, ip) {
			where += " (" + ip + ")"
		}
		q := fmt.Sprintf("The authenticity of host '%s' can't be established.\n%s key fingerprint is %s.\nAre you sure you want to continue connecting (yes/no)? ",
			where, keyLabel(key), gossh.FingerprintSHA256(key))
		if beforePrompt != nil {
			beforePrompt()
		}
		yes, err := h.confirm(q)
		if afterPrompt != nil {
			afterPrompt()
		}
		if err != nil {
			return err
		}
		if !yes {
			return ErrHostKeyRejected
		}
		if err := h.add(host, key); err != nil {
			// Trust it for this connection anyway; say why it will ask again.
			fmt.Fprintf(os.Stderr, "Warning: could not save the host key to %s: %v\n", h.userFile, err)
			return nil
		}
		fmt.Fprintf(os.Stderr, "Warning: permanently added '%s' (%s) to %s.\n", host, keyLabel(key), h.userFile)
		return nil
	}
}

// add appends key for host to the user's known_hosts, hashing the host name
// when the file already uses hashed entries (HashKnownHosts yes).
func (h *hostKeys) add(host string, key gossh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(h.userFile), 0o700); err != nil {
		return err
	}
	existing, err := os.ReadFile(h.userFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	name := host
	if bytes.Contains(existing, []byte("\n|1|")) || bytes.HasPrefix(existing, []byte("|1|")) {
		name = knownhosts.HashHostname(host)
	}
	line := name + " " + string(bytes.TrimSpace(gossh.MarshalAuthorizedKey(key))) + "\n"
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		line = "\n" + line
	}

	f, err := os.OpenFile(h.userFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func keyLabel(key gossh.PublicKey) string {
	t := strings.TrimPrefix(key.Type(), "ssh-")
	t, _, _ = strings.Cut(t, "-sha2-")
	return strings.ToUpper(t)
}

// askYes prints question and accepts only "yes", as ssh does.
func askYes(question string) (bool, error) {
	for {
		ans, err := termutil.ReadLine(question)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "yes":
			return true, nil
		case "no", "":
			return false, nil
		}
		question = "Please type 'yes' or 'no': "
	}
}
