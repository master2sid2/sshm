package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
	sshclient "github.com/master2sid2/sshm/internal/ssh"
	"github.com/master2sid2/sshm/internal/termutil"
)

type modalKind int

const (
	noModal modalKind = iota
	modalUnlock
	modalPassMgr
	modalHelp
)

type view int

const (
	viewList view = iota
	viewForm
	viewGroupForm
	viewMove
	viewConfirmDelete
)

// How long a transient status (saved, deleted, session closed, ...) stays
// in the header. Errors stay longer: they usually need reading.
const (
	statusDisplayTime = 4 * time.Second
	errorDisplayTime  = 10 * time.Second
)

// tickMsg drives the expiry of the header status once a second.
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type connectMsg struct{ profile config.Profile }

type App struct {
	store       *config.Store
	list        listModel
	form        formModel
	groupForm   groupFormModel
	movePicker  movePickerModel
	currentView view

	// Navigation stack — each entry is the group we stepped into.
	groupStack []config.Group

	// Session-level unlock cache: groups unlocked during this run.
	unlockedGroups map[string]struct{}

	// Search: typing (filtering) or applied (filterQuery != "").
	filtering   bool
	filterInput textinput.Model
	filterQuery string

	sortMode sortMode
	sortDesc bool

	// revealID is the profile whose secrets the details panel shows
	// (ctrl+r); moving to another row hides them again.
	revealID string

	// Delete state.
	deleteTarget     string
	deleteIsGroup    bool
	deleteTargetName string

	// Modal state.
	activeModal modalKind
	unlockMdl   unlockModel
	passMgrMdl  passMgrModel

	width          int
	height         int
	statusMsg      string
	statusIsErr    bool
	statusExpireAt time.Time // zero = stays until replaced

	PendingConnect *config.Profile
}

func New(store *config.Store, statusMsg string, statusIsErr bool) *App {
	fi := textinput.New()
	fi.Placeholder = "name, host, user or folder…"
	fi.Prompt = "🔍 "
	fi.PromptStyle = lipgloss.NewStyle().Foreground(clrFocus)
	fi.TextStyle = lipgloss.NewStyle().Foreground(clrText)
	fi.CharLimit = 128

	a := &App{
		store:          store,
		unlockedGroups: make(map[string]struct{}),
		filterInput:    fi,
	}
	if statusMsg != "" {
		a.setStatus(statusMsg, statusIsErr)
	}
	a.list = newListModel()
	a.refreshList()
	return a
}

// currentGroupID returns the ID of the group we are currently inside ("" = root).
func (a *App) currentGroupID() string {
	if len(a.groupStack) == 0 {
		return ""
	}
	return a.groupStack[len(a.groupStack)-1].ID
}

func (a *App) unlocked(g config.Group) bool {
	_, ok := a.unlockedGroups[g.ID]
	return !g.Locked() || ok
}

// ── Entry assembly ────────────────────────────────────────────────────────────

// refreshList rebuilds the rows: the current folder, or search results.
func (a *App) refreshList() {
	if a.filterQuery != "" {
		a.list.setEntries(a.searchEntries(a.filterQuery))
		return
	}
	gid := a.currentGroupID()
	var entries []listEntry
	for _, g := range a.store.GroupsInGroup(gid) {
		_, su := a.unlockedGroups[g.ID]
		entries = append(entries, listEntry{isGroup: true, group: g, sessionUnlocked: su})
	}
	profiles := a.store.ProfilesInGroup(gid)
	sortProfiles(profiles, a.sortMode, a.sortDesc)
	for _, p := range profiles {
		entries = append(entries, listEntry{profile: p})
	}
	a.list.setEntries(entries)
}

