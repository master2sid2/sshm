package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
)

// ── Shared modal messages ─────────────────────────────────────────────────────

// unlockPurpose is what a successful folder-password prompt goes on to do.
// It travels with the prompt (and its result message), so cancelling the
// prompt can never leave a pending action behind for a later unlock.
type unlockPurpose int

const (
	unlockOpen     unlockPurpose = iota // step into the folder
	unlockRename                        // rename it
	unlockMove                          // move it elsewhere
	unlockDelete                        // delete it (always asks, cache or not)
	unlockPassword                      // change or remove its password (always asks)
)

func (p unlockPurpose) verb() string {
	switch p {
	case unlockRename:
		return "rename"
	case unlockMove:
		return "move"
	case unlockDelete:
		return "delete"
	case unlockPassword:
		return "change password"
	default:
		return "open"
	}
}

type unlockOKMsg struct {
	group    config.Group
	purpose  unlockPurpose
	password string
}

type unlockCancelMsg struct{}

// passMgrDoneMsg carries the result of the password manager.
// password=="" means remove the password.
type passMgrDoneMsg struct {
	groupID  string
	password string
}

type passMgrCancelMsg struct{}

// ── unlockModel ───────────────────────────────────────────────────────────────

type unlockModel struct {
	group   config.Group
	purpose unlockPurpose
	inp     textinput.Model
	err     string
	width   int
	height  int
}

func newUnlockModel(g config.Group, purpose unlockPurpose) unlockModel {
	t := textinput.New()
	t.Placeholder = "password"
	t.EchoMode = textinput.EchoPassword
	t.CharLimit = 256
	t.PromptStyle = lipgloss.NewStyle().Foreground(clrAccent)
	t.TextStyle = lipgloss.NewStyle().Foreground(clrText)
	t.Focus()
	return unlockModel{group: g, purpose: purpose, inp: t}
}

func (m unlockModel) Init() tea.Cmd { return textinput.Blink }

func (m unlockModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return unlockCancelMsg{} }
		case "enter", "ctrl+s":
			return m, func() tea.Msg {
				return unlockOKMsg{group: m.group, purpose: m.purpose, password: m.inp.Value()}
			}
		}
	}
	var cmd tea.Cmd
	m.inp, cmd = m.inp.Update(msg)
	return m, cmd
}

func (m unlockModel) View() string {
	return m.view()
}

func (m unlockModel) view() string {
	const panelW = 38 // text area = 32
	var sb strings.Builder

	bar := frmBarStyle.Render("│")
	lbl := frmFocusLabelStyle.Render("Password")
	m.inp.Width = 20
	fmt.Fprintf(&sb, "  %s %s %s\n", lbl, bar, m.inp.View())

	if m.err != "" {
		sb.WriteString("\n" + frmErrStyle.Render("  ✗ "+m.err) + "\n")
	}

	sb.WriteString("\n" + frmSepStyle.Render(strings.Repeat("─", panelW-6)) + "\n")
	sb.WriteString(frmHelpStyle.Render("  enter confirm   esc cancel"))

	return renderModal("🔒", m.group.Name+" — "+m.purpose.verb(), panelW, sb.String())
}

// ── passMgrModel ──────────────────────────────────────────────────────────────

type passMgrModel struct {
	groupID    string
	groupName  string
	hasPass    bool
	newInp     textinput.Model
	confirmInp textinput.Model
	focused    int // 0=new, 1=confirm
	err        string
	width      int
	height     int
}

func newPassMgrModel(g config.Group) passMgrModel {
	mkInp := func(ph string) textinput.Model {
		t := textinput.New()
		t.Placeholder = ph
		t.EchoMode = textinput.EchoPassword
		t.CharLimit = 256
		t.PromptStyle = lipgloss.NewStyle().Foreground(clrAccent)
		t.TextStyle = lipgloss.NewStyle().Foreground(clrText)
		return t
	}
	m := passMgrModel{
		groupID:    g.ID,
		groupName:  g.Name,
		hasPass:    g.PasswordHash != "",
		newInp:     mkInp("new password"),
		confirmInp: mkInp("confirm password"),
		focused:    0,
	}
	m.applyFocus()
	return m
}

func (m *passMgrModel) applyFocus() {
	m.newInp.Blur()
	m.confirmInp.Blur()
	if m.focused == 0 {
		m.newInp.Focus()
	} else {
		m.confirmInp.Focus()
	}
}

func (m passMgrModel) Init() tea.Cmd { return textinput.Blink }

func (m passMgrModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return passMgrCancelMsg{} }

		case "tab", "shift+tab":
			if m.focused == 0 {
				m.focused = 1
			} else {
				m.focused = 0
			}
			m.applyFocus()
			return m, textinput.Blink

		case "enter":
			if m.focused == 0 {
				// Move to next field.
				m.focused = 1
				m.applyFocus()
				return m, textinput.Blink
			}
			// focused == 1: validate and submit.
			return m.submit()

		case "ctrl+s":
			return m.submit()
		}
	}

	var cmd tea.Cmd
	if m.focused == 0 {
		m.newInp, cmd = m.newInp.Update(msg)
	} else {
		m.confirmInp, cmd = m.confirmInp.Update(msg)
	}
	return m, cmd
}

func (m passMgrModel) submit() (tea.Model, tea.Cmd) {
	newVal := m.newInp.Value()
	confVal := m.confirmInp.Value()
	m.err = ""

	if newVal == "" && confVal == "" {
		// Remove password.
		id := m.groupID
		return m, func() tea.Msg { return passMgrDoneMsg{groupID: id, password: ""} }
	}
	if newVal == "" || confVal == "" {
		m.err = "passwords must match"
		return m, nil
	}
	if newVal != confVal {
		m.err = "passwords must match"
		return m, nil
	}
	id := m.groupID
	pw := newVal
	return m, func() tea.Msg { return passMgrDoneMsg{groupID: id, password: pw} }
}

func (m passMgrModel) View() string {
	return m.view()
}

func (m passMgrModel) view() string {
	title := "Set password"
	if m.hasPass {
		title = "Change password"
	}
	const panelW = 42 // text area = 36
	var sb strings.Builder

	// New password field.
	newBar := frmDimStyle.Render("│")
	newLbl := frmLabelStyle.Render("New password")
	if m.focused == 0 {
		newBar = frmBarStyle.Render("│")
		newLbl = frmFocusLabelStyle.Render("New password")
	}
	m.newInp.Width = 22
	fmt.Fprintf(&sb, "  %s %s %s\n", newLbl, newBar, m.newInp.View())

	// Confirm field.
	confBar := frmDimStyle.Render("│")
	confLbl := frmLabelStyle.Render("Confirm")
	if m.focused == 1 {
		confBar = frmBarStyle.Render("│")
		confLbl = frmFocusLabelStyle.Render("Confirm")
	}
	m.confirmInp.Width = 22
	fmt.Fprintf(&sb, "  %s %s %s\n", confLbl, confBar, m.confirmInp.View())

	if m.hasPass {
		sb.WriteString("\n" + frmDimStyle.Render("  Leave blank to remove password") + "\n")
	}

	if m.err != "" {
		sb.WriteString("\n" + frmErrStyle.Render("  ✗ "+m.err) + "\n")
	}

	sb.WriteString("\n" + frmSepStyle.Render(strings.Repeat("─", panelW-6)) + "\n")
	sb.WriteString(frmHelpStyle.Render("  tab navigate   ctrl+s save   esc cancel"))

	return renderModal("◈", title+" — "+m.groupName, panelW, sb.String())
}
