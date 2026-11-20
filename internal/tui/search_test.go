package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/master2sid2/sshm/internal/config"
)

// searchFixture: root has "web-root"; prod/ has "prod-db" and prod/eu/
// has "eu-web"; vault/ is locked (password "vault") and has "secret-web".
func searchFixture(t *testing.T) (*App, map[string]config.Group) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "vault.enc"), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]config.Group{}
	mkGroup := func(name, parent string) {
		g := config.NewGroup(parent)
		g.Name = name
		if err := store.AddGroup(g); err != nil {
			t.Fatal(err)
		}
		groups[name] = g
	}
	mkGroup("prod", "")
	mkGroup("eu", groups["prod"].ID)
	mkGroup("vault", "")
	if err := store.SetGroupPassword(groups["vault"].ID, "vault"); err != nil {
		t.Fatal(err)
	}
	mkProfile := func(name, host, group string) {
		p := config.NewProfile()
		p.Name, p.Host, p.User, p.GroupID = name, host, "deploy", group
		p.AuthType, p.Password = config.AuthPassword, "pw-"+name
		if err := store.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	mkProfile("web-root", "10.0.0.1", "")
	mkProfile("prod-db", "10.1.0.1", groups["prod"].ID)
	mkProfile("eu-web", "10.2.0.1", groups["eu"].ID)
	mkProfile("secret-web", "10.3.0.1", groups["vault"].ID)

	a := New(store, "", false)
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return a, groups
}

func visibleNames(a *App) []string {
	var out []string
	for _, it := range a.list.inner.Items() {
		e := it.(listEntry)
		if e.isGroup {
			out = append(out, "["+e.group.Name+"]")
		} else {
			out = append(out, e.profile.Name)
		}
	}
	return out
}

func TestSearchSpansSubfoldersButNotLockedOnes(t *testing.T) {
	a, groups := searchFixture(t)
	send(a, key("/"))
	typeText(a, "web")
	if got := strings.Join(visibleNames(a), ","); got != "eu-web,web-root" {
		t.Fatalf("results = %s", got)
	}
	// Each hit shows where it lives.
	for _, it := range a.list.inner.Items() {
		if e := it.(listEntry); e.profile.Name == "eu-web" && e.path != "prod / eu" {
			t.Errorf("path = %q", e.path)
		}
	}

	// Once the locked folder is unlocked, it is searched too.
	a.unlockedGroups[groups["vault"].ID] = struct{}{}
	a.refreshList()
	if got := strings.Join(visibleNames(a), ","); got != "eu-web,secret-web,web-root" {
		t.Fatalf("after unlock = %s", got)
	}

	// Matching on the folder path and the host works as well.
	send(a, key("esc"))
	send(a, key("/"))
	typeText(a, "10.1.")
	if got := strings.Join(visibleNames(a), ","); got != "prod-db" {
		t.Fatalf("host search = %s", got)
	}
}

func TestSearchEnterConnectsAndEscClears(t *testing.T) {
	a, _ := searchFixture(t)
	send(a, key("/"))
	typeText(a, "prod-db")
	_, cmd := a.Update(key("enter"))
	msgs := execCmds(cmd)
	if len(msgs) != 1 {
		t.Fatalf("enter produced %v", msgs)
	}
	if c, ok := msgs[0].(connectMsg); !ok || c.profile.Name != "prod-db" {
		t.Fatalf("enter must connect to the selected hit, got %#v", msgs[0])
	}
	if a.filtering || a.filterQuery != "prod-db" {
		t.Fatal("after enter the results stay, the keys go back to the list")
	}
	// "q" now quits instead of being typed into the search.
	if _, cmd := a.Update(key("q")); cmd == nil {
		t.Fatal("q should quit once the search line has lost focus")
	}
	send(a, key("esc"))
	if a.filterQuery != "" || strings.Join(visibleNames(a), ",") != "[prod],[vault],web-root" {
		t.Fatalf("esc must clear the search, got %v", visibleNames(a))
	}
}

func TestSortCyclesAndReverses(t *testing.T) {
	a, groups := searchFixture(t)
	a.groupStack = []config.Group{groups["prod"]}
	for _, n := range []struct{ name, host string }{{"b", "10.9.0.1"}, {"a", "10.9.0.2"}, {"c", "10.0.0.9"}} {
		p := config.NewProfile()
		p.Name, p.Host, p.GroupID = n.name, n.host, groups["prod"].ID
		if err := a.store.Add(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct UpdatedAt
	}
	a.refreshList()
	if got := strings.Join(visibleNames(a), ","); got != "[eu],a,b,c,prod-db" {
		t.Fatalf("by name = %s", got)
	}
	send(a, key("s"))
	if got := strings.Join(visibleNames(a), ","); got != "[eu],c,prod-db,b,a" {
		t.Fatalf("by host = %s", got)
	}
	send(a, key("s"))
	send(a, key("S"))
	if got := strings.Join(visibleNames(a), ","); got != "[eu],c,a,b,prod-db" {
		t.Fatalf("by updated, newest first = %s", got)
	}
}

func TestRevealIsPerProfile(t *testing.T) {
	a, _ := searchFixture(t)
	send(a, key("/"))
	typeText(a, "web-root")
	send(a, key("esc")) // back to the folder; cursor stays on web-root
	for i, it := range a.list.inner.Items() {
		if it.(listEntry).profile.Name == "web-root" {
			a.list.inner.Select(i)
		}
	}
	if strings.Contains(a.detailsView(60), "pw-web-root") {
		t.Fatal("password visible before ctrl+r")
	}
	send(a, tea.KeyMsg{Type: tea.KeyCtrlR})
	if !strings.Contains(a.detailsView(60), "pw-web-root") {
		t.Fatal("ctrl+r must reveal the password")
	}
	a.list.inner.Select(0) // a folder
	a.list.inner.Select(len(a.list.inner.Items()) - 1)
	send(a, tea.KeyMsg{Type: tea.KeyCtrlR})
	send(a, tea.KeyMsg{Type: tea.KeyCtrlR})
	if !strings.Contains(a.detailsView(60), "pw-web-root") {
		t.Fatal("toggling twice on the same row reveals again")
	}
}

func TestStatusExpires(t *testing.T) {
	a, _ := searchFixture(t)
	a.setOK("Saved: x")
	a.statusExpireAt = time.Now().Add(-time.Second)
	a.Update(tickMsg(time.Now()))
	if a.statusMsg != "" {
		t.Fatal("an expired status must disappear on the next tick")
	}
}

func TestOverlayUsesTerminalSize(t *testing.T) {
	// A background line far wider than the terminal (a clipped footer)
	// must not shift the modal.
	bg := strings.Repeat("x", 300) + "\n" + strings.Repeat("\n", 20)
	out := centerOverlay(bg, "MODAL", 80, 21)
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "MODAL"); i >= 0 && i != (80-5)/2 {
			t.Fatalf("modal at column %d, want %d", i, (80-5)/2)
		}
	}
}
