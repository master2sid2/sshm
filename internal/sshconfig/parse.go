// Package sshconfig reads OpenSSH client configuration (~/.ssh/config and
// the files it includes) into concrete hosts, and writes sshm profiles back
// out as a config file of its own.
//
// Only what sshm can use is interpreted: HostName, Port, User, IdentityFile,
// LocalForward and RemoteForward. Options sshm cannot honour (ProxyJump,
// ProxyCommand, ...) are reported as warnings so the user knows the imported
// profile may behave differently from `ssh <alias>`.
package sshconfig

import (
	"bufio"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// maxIncludeDepth matches OpenSSH's limit on nested Include directives.
const maxIncludeDepth = 16

// directive is one "keyword value..." line, with where it came from.
type directive struct {
	key  string // lower-cased keyword
	args []string
	file string
	line int
}

// block is a Host (or Match) section and the directives under it. The
// implicit section before the first Host line has patterns ["*"].
type block struct {
	patterns []string
	match    bool // a Match block: not evaluated, never applies
	file     string
	dirs     []directive
}

// Config is a parsed ssh_config with its includes expanded in place.
type Config struct {
	blocks []block
	// Files lists every file that was read, in order.
	Files []string
}

// Host is the effective configuration for one concrete Host alias.
type Host struct {
	Alias        string
	HostName     string // "" = same as Alias
	Port         int    // 0 = default (22)
	User         string
	IdentityFile string // first IdentityFile, as written (may start with ~)
	Forwards     []Forward
	// Source is the file where the alias's Host block was defined.
	Source string
	// Unsupported lists options in effect for this host that sshm ignores.
	Unsupported []string
}

// Forward is a LocalForward or RemoteForward rule.
type Forward struct {
	Remote     bool // RemoteForward (-R) rather than LocalForward (-L)
	BindHost   string
	BindPort   int
	TargetHost string
	TargetPort int
}

// unsupported are options that change how a connection is made; sshm cannot
// honour them, so a host using one may not connect the way `ssh` would.
var unsupported = map[string]bool{
	"proxyjump":           true,
	"proxycommand":        true,
	"certificatefile":     true,
	"pkcs11provider":      true,
	"securitykeyprovider": true,
}

// Load parses path (usually ~/.ssh/config). sshDir resolves relative Include
// paths, as ssh does for the user config ("~/.ssh"). A missing file yields
// an empty Config, not an error.
func Load(path, sshDir string) (*Config, error) {
	c := &Config{}
	if err := c.parseFile(path, sshDir, 0, "*"); err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	return c, nil
}

// parseFile appends path's blocks to c. current is the Host pattern list in
// effect where the Include appeared: OpenSSH evaluates an included file's
// leading directives within the including Host block.
func (c *Config) parseFile(path, sshDir string, depth int, current ...string) error {
	if depth > maxIncludeDepth {
		return fmt.Errorf("%s: Include nested too deeply", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	c.Files = append(c.Files, path)

	cur := block{patterns: current, file: path}
	flush := func() {
		if len(cur.dirs) > 0 {
			c.blocks = append(c.blocks, cur)
		}
	}

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		key, args, ok := splitLine(sc.Text())
		if !ok {
			continue
		}
		switch key {
		case "host":
			flush()
			cur = block{patterns: args, file: path}
		case "match":
			flush()
			cur = block{match: true, file: path}
		case "include":
			flush()
			for _, pat := range args {
				matches, err := expandInclude(pat, sshDir)
				if err != nil {
					return fmt.Errorf("%s:%d: %w", path, n, err)
				}
				for _, m := range matches {
					if cur.match {
						continue // included under a Match we do not evaluate
					}
					if err := c.parseFile(m, sshDir, depth+1, cur.patterns...); err != nil && !os.IsNotExist(err) {
						return err
					}
				}
			}
			cur = block{patterns: cur.patterns, match: cur.match, file: path}
		default:
			cur.dirs = append(cur.dirs, directive{key: key, args: args, file: path, line: n})
		}
	}
	flush()
	return sc.Err()
}

// expandInclude resolves an Include argument: ~ is the home directory, a
// relative path is relative to sshDir, and glob patterns are expanded in
// lexical order (as ssh does).
func expandInclude(pat, sshDir string) ([]string, error) {
	if strings.HasPrefix(pat, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		pat = filepath.Join(home, pat[2:])
	} else if !filepath.IsAbs(pat) {
		pat = filepath.Join(sshDir, pat)
	}
	matches, err := filepath.Glob(pat)
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	var files []string
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.Mode().IsRegular() {
			files = append(files, m)
		}
	}
	return files, nil
}

// splitLine parses one config line into a lower-cased keyword and its
// arguments. It accepts "Key value", "Key=value" and "Key = value", strips
// comments and honours double quotes.
func splitLine(line string) (key string, args []string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil, false
	}
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return strings.ToLower(line), nil, true
	}
	key = strings.ToLower(line[:end])
	rest := strings.TrimLeft(line[end:], " \t")
	rest = strings.TrimPrefix(rest, "=")
	return key, splitArgs(rest), true
}