// searchEntries returns the profiles in the current folder and every
// subfolder the user may open — locked folders not unlocked this session are
// not searched — whose name, host, user or folder path contains query
// (case-insensitive). Each row shows its folder path.
func (a *App) searchEntries(query string) []listEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	type hit struct {
		p    config.Profile
		path string
	}
	var hits []hit
	var walk func(groupID string, path []string)
	walk = func(groupID string, path []string) {
		label := strings.Join(path, " / ")
		for _, p := range a.store.ProfilesInGroup(groupID) {
			hay := strings.ToLower(strings.Join([]string{p.Name, p.Host, p.User, label}, " "))
			if strings.Contains(hay, q) {
				hits = append(hits, hit{p, label})
			}
		}
		for _, g := range a.store.GroupsInGroup(groupID) {
			if a.unlocked(g) {
				walk(g.ID, append(path[:len(path):len(path)], g.Name))
			}
		}
	}
	walk(a.currentGroupID(), nil)

	ps := make([]config.Profile, len(hits))
	paths := make(map[string]string, len(hits))
	for i, h := range hits {
		ps[i] = h.p
		paths[h.p.ID] = h.path
	}
	sortProfiles(ps, a.sortMode, a.sortDesc)
	entries := make([]listEntry, len(ps))
	for i, p := range ps {
		entries[i] = listEntry{profile: p, path: paths[p.ID]}
	}
	return entries
}

// pathLabel renders a folder's ancestry as "prod / eu / db".
func (a *App) pathLabel(groupID string) string {
	var names []string
	for _, g := range a.store.GroupPath(groupID) {
		names = append(names, g.Name)
	}
	return strings.Join(names, " / ")
}

// ── Update ────────────────────────────────────────────────────────────────────

func (a *App) Init() tea.Cmd { return tickCmd() }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		a.width, a.height = wsm.Width, wsm.Height
		a.recomputeListSize()
		a.form.width = wsm.Width
		a.groupForm.width = wsm.Width
		a.movePicker.width = wsm.Width
		a.movePicker.height = wsm.Height
		a.unlockMdl.width, a.unlockMdl.height = wsm.Width, wsm.Height
		a.passMgrMdl.width, a.passMgrMdl.height = wsm.Width, wsm.Height
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		return a, tea.Quit
	}

	if _, ok := msg.(tickMsg); ok {
		if !a.statusExpireAt.IsZero() && !time.Now().Before(a.statusExpireAt) {
			a.statusMsg, a.statusExpireAt = "", time.Time{}
		}
		return a, tickCmd()
	}

	// Handle modal result messages first.
	switch msg := msg.(type) {
	case unlockOKMsg:
		if !msg.group.CheckPassword(msg.password) {
			a.unlockMdl.err = "Wrong password"
			return a, textinput.Blink
		}
		a.activeModal = noModal
		a.unlockedGroups[msg.group.ID] = struct{}{}
		return a, a.groupAction(msg.group, msg.purpose)
	case unlockCancelMsg:
		a.activeModal = noModal
		return a, nil
	case passMgrDoneMsg:
		var err error
		if msg.password == "" {
			if err = a.store.RemoveGroupPassword(msg.groupID); err == nil {
				a.setOK("Password removed")
			}
		} else {
			if err = a.store.SetGroupPassword(msg.groupID, msg.password); err == nil {
				a.setOK("Password set")
				// Whoever just set it knows it: keep the folder open for
				// this session rather than locking them out at once.
				a.unlockedGroups[msg.groupID] = struct{}{}
			}
		}
		if err != nil {
			a.setErr(err.Error())
		}
		a.activeModal = noModal
		a.refreshList()
		return a, nil
	case passMgrCancelMsg:
		a.activeModal = noModal
		return a, nil
	}

	// Route to active modal if one is open.
	if a.activeModal != noModal {
		return a.updateModal(msg)
	}

	switch msg := msg.(type) {
	case connectMsg:
		p := msg.profile
		a.PendingConnect = &p
		return a, tea.Quit

	case formSavedMsg:
		var err error
		if a.form.isEdit {
			err = a.store.Update(msg.profile)
		} else {
			err = a.store.Add(msg.profile)
		}
		if err != nil {
			a.setErr("Save error: " + err.Error())
		} else {
			a.setOK("Saved: " + msg.profile.Name)
		}
		a.refreshList()
		a.currentView = viewList
		return a, nil

	case formCancelledMsg:
		a.currentView = viewList
		return a, nil

	case groupSavedMsg:
		var err error
		if a.groupForm.isEdit {
			err = a.store.UpdateGroup(msg.group)
		} else {
			err = a.store.AddGroup(msg.group)
		}
		if err != nil {
			a.setErr("Save error: " + err.Error())
		} else {
			a.setOK("Saved: " + msg.group.Name)
		}
		a.refreshList()
		a.currentView = viewList
		return a, nil

	case groupCancelledMsg:
		a.currentView = viewList
		return a, nil

	case moveDoneMsg:
		var err error
		if msg.isGroup {
			err = a.store.MoveGroup(msg.id, msg.targetGroupID)
		} else {
			err = a.store.MoveProfile(msg.id, msg.targetGroupID)
		}
		if err != nil {
			a.setErr("Move error: " + err.Error())
		} else {
			dest := "root"
			if msg.targetGroupID != "" {
				dest = a.pathLabel(msg.targetGroupID)
			}
			a.setOK("Moved to " + dest)
		}
		a.refreshList()
		a.currentView = viewList
		return a, nil

	case moveCancelledMsg:
		a.currentView = viewList
		return a, nil
	}

	switch a.currentView {
	case viewList:
		if key, ok := msg.(tea.KeyMsg); ok {
			return a.updateList(key)
		}
		return a, nil
	case viewForm:
		newModel, cmd := a.form.Update(msg)
		if fm, ok := newModel.(formModel); ok {
			a.form = fm
		}
		return a, cmd
	case viewGroupForm:
		newModel, cmd := a.groupForm.Update(msg)
		if gf, ok := newModel.(groupFormModel); ok {
			a.groupForm = gf
		}
		return a, cmd
	case viewMove:
		newModel, cmd := a.movePicker.Update(msg)
		if mp, ok := newModel.(movePickerModel); ok {
			a.movePicker = mp
		}
		return a, cmd
	case viewConfirmDelete:
		if key, ok := msg.(tea.KeyMsg); ok {
			return a.updateConfirmDelete(key)
		}
	}
	return a, nil
}

