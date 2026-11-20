package sshconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/master2sid2/sshm/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const mainConfig = `# personal hosts
Include config.d/*.conf

Host web web-alias
    HostName 10.0.0.10
    User deploy
    Port 2222
    IdentityFile ~/.ssh/id_web
    IdentityFile ~/.ssh/id_second
    LocalForward 5433 127.0.0.1:5432
    RemoteForward 0.0.0.0:9090 localhost:8080

Host db
    HostName=db.internal
    ProxyJump bastion

Host bastion
    HostName "bastion.example.com"

Host *.example.com !skip.example.com
    User wildcard-user

Match host db
    User from-match

Host %h-template
    HostName %h.lan

Host *
    User fallback
    Port 22
`

const workConfig = `Host work-box
    HostName work.example.com
    LocalForward [::1]:8080 [fd00::1]:80
`

func loadFixture(t *testing.T) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config"), mainConfig)
	writeFile(t, filepath.Join(dir, "config.d", "work.conf"), workConfig)
	writeFile(t, filepath.Join(dir, "config.d", "ignored.txt"), "Host not-included\n")
	c, err := Load(filepath.Join(dir, "config"), dir)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func hostByAlias(t *testing.T, hosts []Host, alias string) Host {
	t.Helper()
	for _, h := range hosts {
		if h.Alias == alias {
			return h
		}
	}
	t.Fatalf("alias %q not found among %d hosts", alias, len(hosts))
	return Host{}
}

func TestHostsResolveLikeSSH(t *testing.T) {
	c, dir := loadFixture(t)
	hosts := c.Hosts()

	var aliases []string
	for _, h := range hosts {
		aliases = append(aliases, h.Alias)
	}
	want := []string{"work-box", "web", "web-alias", "db", "bastion", "%h-template"}
	if !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases = %v, want %v", aliases, want)
	}

	web := hostByAlias(t, hosts, "web")
	if web.HostName != "10.0.0.10" || web.User != "deploy" || web.Port != 2222 {
		t.Errorf("web = %+v", web)
	}
	if web.IdentityFile != "~/.ssh/id_web" {
		t.Errorf("first IdentityFile must win, got %q", web.IdentityFile)
	}
	wantFw := []Forward{
		{BindPort: 5433, TargetHost: "127.0.0.1", TargetPort: 5432},
		{Remote: true, BindHost: "0.0.0.0", BindPort: 9090, TargetHost: "localhost", TargetPort: 8080},
	}
	if !reflect.DeepEqual(web.Forwards, wantFw) {
		t.Errorf("forwards = %+v", web.Forwards)
	}
	if web.Source != filepath.Join(dir, "config") {
		t.Errorf("source = %q", web.Source)
	}

	// Defaults from "Host *" fill only what is not already set; Match is
	// not evaluated.
	db := hostByAlias(t, hosts, "db")
	if db.HostName != "db.internal" || db.User != "fallback" || db.Port != 22 {
		t.Errorf("db = %+v", db)
	}
	if !reflect.DeepEqual(db.Unsupported, []string{"proxyjump"}) {
		t.Errorf("db unsupported = %v", db.Unsupported)
	}

	// Wildcard block applies to bastion.example.com? No: the alias is
	// "bastion", the pattern matches aliases, not HostNames.
	if b := hostByAlias(t, hosts, "bastion"); b.HostName != "bastion.example.com" || b.User != "fallback" {
		t.Errorf("bastion = %+v", b)
	}

	if tpl := hostByAlias(t, hosts, "%h-template"); tpl.HostName != "%h-template.lan" {
		t.Errorf("%%h must expand to the alias, got %q", tpl.HostName)
	}

	work := hostByAlias(t, hosts, "work-box")
	if work.Source != filepath.Join(dir, "config.d", "work.conf") {
		t.Errorf("work source = %q", work.Source)
	}
	if len(work.Forwards) != 1 || work.Forwards[0].BindHost != "::1" || work.Forwards[0].TargetHost != "fd00::1" {
		t.Errorf("ipv6 forward = %+v", work.Forwards)
	}
	if FolderName(work.Source, dir) != "work" || FolderName(web.Source, dir) != "ssh config" {
		t.Error("folder names")
	}
}

func TestGlobAndNegation(t *testing.T) {
	cases := []struct {
		alias    string
		patterns []string
		want     bool
	}{
		{"a.example.com", []string{"*.example.com"}, true},
		{"skip.example.com", []string{"*.example.com", "!skip.example.com"}, false},
		{"web1", []string{"web?"}, true},
		{"web10", []string{"web?"}, false},
		{"WEB", []string{"web"}, true},
		{"x", []string{"!y"}, false}, // only a negation: nothing positive matched
		{"a[1]", []string{"a[1]"}, true},
	}
	for _, tc := range cases {
		if got := matchHost(tc.alias, tc.patterns); got != tc.want {
			t.Errorf("matchHost(%q, %v) = %v", tc.alias, tc.patterns, got)
		}
	}
}

