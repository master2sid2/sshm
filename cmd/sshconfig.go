package cmd

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/master2sid2/sshm/internal/config"
	"github.com/master2sid2/sshm/internal/fsutil"
	sshclient "github.com/master2sid2/sshm/internal/ssh"
	"github.com/master2sid2/sshm/internal/sshconfig"
	"github.com/master2sid2/sshm/internal/termutil"
	"github.com/master2sid2/sshm/internal/version"
)

// ── import ───────────────────────────────────────────────────────────────────

// importReport describes what an import did (or, for a dry run, would do).
type importReport struct {
	added    []string // "folder / alias → user@host:port"
	skipped  []string // duplicates of an existing profile
	warnings []string
}

func (r importReport) print(w io.Writer, dry bool) {
	verb := "Imported"
	if dry {
		verb = "Would import"
	}
	for _, a := range r.added {
		fmt.Fprintf(w, "  + %s\n", a)
	}
	for _, s := range r.skipped {
		fmt.Fprintf(w, "  = %s (already in the vault)\n", s)
	}
	for _, s := range r.warnings {
		fmt.Fprintf(w, "  ! %s\n", s)
	}
	fmt.Fprintf(w, "%s %d host(s), skipped %d duplicate(s).\n", verb, len(r.added), len(r.skipped))
}