func (a *App) setOK(msg string)  { a.setStatus(msg, false) }
func (a *App) setErr(msg string) { a.setStatus(msg, true) }

func (a *App) setStatus(msg string, isErr bool) {
	a.statusMsg, a.statusIsErr = msg, isErr
	d := statusDisplayTime
	if isErr {
		d = errorDisplayTime
	}
	a.statusExpireAt = time.Now().Add(d)
}

func (a *App) recomputeListSize() {
	h := a.height - headerH - footerH - 2
	if a.filtering || a.filterQuery != "" {
		h-- // the search line above the list
	}
	a.list.setSize(a.width/2-2, max(h, 1))
}

// ── List view key handling ───────────────────────────────────────────────────

func (a *App) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.filtering {
		return a.updateFiltering(msg)
	}

	switch msg.String() {
	case "q":
		return a, tea.Quit

	case "esc":
		switch {
		case a.filterQuery != "":
			a.clearSearch()
		case len(a.groupStack) > 0:
			a.groupStack = a.groupStack[:len(a.groupStack)-1]
			a.refreshList()
		}
		return a, nil

	case "/":
		a.filtering = true
		a.filterInput.SetValue(a.filterQuery)
		a.filterInput.CursorEnd()
		a.recomputeListSize()
		return a, a.filterInput.Focus()

	case "enter":
		e, ok := a.list.selectedEntry()
		if !ok {
			return a, nil
		}
		if e.isGroup {
			return a, a.withUnlock(e.group, unlockOpen)
		}
		return a, connect(e.profile)

	case "ctrl+r":
		if e, ok := a.list.selectedEntry(); ok && !e.isGroup && a.revealID != e.profile.ID {
			a.revealID = e.profile.ID
		} else {
			a.revealID = ""
		}
		return a, nil

	case "s":
		a.sortMode = (a.sortMode + 1) % sortModeCount
		a.refreshList()
		a.setOK("Sorted by " + a.sortMode.label())
		return a, nil

	case "S":
		a.sortDesc = !a.sortDesc
		a.refreshList()
		return a, nil

	case "n":
		p := config.NewProfile()
		p.GroupID = a.currentGroupID()
		a.form = newFormModel(p, false)
		a.form.width = a.width
		a.currentView = viewForm
		return a, a.form.Init()

	case "g":
		g := config.NewGroup(a.currentGroupID())
		a.groupForm = newGroupFormModel(g, false)
		a.groupForm.width = a.width
		a.currentView = viewGroupForm
		return a, a.groupForm.Init()

	case "e":
		e, ok := a.list.selectedEntry()
		if !ok {
			return a, nil
		}
		if e.isGroup {
			return a, a.withUnlock(e.group, unlockRename)
		}
		a.form = newFormModel(e.profile, true)
		a.form.width = a.width
		a.currentView = viewForm
		return a, a.form.Init()

	case "m":
		e, ok := a.list.selectedEntry()
		if !ok {
			return a, nil
		}
		if e.isGroup {
			return a, a.withUnlock(e.group, unlockMove)
		}
		a.openMovePicker(e.profile.ID, false, e.profile.Name)
		return a, nil

	case "d":
		e, ok := a.list.selectedEntry()
		if !ok {
			return a, nil
		}
		if e.isGroup {
			return a, a.withUnlock(e.group, unlockDelete)
		}
		a.deleteTarget, a.deleteTargetName, a.deleteIsGroup = e.profile.ID, e.profile.Name, false
		a.currentView = viewConfirmDelete
		return a, nil

	case "p":
		e, ok := a.list.selectedEntry()
		if !ok || !e.isGroup {
			return a, nil
		}
		return a, a.withUnlock(e.group, unlockPassword)

	case "?":
		a.activeModal = modalHelp
		return a, nil
	}
	return a, a.list.update(msg)
}