func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			have = true
		case !inQuote && (r == ' ' || r == '\t'):
			if have {
				args = append(args, cur.String())
				cur.Reset()
				have = false
			}
		case !inQuote && r == '#' && !have:
			return args // trailing comment
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		args = append(args, cur.String())
	}
	return args
}

// Without returns a copy of c that ignores everything read from the given
// files — used to leave sshm's own export out of an import, both from the
// host list and from option resolution.
func (c *Config) Without(files ...string) *Config {
	drop := map[string]bool{}
	for _, f := range files {
		if abs, err := filepath.Abs(f); err == nil {
			drop[abs] = true
		}
	}
	out := &Config{}
	for _, b := range c.blocks {
		if abs, err := filepath.Abs(b.file); err != nil || !drop[abs] {
			out.blocks = append(out.blocks, b)
		}
	}
	for _, f := range c.Files {
		if abs, err := filepath.Abs(f); err != nil || !drop[abs] {
			out.Files = append(out.Files, f)
		}
	}
	return out
}

// Hosts returns every concrete alias (a Host pattern without wildcards or
// negation) in order of first appearance, each with its effective options
// computed the way ssh does: blocks are scanned in file order and, for each
// keyword, the first value obtained wins (IdentityFile and the forwards
// accumulate instead).
func (c *Config) Hosts() []Host {
	var hosts []Host
	seen := map[string]bool{}
	for _, b := range c.blocks {
		if b.match {
			continue
		}
		for _, p := range b.patterns {
			if seen[p] || strings.ContainsAny(p, "*?!") {
				continue
			}
			seen[p] = true
			hosts = append(hosts, c.resolve(p, b.file))
		}
	}
	return hosts
}

func (c *Config) resolve(alias, source string) Host {
	h := Host{Alias: alias, Source: source}
	got := map[string]bool{}
	for _, b := range c.blocks {
		if b.match || !matchHost(alias, b.patterns) {
			continue
		}
		for _, d := range b.dirs {
			if len(d.args) == 0 {
				continue
			}
			v := d.args[0]
			switch d.key {
			case "identityfile":
				if h.IdentityFile == "" && !strings.EqualFold(v, "none") {
					h.IdentityFile = v
				}
				continue
			case "localforward", "remoteforward":
				if fw, ok := parseForward(d.key == "remoteforward", d.args); ok {
					h.Forwards = append(h.Forwards, fw)
				}
				continue
			}
			if got[d.key] {
				continue
			}
			got[d.key] = true
			switch d.key {
			case "hostname":
				h.HostName = v
			case "port":
				h.Port, _ = strconv.Atoi(v)
			case "user":
				h.User = v
			default:
				if unsupported[d.key] && !strings.EqualFold(v, "none") {
					h.Unsupported = append(h.Unsupported, d.key)
				}
			}
		}
	}
	// In HostName, %h is the alias as typed; everywhere else it is the
	// resolved host name.
	h.HostName = expandTokens(h.HostName, alias, h)
	if h.HostName == alias {
		h.HostName = ""
	}
	h.IdentityFile = expandTokens(h.IdentityFile, cmp.Or(h.HostName, alias), h)
	return h
}

