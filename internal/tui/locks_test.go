package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/master2sid2/sshm/internal/config"
)

// newTestApp returns an App over a fresh vault holding the given root folders,
// each protected by a password equal to its name.
func newTestApp(t *testing.T, names ...string) (*App, *config.Store, []config.Group) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "vault.enc"), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	var groups []config.Group
	for _, n := range names {
		g := config.NewGroup("")
		g.Name = n
		if err := store.AddGroup(g); err != nil {
			t.Fatal(err)
		}
		if err := store.SetGroupPassword(g.ID, n); err != nil {
			t.Fatal(err)
		}
		g, _ = store.GetGroup(g.ID)
		groups = append(groups, g)
	}
	a := New(store, "", false)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return a, store, groups
}

// send feeds msg to the app and then every message its commands produce,
// the way the bubbletea runtime would (textinput blink ticks are dropped).
func send(a *App, msg tea.Msg) {
	queue := []tea.Msg{msg}
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		_, cmd := a.Update(m)
		for _, out := range execCmds(cmd) {
			switch out.(type) {
			case unlockOKMsg, unlockCancelMsg, passMgrDoneMsg, passMgrCancelMsg,
				groupSavedMsg, groupCancelledMsg, moveDoneMsg, moveCancelledMsg:
				queue = append(queue, out)
			}
		}
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(a *App, text string) {
	for _, r := range text {
		send(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func selectGroup(t *testing.T, a *App, id string) {
	t.Helper()
	for i, it := range a.list.inner.Items() {
		if e := it.(listEntry); e.isGroup && e.group.ID == id {
			a.list.inner.Select(i)
			return
		}
	}
	t.Fatalf("group %s not in list", id)
}

// Cancelling a delete prompt used to leave a flag behind: unlocking any other
// folder afterwards opened "Delete <first folder>?" without its password.
func TestCancelledDeleteUnlockDoesNotCarryOver(t *testing.T) {
	a, _, g := newTestApp(t, "alpha", "beta")

	selectGroup(t, a, g[0].ID)
	send(a, key("d"))
	if a.activeModal != modalUnlock {
		t.Fatal("delete of a locked folder must ask for its password")
	}
	send(a, key("esc"))

	selectGroup(t, a, g[1].ID)
	send(a, key("enter"))
	typeText(a, "beta")
	send(a, key("enter"))

	if a.currentView == viewConfirmDelete {
		t.Fatalf("unlocking beta opened a delete confirmation for %q", a.deleteTargetName)
	}
	if a.currentGroupID() != g[1].ID {
		t.Fatalf("expected to be inside beta, stack=%v", a.groupStack)
	}
}

func TestPasswordChangeRequiresCurrentPassword(t *testing.T) {
	a, store, g := newTestApp(t, "alpha")

	// Unlocked for the session: still must re-prove the password.
	a.unlockedGroups[g[0].ID] = struct{}{}
	selectGroup(t, a, g[0].ID)
	send(a, key("p"))
	if a.activeModal != modalUnlock {
		t.Fatal("changing a folder password must ask for the current one")
	}
	typeText(a, "wrong")
	send(a, key("enter"))
	if a.activeModal != modalUnlock || a.unlockMdl.err == "" {
		t.Fatal("a wrong password must keep the prompt open with an error")
	}
	send(a, key("esc"))
	if got, _ := store.GetGroup(g[0].ID); !got.Locked() {
		t.Fatal("password must still be set")
	}

	selectGroup(t, a, g[0].ID)
	send(a, key("p"))
	typeText(a, "alpha")
	send(a, key("enter"))
	if a.activeModal != modalPassMgr {
		t.Fatalf("expected the password manager after the right password, got modal %d", a.activeModal)
	}
	send(a, tea.KeyMsg{Type: tea.KeyCtrlS}) // both fields blank = remove
	if got, _ := store.GetGroup(g[0].ID); got.Locked() {
		t.Fatal("blank fields after a correct unlock should remove the password")
	}
}

func TestRenameAndMoveOfLockedFolderAskUnlessUnlocked(t *testing.T) {
	a, _, g := newTestApp(t, "alpha")
	for _, k := range []string{"e", "m"} {
		selectGroup(t, a, g[0].ID)
		send(a, key(k))
		if a.activeModal != modalUnlock {
			t.Fatalf("%q on a locked folder must ask for its password", k)
		}
		send(a, key("esc"))
	}

	a.unlockedGroups[g[0].ID] = struct{}{}
	selectGroup(t, a, g[0].ID)
	send(a, key("e"))
	if a.activeModal != noModal || a.currentView != viewGroupForm {
		t.Fatal("a folder unlocked this session can be renamed without asking again")
	}
}

func TestDeleteAlwaysAsksEvenWhenUnlocked(t *testing.T) {
	a, store, g := newTestApp(t, "alpha")
	a.unlockedGroups[g[0].ID] = struct{}{}
	selectGroup(t, a, g[0].ID)
	send(a, key("d"))
	if a.activeModal != modalUnlock {
		t.Fatal("deleting a locked folder must always ask")
	}
	typeText(a, "alpha")
	send(a, key("enter"))
	if a.currentView != viewConfirmDelete || a.deleteTarget != g[0].ID {
		t.Fatal("expected the delete confirmation for alpha")
	}
	send(a, key("y"))
	if _, ok := store.GetGroup(g[0].ID); ok {
		t.Fatal("folder should be deleted")
	}
}