// updateFiltering handles keys while the search line has focus.
func (a *App) updateFiltering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.clearSearch()
		return a, nil

	case "enter":
		// Keep the results, give the keys back to the list; enter on a
		// host connects right away.
		a.filtering = false
		a.filterInput.Blur()
		if a.filterQuery == "" {
			a.recomputeListSize()
		}
		if e, ok := a.list.selectedEntry(); ok && !e.isGroup {
			return a, connect(e.profile)
		}
		return a, nil

	case "up", "down", "pgup", "pgdown", "ctrl+p", "ctrl+n":
		switch msg.String() {
		case "ctrl+p":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "ctrl+n":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		}
		return a, a.list.update(msg)

	default:
		var cmd tea.Cmd
		a.filterInput, cmd = a.filterInput.Update(msg)
		if q := a.filterInput.Value(); q != a.filterQuery {
			a.filterQuery = q
			a.refreshList()
		}
		return a, cmd
	}
}

func (a *App) clearSearch() {
	a.filtering = false
	a.filterQuery = ""
	a.filterInput.SetValue("")
	a.filterInput.Blur()
	a.recomputeListSize()
	a.refreshList()
}

func connect(p config.Profile) tea.Cmd {
	return func() tea.Msg { return connectMsg{p} }
}

// withUnlock runs purpose on g, first asking for the folder password when g
// is protected. Opening, renaming and moving accept a folder already
// unlocked in this session; deleting it or changing its password always
// re-asks, so a folder left open on an unattended screen cannot be wiped
// or have its password replaced.
func (a *App) withUnlock(g config.Group, purpose unlockPurpose) tea.Cmd {
	always := purpose == unlockDelete || purpose == unlockPassword
	if g.Locked() && (always || !a.unlocked(g)) {
		a.unlockMdl = newUnlockModel(g, purpose)
		a.unlockMdl.width, a.unlockMdl.height = a.width, a.height
		a.activeModal = modalUnlock
		return textinput.Blink
	}
	return a.groupAction(g, purpose)
}