// expandTokens substitutes the ssh_config tokens sshm can know: %h (host),
// %n (alias), %r (user), %p (port), %d (home), %% (percent). Others stay.
func expandTokens(s, host string, h Host) string {
	if !strings.Contains(s, "%") {
		return s
	}
	port := "22"
	if h.Port != 0 {
		port = strconv.Itoa(h.Port)
	}
	home, _ := os.UserHomeDir()
	r := strings.NewReplacer("%%", "%", "%h", host, "%n", h.Alias, "%r", h.User, "%p", port, "%d", home)
	return r.Replace(s)
}

// matchHost reports whether alias matches a Host pattern list: at least one
// positive pattern matches and no negated one does.
func matchHost(alias string, patterns []string) bool {
	matched := false
	for _, p := range patterns {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			if globMatch(neg, alias) {
				return false
			}
			continue
		}
		if globMatch(p, alias) {
			matched = true
		}
	}
	return matched
}

// globMatch implements ssh's pattern syntax: * and ?, case-insensitive.
// (filepath.Match would also give [ and \ a meaning ssh does not.)
func globMatch(pattern, s string) bool {
	p, str := []rune(strings.ToLower(pattern)), []rune(strings.ToLower(s))
	var match func(i, j int) bool
	match = func(i, j int) bool {
		for i < len(p) {
			switch p[i] {
			case '*':
				for k := j; k <= len(str); k++ {
					if match(i+1, k) {
						return true
					}
				}
				return false
			case '?':
				if j >= len(str) {
					return false
				}
			default:
				if j >= len(str) || p[i] != str[j] {
					return false
				}
			}
			i++
			j++
		}
		return j == len(str)
	}
	return match(0, 0)
}

// parseForward parses "[bind_address:]port host:hostport" (also with
// [ipv6] brackets, or a single "host:port" argument with a space-less
// form "port:host:hostport" being unsupported by ssh too).
func parseForward(remote bool, args []string) (Forward, bool) {
	if len(args) < 2 {
		return Forward{}, false
	}
	bindHost, bindPort, ok := splitBind(args[0])
	if !ok {
		return Forward{}, false
	}
	th, tp, ok := splitHostPort(args[1])
	if !ok {
		return Forward{}, false
	}
	return Forward{Remote: remote, BindHost: bindHost, BindPort: bindPort, TargetHost: th, TargetPort: tp}, true
}

// splitBind parses "port", "addr:port", "[v6]:port" or "*:port".
func splitBind(s string) (string, int, bool) {
	if p, err := strconv.Atoi(s); err == nil {
		return "", p, validPort(p)
	}
	h, p, ok := splitHostPort(s)
	if h == "*" {
		h = "0.0.0.0"
	}
	return h, p, ok
}

func splitHostPort(s string) (string, int, bool) {
	var host, port string
	if rest, ok := strings.CutPrefix(s, "["); ok {
		h, after, found := strings.Cut(rest, "]")
		if !found {
			return "", 0, false
		}
		host, port = h, strings.TrimPrefix(after, ":")
	} else {
		i := strings.LastIndex(s, ":")
		if i < 0 {
			return "", 0, false
		}
		host, port = s[:i], s[i+1:]
	}
	p, err := strconv.Atoi(port)
	if err != nil || !validPort(p) {
		return "", 0, false
	}
	return host, p, true
}

func validPort(p int) bool { return p > 0 && p < 65536 }
