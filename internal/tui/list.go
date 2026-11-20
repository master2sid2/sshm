package tui

import (
	"cmp"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
)

// ── Sorting ───────────────────────────────────────────────────────────────────

type sortMode int

const (
	sortByName sortMode = iota
	sortByHost
	sortByUpdated
	sortModeCount
)

func (s sortMode) label() string {
	switch s {
	case sortByHost:
		return "host"
	case sortByUpdated:
		return "updated"
	default:
		return "name"
	}
}

// sortProfiles sorts ps in place. Ties keep the vault order (stable sort).
func sortProfiles(ps []config.Profile, mode sortMode, desc bool) {
	slices.SortStableFunc(ps, func(a, b config.Profile) int {
		var c int
		switch mode {
		case sortByHost:
			c = cmp.Or(
				cmp.Compare(strings.ToLower(a.Host), strings.ToLower(b.Host)),
				cmp.Compare(a.Port, b.Port),
				cmp.Compare(strings.ToLower(a.User), strings.ToLower(b.User)),
			)
		case sortByUpdated:
			c = a.UpdatedAt.Compare(b.UpdatedAt)
		default:
			c = cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if desc {
			return -c
		}
		return c
	})
}

// hostPort renders "host:port", with brackets for IPv6, and omits :22.
func hostPort(host string, port int) string {
	if port == 0 || port == 22 {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// connSummary is the second line of a profile row: "user@host:port".
func connSummary(p config.Profile) string {
	hp := hostPort(p.Host, p.Port)
	if p.User == "" {
		return hp
	}
	return p.User + "@" + hp
}

// ── Entries ───────────────────────────────────────────────────────────────────

// listEntry is one row in the main list — either a group/folder or a profile.
type listEntry struct {
	isGroup         bool
	group           config.Group
	profile         config.Profile
	sessionUnlocked bool   // true if group was unlocked this session
	path            string // folder path shown next to a profile in search results
}

func (e listEntry) FilterValue() string { return "" } // search is done by the App

func entryKey(e listEntry) string {
	if e.isGroup {
		return "g:" + e.group.ID
	}
	return "p:" + e.profile.ID
}

// entryDelegate renders groups and profiles with distinct visual styles.
type entryDelegate struct{}

func (d entryDelegate) Height() int                             { return 2 }
func (d entryDelegate) Spacing() int                            { return 1 }
func (d entryDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d entryDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	e, ok := item.(listEntry)
	if !ok {
		return
	}
	width := max(m.Width(), 12)
	selected := index == m.Index()
	if e.isGroup {
		d.renderGroup(w, e.group, width, selected, e.sessionUnlocked)
	} else {
		d.renderProfile(w, e, width, selected)
	}
}

var (
	rowSelStyle    = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("230")).Bold(true).PaddingLeft(1)
	rowSelSubStyle = lipgloss.NewStyle().Background(lipgloss.Color("57")).Foreground(lipgloss.Color("189")).PaddingLeft(1)
)

func (d entryDelegate) renderGroup(w io.Writer, g config.Group, width int, selected bool, sessionUnlocked bool) {
	icon := "▸"
	if g.Locked() {
		icon = "🔒"
		if sessionUnlocked {
			icon = "🔓"
		}
	}

	if selected {
		line1 := rowSelStyle.Width(width).Render("▶  " + icon + " " + g.Name)
		line2 := rowSelSubStyle.Width(width).Render("     folder  ↵ open  esc back")
		fmt.Fprintf(w, "%s\n%s", line1, line2)
		return
	}
	iconColor := lipgloss.Color("214")
	if g.Locked() {
		iconColor = lipgloss.Color("208")
		if sessionUnlocked {
			iconColor = lipgloss.Color("71") // muted green — unlocked
		}
	}
	line1 := lipgloss.NewStyle().Foreground(iconColor).Bold(true).Width(width).PaddingLeft(2).
		Render("   " + icon + " " + g.Name)
	line2 := lipgloss.NewStyle().Foreground(lipgloss.Color("136")).Width(width).PaddingLeft(2).
		Render("     folder")
	fmt.Fprintf(w, "%s\n%s", line1, line2)
}

func (d entryDelegate) renderProfile(w io.Writer, e listEntry, width int, selected bool) {
	p := e.profile
	tunnelBadge := ""
	if n := len(p.Tunnels); n > 0 {
		tunnelBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render(fmt.Sprintf("  ⇄ %d", n))
	}
	sub := fmt.Sprintf("   %s  %s", authIcon(p.AuthType), connSummary(p))
	if e.path != "" {
		sub += "   " + e.path
	}

	if selected {
		line1 := rowSelStyle.Width(width).Render("▶  " + p.Name)
		line2 := rowSelSubStyle.Width(width).Render(sub + tunnelBadge)
		fmt.Fprintf(w, "%s\n%s", line1, line2)
		return
	}
	line1 := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(width).PaddingLeft(2).
		Render("   " + p.Name)
	line2 := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Width(width).PaddingLeft(2).
		Render(sub + tunnelBadge)
	fmt.Fprintf(w, "%s\n%s", line1, line2)
}

func authIcon(a config.AuthType) string {
	switch a {
	case config.AuthPassword:
		return "⊙"
	case config.AuthKey:
		return "⚿"
	default:
		return "⬡"
	}
}

// ── listModel ─────────────────────────────────────────────────────────────────

// listModel wraps bubbles/list. Filtering is done by the App (search spans
// subfolders), so the built-in filter is disabled.
type listModel struct {
	inner list.Model
}

func newListModel() listModel {
	l := list.New(nil, entryDelegate{}, 0, 0)
	l.Title = ""
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.SetShowStatusBar(false)
	return listModel{inner: l}
}

func (m *listModel) setSize(w, h int) { m.inner.SetSize(w, h) }

// setEntries replaces the rows, keeping the cursor on the same item when it
// is still there.
func (m *listModel) setEntries(entries []listEntry) {
	prev := ""
	if e, ok := m.selectedEntry(); ok {
		prev = entryKey(e)
	}
	items := make([]list.Item, len(entries))
	for i, e := range entries {
		items[i] = e
	}
	m.inner.SetItems(items)

	idx := 0
	if prev != "" {
		if i := slices.IndexFunc(entries, func(e listEntry) bool { return entryKey(e) == prev }); i >= 0 {
			idx = i
		}
	}
	m.inner.Select(idx)
}

func (m *listModel) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.inner, cmd = m.inner.Update(msg)
	return cmd
}

func (m *listModel) selectedEntry() (listEntry, bool) {
	e, ok := m.inner.SelectedItem().(listEntry)
	return e, ok
}

func (m *listModel) view() string { return m.inner.View() }