func TestSplitLine(t *testing.T) {
	cases := []struct {
		in   string
		key  string
		args []string
	}{
		{"HostName example.com", "hostname", []string{"example.com"}},
		{"  Port=2222", "port", []string{"2222"}},
		{"User = alice # comment", "user", []string{"alice"}},
		{`IdentityFile "/path/with space/key"`, "identityfile", []string{"/path/with space/key"}},
	}
	for _, tc := range cases {
		key, args, ok := splitLine(tc.in)
		if !ok || key != tc.key || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("splitLine(%q) = %q %q %v", tc.in, key, args, ok)
		}
	}
	if _, _, ok := splitLine("   # only a comment"); ok {
		t.Error("comment line must be skipped")
	}
}

func TestMissingConfigIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent"), t.TempDir())
	if err != nil || len(c.Hosts()) != 0 {
		t.Fatalf("got %v, %v", c, err)
	}
}

func TestIncludeLoopIsBounded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config"), "Include config\nHost a\n")
	if _, err := Load(filepath.Join(dir, "config"), dir); err == nil {
		t.Fatal("a self-including config must fail, not recurse forever")
	}
}

func TestRenderNeverWritesSecretsAndRoundTrips(t *testing.T) {
	key := config.NewProfile()
	key.Name, key.Host, key.User, key.Port = "web 01", "10.0.0.1", "deploy", 2222
	key.AuthType, key.KeyPath, key.KeyPassword = config.AuthKey, "~/.ssh/id_web", "KEY-PASSPHRASE"
	key.Tunnels = []config.Tunnel{
		{Direction: config.TunnelLocal, LocalHost: "127.0.0.1", LocalPort: 5433, RemoteHost: "127.0.0.1", RemotePort: 5432},
		{Direction: config.TunnelRemote, LocalHost: "127.0.0.1", LocalPort: 8080, RemoteHost: "0.0.0.0", RemotePort: 9090},
	}
	pw := config.NewProfile()
	pw.Name, pw.Host, pw.User = "db", "db.internal", "root"
	pw.AuthType, pw.Password = config.AuthPassword, "SECRET-PASSWORD"
	dup := config.NewProfile()
	dup.Name, dup.Host = "db", "db2.internal"

	out := Render([]ExportEntry{
		{Profile: key},
		{Profile: pw, Folders: []string{"prod"}},
		{Profile: dup, Folders: []string{"stage"}},
	}, "v0.2.0")
	text := string(out)
	for _, secret := range []string{"SECRET-PASSWORD", "KEY-PASSPHRASE"} {
		if strings.Contains(text, secret) {
			t.Fatalf("export leaked %q:\n%s", secret, text)
		}
	}
	if !strings.HasPrefix(text, GeneratedHeader) {
		t.Fatal("missing generated header")
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config"), text)
	c, err := Load(filepath.Join(dir, "config"), dir)
	if err != nil {
		t.Fatal(err)
	}
	hosts := c.Hosts()
	if len(hosts) != 3 {
		t.Fatalf("hosts = %+v\n%s", hosts, text)
	}
	if hosts[0].Alias != "web-01" || hosts[1].Alias != "prod-db" || hosts[2].Alias != "stage-db" {
		t.Errorf("aliases = %q %q %q", hosts[0].Alias, hosts[1].Alias, hosts[2].Alias)
	}

	back := hosts[0].Profile("me")
	if back.Host != key.Host || back.User != key.User || back.Port != key.Port ||
		back.AuthType != config.AuthKey || back.KeyPath != key.KeyPath {
		t.Errorf("round trip = %+v", back)
	}
	if !reflect.DeepEqual(back.Tunnels, key.Tunnels) {
		t.Errorf("tunnels round trip:\n got %+v\nwant %+v", back.Tunnels, key.Tunnels)
	}
	if p := hosts[2].Profile("me"); p.User != "me" || p.AuthType != config.AuthAgent {
		t.Errorf("a host without User/IdentityFile: %+v", p)
	}
}

func TestIncludesAndPrepend(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	exported := filepath.Join(dir, "config.d", "sshm.conf")
	writeFile(t, exported, "# Generated by sshm\nHost x\n")

	writeFile(t, cfg, "Host a\n  User b\n")
	if ok, _ := Includes(cfg, dir, exported); ok {
		t.Fatal("not included yet")
	}
	writeFile(t, cfg, string(PrependInclude([]byte("Host a\n  User b\n"))))
	if ok, err := Includes(cfg, dir, exported); !ok || err != nil {
		t.Fatalf("after prepend: %v %v", ok, err)
	}
	writeFile(t, cfg, "Include config.d/*\n")
	if ok, _ := Includes(cfg, dir, exported); !ok {
		t.Fatal("a glob include covers the file")
	}
}

func TestAlias(t *testing.T) {
	for in, want := range map[string]string{
		"web 01":      "web-01",
		"  prod/db  ": "prod-db",
		"*wild?":      "wild",
		"user@host":   "user@host",
		"":            "host",
		"сервер":      "сервер",
		"a  -  b":     "a-b",
	} {
		if got := Alias(in); got != want {
			t.Errorf("Alias(%q) = %q, want %q", in, got, want)
		}
	}
}