func runImportSSH(args []string) error {
	fs := flag.NewFlagSet("import-ssh", flag.ExitOnError)
	vault := vaultFlag(fs)
	cfgFlag := fs.String("config", "", "ssh config to read (default: ~/.ssh/config)")
	dry := fs.Bool("n", false, "dry run: show what would be imported, change nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sshDir, cfgPath, exportPath, err := sshconfig.Paths()
	if err != nil {
		return err
	}
	cfgPath = cmp.Or(*cfgFlag, cfgPath)

	vaultPath, err := vault()
	if err != nil {
		return err
	}
	store, err := openVault(vaultPath, true)
	if err != nil {
		return err
	}
	rep, err := importSSH(store, cfgPath, sshDir, exportPath, *dry)
	if err != nil {
		return err
	}
	rep.print(os.Stdout, *dry)
	return nil
}

// sshHosts lists the concrete hosts of cfgPath and its includes, leaving
// out sshm's own export (re-importing it would only duplicate the vault).
func sshHosts(cfgPath, sshDir, exportPath string) ([]sshconfig.Host, error) {
	c, err := sshconfig.Load(cfgPath, sshDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	return c.Without(exportPath).Hosts(), nil
}

// importSSH adds the hosts of an ssh config to the vault, one root folder
// per source file. A host already in the vault (same host, user and port,
// anywhere in the tree) is skipped, so importing again only brings in what
// is new. Passwords are not part of an ssh config: key hosts get their
// IdentityFile, everything else uses the ssh-agent.
func importSSH(store *config.Store, cfgPath, sshDir, exportPath string, dry bool) (importReport, error) {
	var rep importReport
	hosts, err := sshHosts(cfgPath, sshDir, exportPath)
	if err != nil {
		return rep, err
	}

	have := map[string]bool{}
	for _, p := range store.Profiles() {
		have[identity(p)] = true
	}

	// Reuse an existing, unprotected root folder of the same name; a locked
	// one is left alone (importing into it would bypass its password).
	folders := map[string]string{} // folder name → group ID
	for _, g := range store.GroupsInGroup("") {
		if _, dup := folders[g.Name]; !dup && !g.Locked() {
			folders[g.Name] = g.ID
		}
	}
	var newGroups []config.Group
	folderID := func(name string) string {
		if id, ok := folders[name]; ok {
			return id
		}
		g := config.NewGroup("")
		g.Name = name
		newGroups = append(newGroups, g)
		folders[name] = g.ID
		return g.ID
	}

	defUser := currentUser()
	var profiles []config.Profile
	for _, h := range hosts {
		p := h.Profile(defUser)
		label := fmt.Sprintf("%s / %s → %s@%s:%d", sshconfig.FolderName(h.Source, sshDir), h.Alias, p.User, p.Host, p.Port)
		if have[identity(p)] {
			rep.skipped = append(rep.skipped, label)
			continue
		}
		have[identity(p)] = true

		if p.AuthType == config.AuthKey {
			if _, err := os.Stat(sshclient.ExpandPath(p.KeyPath)); err != nil {
				rep.warnings = append(rep.warnings, fmt.Sprintf("%s: IdentityFile %s not found, using the ssh-agent instead", h.Alias, p.KeyPath))
				p.AuthType, p.KeyPath = config.AuthAgent, ""
			}
		}
		for _, opt := range h.Unsupported {
			rep.warnings = append(rep.warnings, fmt.Sprintf("%s: %s is not supported by sshm; the connection may not work as with `ssh %s`", h.Alias, opt, h.Alias))
		}
		p.GroupID = folderID(sshconfig.FolderName(h.Source, sshDir))
		profiles = append(profiles, p)
		rep.added = append(rep.added, label)
	}

	if dry || len(profiles) == 0 {
		return rep, nil
	}
	if err := store.AddBatch(newGroups, profiles); err != nil {
		return rep, fmt.Errorf("save vault: %w", err)
	}
	return rep, nil
}

// identity is what makes two profiles the same server login, for skipping
// duplicates on import and hosts the user's ssh config already has on export.
func identity(p config.Profile) string {
	return strings.ToLower(p.Host) + "\x00" + p.User + "\x00" + strconv.Itoa(p.Port)
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// offerFirstRunImport is called once a brand-new vault exists: when the
// user already has hosts in ~/.ssh/config, it offers to bring them in.
func offerFirstRunImport(store *config.Store) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	sshDir, cfgPath, exportPath, err := sshconfig.Paths()
	if err != nil {
		return
	}
	hosts, err := sshHosts(cfgPath, sshDir, exportPath)
	if err != nil || len(hosts) == 0 {
		return
	}
	q := fmt.Sprintf("Found %d host(s) in %s. Import them (no passwords; key paths are kept)?", len(hosts), cfgPath)
	if !termutil.Confirm(q, true) {
		fmt.Fprintln(os.Stderr, "Skipped. Run \"sshm import-ssh\" any time to import them.")
		return
	}
	rep, err := importSSH(store, cfgPath, sshDir, exportPath, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "import:", err)
		return
	}
	rep.print(os.Stderr, false)
}

// ── export ───────────────────────────────────────────────────────────────────

func runExportSSH(args []string) error {
	fs := flag.NewFlagSet("export-ssh", flag.ExitOnError)
	vault := vaultFlag(fs)
	outFlag := fs.String("o", "", "output file, or - for stdout (default: ~/.ssh/config.d/sshm.conf)")
	yes := fs.Bool("y", false, "add the Include line to ~/.ssh/config without asking")
	locked := fs.Bool("locked", false, "also export hosts in password-protected folders (asks each folder's password)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sshDir, cfgPath, exportPath, err := sshconfig.Paths()
	if err != nil {
		return err
	}
	out := cmp.Or(*outFlag, exportPath)

	vaultPath, err := vault()
	if err != nil {
		return err
	}
	store, err := openVault(vaultPath, true)
	if err != nil {
		return err
	}

	entries, skipped, err := exportEntries(store, *locked)
	if err != nil {
		return err
	}
	// Hosts the user's own ssh config already defines are left out: written
	// twice, ssh would merge both definitions (and bind forwards twice).
	own, err := sshHosts(cfgPath, sshDir, exportPath)
	if err != nil {
		return err
	}
	entries, dups := withoutKnown(entries, own)
	data := sshconfig.Render(entries, version.Version)

	if out == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := writeExport(out, data); err != nil {
		return err
	}
	fmt.Printf("Exported %d host(s) to %s (no passwords).\n", len(entries), out)
	if dups > 0 {
		fmt.Printf("%d host(s) already defined in your ssh config were left out.\n", dups)
	}
	if skipped > 0 {
		fmt.Printf("%d host(s) in password-protected folders were left out; -locked includes them.\n", skipped)
	}

	if filepath.Clean(out) == exportPath {
		return ensureInclude(cfgPath, sshDir, exportPath, *yes)
	}
	return nil
}

// exportEntries collects every profile with its folder path, sorted by
// folder then name. Profiles under a locked folder are skipped unless
// withLocked is set, in which case each locked folder's password is asked
// once (a wrong or empty answer skips that folder).
func exportEntries(store *config.Store, withLocked bool) ([]sshconfig.ExportEntry, int, error) {
	verdict := map[string]bool{} // locked group ID → password given
	var entries []sshconfig.ExportEntry
	skipped := 0
	for _, p := range store.Profiles() {
		path := store.GroupPath(p.GroupID)
		ok := true
		for _, g := range path {
			if !g.Locked() {
				continue
			}
			v, asked := verdict[g.ID]
			if !asked {
				v = false
				if withLocked {
					pw, err := termutil.ReadSecret(fmt.Sprintf("Password for folder %q (enter to skip): ", g.Name))
					if errors.Is(err, termutil.ErrCancelled) {
						return nil, 0, errors.New("cancelled")
					}
					v = err == nil && pw != "" && g.CheckPassword(pw)
					if !v && pw != "" {
						fmt.Fprintf(os.Stderr, "Wrong password: folder %q is left out.\n", g.Name)
					}
				}
				verdict[g.ID] = v
			}
			if !v {
				ok = false
				break
			}
		}
		if !ok {
			skipped++
			continue
		}
		names := make([]string, len(path))
		for i, g := range path {
			names[i] = g.Name
		}
		entries = append(entries, sshconfig.ExportEntry{Profile: p, Folders: names})
	}
	slices.SortStableFunc(entries, func(a, b sshconfig.ExportEntry) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(strings.Join(a.Folders, "/")), strings.ToLower(strings.Join(b.Folders, "/"))),
			cmp.Compare(strings.ToLower(a.Profile.Name), strings.ToLower(b.Profile.Name)),
		)
	})
	return entries, skipped, nil
}