// groupAction performs purpose on g once any folder password has been checked.
func (a *App) groupAction(g config.Group, purpose unlockPurpose) tea.Cmd {
	switch purpose {
	case unlockRename:
		a.groupForm = newGroupFormModel(g, true)
		a.groupForm.width = a.width
		a.currentView = viewGroupForm
		return a.groupForm.Init()
	case unlockMove:
		a.openMovePicker(g.ID, true, g.Name)
		return nil
	case unlockDelete:
		a.deleteTarget, a.deleteTargetName, a.deleteIsGroup = g.ID, g.Name, true
		a.currentView = viewConfirmDelete
		return nil
	case unlockPassword:
		a.passMgrMdl = newPassMgrModel(g)
		a.passMgrMdl.width, a.passMgrMdl.height = a.width, a.height
		a.activeModal = modalPassMgr
		return textinput.Blink
	default: // unlockOpen
		a.groupStack = append(a.groupStack, g)
		a.clearSearch()
		return nil
	}
}

func (a *App) openMovePicker(id string, isGroup bool, name string) {
	a.movePicker = newMovePicker(a.store, id, isGroup, name, a.unlockedGroups)
	a.movePicker.width = a.width
	a.movePicker.height = a.height
	a.currentView = viewMove
}

func (a *App) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		var err error
		if a.deleteIsGroup {
			err = a.store.DeleteGroup(a.deleteTarget)
		} else {
			err = a.store.Delete(a.deleteTarget)
		}
		if err != nil {
			a.setErr("Delete error: " + err.Error())
		} else {
			a.setOK("Deleted: " + a.deleteTargetName)
		}
		a.refreshList()
		a.currentView = viewList
	case "n", "N", "esc":
		a.currentView = viewList
	}
	return a, nil
}

func (a *App) updateModal(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch a.activeModal {
	case modalUnlock:
		newMdl, cmd := a.unlockMdl.Update(msg)
		if um, ok := newMdl.(unlockModel); ok {
			a.unlockMdl = um
		}
		return a, cmd
	case modalPassMgr:
		newMdl, cmd := a.passMgrMdl.Update(msg)
		if pm, ok := newMdl.(passMgrModel); ok {
			a.passMgrMdl = pm
		}
		return a, cmd
	case modalHelp:
		if _, ok := msg.(tea.KeyMsg); ok {
			a.activeModal = noModal
		}
		return a, nil
	}
	return a, nil
}

// ── Run ───────────────────────────────────────────────────────────────────────

// Run is the main event loop: TUI → connect → TUI → …
//
// The same App lives across SSH sessions, so after disconnecting the user is
// back exactly where they were: same folder, same cursor, same search, and
// folders unlocked earlier in the run stay unlocked.
func Run(ctx context.Context, store *config.Store) error {
	app := New(store, "", false)
	for {
		p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithContext(ctx))
		if _, err := p.Run(); err != nil {
			if ctx.Err() != nil {
				return nil // SIGTERM/SIGHUP: leave quietly
			}
			return fmt.Errorf("tui: %w", err)
		}
		if app.PendingConnect == nil {
			return nil
		}
		profile := *app.PendingConnect
		app.PendingConnect = nil

		res, err := connectSSH(ctx, profile)
		if hk, ok := errors.AsType[*sshclient.HostKeyError](err); ok {
			// Too important for a status line that fades: show the whole
			// warning and wait until it has been read.
			fmt.Fprint(os.Stderr, "\n"+hk.Details()+"\n")
			_, _ = termutil.ReadLine("Press enter to return to sshm ")
		}
		switch {
		case errors.Is(err, sshclient.ErrCancelled):
			app.setOK("Connection cancelled: " + profile.Name)
		case errors.Is(err, sshclient.ErrHostKeyRejected):
			app.setErr("Host key not accepted: " + profile.Name)
		case err != nil:
			app.setErr(fmt.Sprintf("Connection failed: %s", err))
		case len(res.TunnelErrors) > 0:
			app.setErr(fmt.Sprintf("Session closed: %s — tunnel failed: %v", profile.Name, res.TunnelErrors[0]))
		default:
			app.setOK("Session closed: " + profile.Name)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}
