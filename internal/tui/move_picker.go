package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
)

// moveDoneMsg carries the confirmed move destination.
type moveDoneMsg struct {
	id            string
	isGroup       bool
	targetGroupID string // "" = root
}

// moveCancelledMsg is sent when the user aborts the picker.
type moveCancelledMsg struct{}

// pickerRow is one line in the move picker.
type pickerRow struct {
	isMoveHere bool
	group      config.Group
}

// movePickerModel lets the user navigate the group tree to choose a move target.
type movePickerModel struct {
	store          *config.Store
	movingID       string
	movingIsGroup  bool
	movingName     string
	stack          []config.Group // navigation path inside the picker
	rows           []pickerRow    // current-level rows
	cursor         int
	width          int
	height         int
	unlockedGroups map[string]struct{}
}

func newMovePicker(store *config.Store, id string, isGroup bool, name string, unlocked map[string]struct{}) movePickerModel {
	// Start the picker inside the folder where the item currently lives.
	var parentID string
	if isGroup {
		if g, ok := store.GetGroup(id); ok {
			parentID = g.ParentID
		}
	} else {
		if p, ok := store.Get(id); ok {
			parentID = p.GroupID
		}
	}

	m := movePickerModel{
		store:          store,
		movingID:       id,
		movingIsGroup:  isGroup,
		movingName:     name,
		stack:          store.GroupPath(parentID),
		unlockedGroups: unlocked,
	}
	m.rebuild()
	return m
}

// rebuild recomputes rows for the current stack level.
func (m *movePickerModel) rebuild() {
	parentID := ""
	if len(m.stack) > 0 {
		parentID = m.stack[len(m.stack)-1].ID
	}

	m.rows = []pickerRow{{isMoveHere: true}}

	for _, g := range m.store.GroupsInGroup(parentID) {
		// Skip the item itself and any of its descendants (to avoid cycles).
		if m.movingIsGroup && m.isAncestorOrSelf(g.ID) {
			continue
		}
		m.rows = append(m.rows, pickerRow{group: g})
	}

	// Keep cursor in bounds.
	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
}

// isAncestorOrSelf returns true if groupID equals movingID or is a descendant of it.
func (m *movePickerModel) isAncestorOrSelf(groupID string) bool {
	if groupID == m.movingID {
		return true
	}
	// Walk up from groupID; if we ever hit movingID the target is a descendant.
	cur := groupID
	for {
		g, ok := m.store.GetGroup(cur)
		if !ok || g.ParentID == "" {
			return false
		}
		if g.ParentID == m.movingID {
			return true
		}
		cur = g.ParentID
	}
}

func (m movePickerModel) currentTargetID() string {
	if len(m.stack) == 0 {
		return ""
	}
	return m.stack[len(m.stack)-1].ID
}

// ── tea.Model ─────────────────────────────────────────────────────────────────

func (m movePickerModel) Init() tea.Cmd { return nil }

func (m movePickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wsm.Width, wsm.Height
		return m, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "esc":
		if len(m.stack) > 0 {
			m.stack = m.stack[:len(m.stack)-1]
			m.cursor = 0
			m.rebuild()
		} else {
			return m, func() tea.Msg { return moveCancelledMsg{} }
		}

	case "up", "k", "shift+tab":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j", "tab":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}

	case "enter":
		if len(m.rows) == 0 {
			return m, nil
		}
		row := m.rows[m.cursor]
		if row.isMoveHere {
			tid := m.currentTargetID()
			return m, func() tea.Msg {
				return moveDoneMsg{id: m.movingID, isGroup: m.movingIsGroup, targetGroupID: tid}
			}
		}
		// Locked groups that haven't been unlocked this session cannot be navigated.
		if row.group.PasswordHash != "" {
			if _, ok := m.unlockedGroups[row.group.ID]; !ok {
				return m, nil
			}
		}
		// Drill into the selected group.
		m.stack = append(m.stack, row.group)
		m.cursor = 0
		m.rebuild()
	}

	return m, nil
}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	mpSepStyle = lipgloss.NewStyle().Foreground(clrMuted)
	mpDimStyle = lipgloss.NewStyle().Foreground(clrDim)

	mpMoveHereSelStyle = lipgloss.NewStyle().
				Foreground(clrSuccess).
				Bold(true)
	mpMoveHereStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("34"))

	mpGroupSelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("63")).
			Bold(true)
	mpGroupStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	mpBreadStyle = lipgloss.NewStyle().Foreground(clrDim)
	mpBreadSep   = lipgloss.NewStyle().Foreground(clrMuted).Render("  ›  ")
)

func (m movePickerModel) View() string {
	innerW := m.width - 10
	if innerW < 36 {
		innerW = 36
	}
	if innerW > 68 {
		innerW = 68
	}

	title := fmt.Sprintf("Move %q to…", m.movingName)
	var sb strings.Builder

	// Breadcrumb inside picker.
	if len(m.stack) > 0 {
		var parts []string
		parts = append(parts, mpBreadStyle.Render("root"))
		for _, g := range m.stack {
			parts = append(parts, mpBreadStyle.Render(g.Name))
		}
		sb.WriteString(mpDimStyle.Render("  ") +
			strings.Join(parts, mpBreadSep) + "\n")
	} else {
		sb.WriteString(mpDimStyle.Render("  root") + "\n")
	}
	sb.WriteString("\n")

	// Rows.
	for i, row := range m.rows {
		sel := i == m.cursor
		cursor := "  "
		if sel {
			cursor = lipgloss.NewStyle().Foreground(clrFocus).Bold(true).Render("❯ ")
		}

		if row.isMoveHere {
			label := "[ ✓ Move here ]"
			style := mpMoveHereStyle
			if sel {
				style = mpMoveHereSelStyle
			}
			sb.WriteString(cursor + style.Render(label) + "\n")
		} else {
			_, sessionUnlocked := m.unlockedGroups[row.group.ID]
			needsPassword := row.group.PasswordHash != "" && !sessionUnlocked
			icon := "▸"
			if row.group.PasswordHash != "" {
				if sessionUnlocked {
					icon = "🔓"
				} else {
					icon = "🔒"
				}
			}
			label := icon + " " + row.group.Name
			// cursor(2) + style width must equal text area (innerW-6)
			rowW := innerW - 8
			if needsPassword {
				if sel {
					sb.WriteString(cursor + mpGroupSelStyle.Width(rowW).Render(label) + "\n")
				} else {
					sb.WriteString(cursor + mpDimStyle.Render(label) +
						mpDimStyle.Render("  (locked)") + "\n")
				}
			} else {
				style := mpGroupStyle
				if sel {
					style = mpGroupSelStyle.Width(rowW)
				}
				sb.WriteString(cursor + style.Render(label) + "\n")
			}
		}
	}

	if len(m.rows) == 0 {
		sb.WriteString(mpDimStyle.Render("  (empty folder)") + "\n")
	}

	sb.WriteString("\n" + mpSepStyle.Render(strings.Repeat("─", innerW-6)) + "\n")
	sb.WriteString(mpDimStyle.Render("  ↑↓ navigate   enter open/confirm   esc back / cancel"))

	return renderModal("→", title, innerW, sb.String())
}