// withoutKnown drops entries whose server login (host, user, port) is
// already one of the user's own ssh config hosts, and counts them.
func withoutKnown(entries []sshconfig.ExportEntry, own []sshconfig.Host) ([]sshconfig.ExportEntry, int) {
	known := map[string]bool{}
	user := currentUser()
	for _, h := range own {
		known[identity(h.Profile(user))] = true
	}
	n := len(entries)
	entries = slices.DeleteFunc(entries, func(e sshconfig.ExportEntry) bool {
		p := e.Profile
		p.User = cmp.Or(p.User, user)
		return known[identity(p)]
	})
	return entries, n - len(entries)
}

// writeExport writes the generated file, refusing to replace a file sshm
// did not generate unless the user confirms.
func writeExport(out string, data []byte) error {
	if existing, err := os.ReadFile(out); err == nil && !strings.HasPrefix(string(existing), sshconfig.GeneratedHeader) {
		if !termutil.Confirm(fmt.Sprintf("%s exists and was not written by sshm. Overwrite it?", out), false) {
			return errors.New("aborted: nothing was written")
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(out, data, 0o600)
}

// ensureInclude makes ~/.ssh/config read sshm's export. If it does not
// already (directly or through a glob), it offers to add the Include line at
// the top, keeping the previous file as config.bak. Nothing else in the
// file is touched.
func ensureInclude(cfgPath, sshDir, exportPath string, yes bool) error {
	included, err := sshconfig.Includes(cfgPath, sshDir, exportPath)
	if err != nil {
		return err
	}
	if included {
		fmt.Printf("%s already includes it: \"ssh <alias>\" works.\n", cfgPath)
		return nil
	}
	if !yes && !termutil.Confirm(fmt.Sprintf("%s does not read it yet. Add %q at the top (the old file is kept as config.bak)?", cfgPath, sshconfig.IncludeLine), true) {
		fmt.Printf("Not changed. To use the exported hosts with ssh, add this line at the top of %s:\n  %s\n", cfgPath, sshconfig.IncludeLine)
		return nil
	}

	old, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	if err == nil {
		if err := fsutil.WriteFileAtomic(cfgPath+".bak", old, 0o600); err != nil {
			return fmt.Errorf("back up %s: %w", cfgPath, err)
		}
	}
	if err := fsutil.WriteFileAtomic(cfgPath, sshconfig.PrependInclude(old), 0o600); err != nil {
		return err
	}
	fmt.Printf("Added %q to %s.\n", sshconfig.IncludeLine, cfgPath)
	return nil
}
