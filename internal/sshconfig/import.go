package sshconfig

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"

	"github.com/master2sid2/sshm/internal/config"
)

// Profile converts h to an sshm profile. A host with an IdentityFile uses
// key auth with that path (kept as written, ~ included); anything else uses
// the ssh-agent, which is also what ssh falls back to. Passwords are never
// part of an ssh config, so none is set.
func (h Host) Profile(defaultUser string) config.Profile {
	p := config.NewProfile()
	p.Name = h.Alias
	p.Host = cmp.Or(h.HostName, h.Alias)
	p.Port = cmp.Or(h.Port, 22)
	p.User = cmp.Or(h.User, defaultUser)
	if h.IdentityFile != "" {
		p.AuthType = config.AuthKey
		p.KeyPath = h.IdentityFile
	}
	for _, f := range h.Forwards {
		t := config.Tunnel{Direction: config.TunnelLocal, LocalHost: f.BindHost, LocalPort: f.BindPort, RemoteHost: f.TargetHost, RemotePort: f.TargetPort}
		if f.Remote {
			t = config.Tunnel{Direction: config.TunnelRemote, RemoteHost: f.BindHost, RemotePort: f.BindPort, LocalHost: f.TargetHost, LocalPort: f.TargetPort}
		}
		if t.LocalHost == "" {
			t.LocalHost = "127.0.0.1"
		}
		p.Tunnels = append(p.Tunnels, t)
	}
	return p
}

// FolderName is the vault folder a source file's hosts are imported into:
// "ssh config" for ~/.ssh/config itself, the file name (without .conf)
// for an included file such as ~/.ssh/config.d/work.conf.
func FolderName(source, sshDir string) string {
	if filepath.Clean(source) == filepath.Join(sshDir, "config") {
		return "ssh config"
	}
	return cmp.Or(strings.TrimSuffix(filepath.Base(source), ".conf"), "ssh config")
}

// Paths returns the user's ssh directory, main config and the file sshm
// exports to.
func Paths() (sshDir, configPath, exportPath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}
	sshDir = filepath.Join(home, ".ssh")
	return sshDir, filepath.Join(sshDir, "config"), filepath.Join(sshDir, "config.d", "sshm.conf"), nil
}
