package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
)

type formSavedMsg struct{ profile config.Profile }
type formCancelledMsg struct{}

const (
	fldName     = 0
	fldHost     = 1
	fldPort     = 2
	fldUser     = 3
	fldAuthType = 4
	fldPassword = 5
	fldKeyPath  = 6
	fldKeyPass  = 7
	fldTunnels  = 8
)

const (
	authAgent    = 0
	authPassword = 1
	authKey      = 2
)

var authLabels = []string{"agent", "password", "key"}

// detectDefaultSSHKey returns the first default private key found in ~/.ssh.
func detectDefaultSSHKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

type formModel struct {
	origProfile config.Profile

	nameInp  textinput.Model
	hostInp  textinput.Model
	portInp  textinput.Model
	userInp  textinput.Model
	passInp  textinput.Model
	keyInp   textinput.Model
	kpassInp textinput.Model

	authIdx    int
	isEdit     bool
	focusField int
	tunCursor  int
	tunnels    []config.Tunnel
	tunEditor  *tunnelEditorModel
	err        string
	width      int
}

func newFormModel(p config.Profile, isEdit bool) formModel {
	mkInput := func(placeholder string, pw bool) textinput.Model {
		t := textinput.New()
		t.Placeholder = placeholder
		t.CharLimit = 256
		t.PromptStyle = lipgloss.NewStyle().Foreground(clrAccent)
		t.TextStyle = lipgloss.NewStyle().Foreground(clrText)
		if pw {
			t.EchoMode = textinput.EchoPassword
		}
		return t
	}

	authIdx := authAgent
	switch p.AuthType {
	case config.AuthPassword:
		authIdx = authPassword
	case config.AuthKey:
		authIdx = authKey
	}

	port := p.Port
	if port == 0 {
		port = 22
	}

	m := formModel{
		origProfile: p,
		isEdit:      isEdit,
		nameInp:     mkInput("profile name", false),
		hostInp:     mkInput("hostname or IP", false),
		portInp:     mkInput("22", false),
		userInp:     mkInput("username", false),
		passInp:     mkInput("password", true),
		keyInp:      mkInput("~/.ssh/id_rsa", false),
		kpassInp:    mkInput("passphrase", true),
		authIdx:     authIdx,
		tunnels:     p.Clone().Tunnels,
		focusField:  fldName,
	}
	if m.tunnels == nil {
		m.tunnels = []config.Tunnel{}
	}

	m.nameInp.SetValue(p.Name)
	m.hostInp.SetValue(p.Host)
	m.portInp.SetValue(strconv.Itoa(port))
	m.userInp.SetValue(p.User)
	m.passInp.SetValue(p.Password)
	keyPath := p.KeyPath
	if authIdx == authKey && keyPath == "" {
		keyPath = detectDefaultSSHKey()
	}
	m.keyInp.SetValue(keyPath)
	m.kpassInp.SetValue(p.KeyPassword)

	m.applyFocus()
	return m
}

// ── Focus helpers ─────────────────────────────────────────────────────────────

func (m *formModel) visibleFields() []int {
	base := []int{fldName, fldHost, fldPort, fldUser, fldAuthType}
	switch m.authIdx {
	case authPassword:
		base = append(base, fldPassword)
	case authKey:
		base = append(base, fldKeyPath, fldKeyPass)
	}
	return append(base, fldTunnels)
}

func (m *formModel) nextField() {
	vf := m.visibleFields()
	for i, f := range vf {
		if f == m.focusField {
			m.setFocus(vf[(i+1)%len(vf)])
			return
		}
	}
}

func (m *formModel) prevField() {
	vf := m.visibleFields()
	for i, f := range vf {
		if f == m.focusField {
			m.setFocus(vf[(i-1+len(vf))%len(vf)])
			return
		}
	}
}

func (m *formModel) setFocus(field int) {
	m.focusField = field
	if field == fldTunnels && m.tunCursor > len(m.tunnels) {
		m.tunCursor = 0
	}
	m.applyFocus()
}

func (m *formModel) applyFocus() {
	m.nameInp.Blur()
	m.hostInp.Blur()
	m.portInp.Blur()
	m.userInp.Blur()
	m.passInp.Blur()
	m.keyInp.Blur()
	m.kpassInp.Blur()
	switch m.focusField {
	case fldName:
		m.nameInp.Focus()
	case fldHost:
		m.hostInp.Focus()
	case fldPort:
		m.portInp.Focus()
	case fldUser:
		m.userInp.Focus()
	case fldPassword:
		m.passInp.Focus()
	case fldKeyPath:
		m.keyInp.Focus()
	case fldKeyPass:
		m.kpassInp.Focus()
	}
}

func (m *formModel) ensureAuthVisible() {
	visible := make(map[int]bool)
	for _, f := range m.visibleFields() {
		visible[f] = true
	}
	if !visible[m.focusField] {
		m.nextField()
	}
}

// ── tea.Model ─────────────────────────────────────────────────────────────────

func (m formModel) Init() tea.Cmd { return textinput.Blink }

func (m formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsm.Width
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.tunEditor != nil {
		return m.updateTunnelEditor(key)
	}
	return m.handleKey(key)
}

func (m formModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return formCancelledMsg{} }

	case "ctrl+s":
		return m.submit()

	case "tab", "down":
		if m.focusField == fldTunnels {
			total := len(m.tunnels) + 1
			if m.tunCursor < total-1 {
				m.tunCursor++
			} else {
				m.nextField()
			}
		} else {
			m.nextField()
		}

	case "shift+tab", "up":
		if m.focusField == fldTunnels {
			if m.tunCursor > 0 {
				m.tunCursor--
			} else {
				m.prevField()
			}
		} else {
			m.prevField()
		}

	case "left":
		if m.focusField == fldAuthType && m.authIdx > 0 {
			m.authIdx--
			m.ensureAuthVisible()
		} else if m.focusField != fldAuthType {
			return m.updateInput(key)
		}

	case "right":
		if m.focusField == fldAuthType && m.authIdx < len(authLabels)-1 {
			m.authIdx++
			m.ensureAuthVisible()
			if m.authIdx == authKey && m.keyInp.Value() == "" {
				m.keyInp.SetValue(detectDefaultSSHKey())
			}
		} else if m.focusField != fldAuthType {
			return m.updateInput(key)
		}

	case "enter":
		if m.focusField == fldTunnels {
			if m.tunCursor < len(m.tunnels) {
				return m.openTunnelEditor(m.tunCursor)
			}
			return m.openTunnelEditor(-1)
		}
		m.nextField()

	case "d", "delete":
		if m.focusField == fldTunnels {
			if m.tunCursor < len(m.tunnels) {
				m.tunnels = append(m.tunnels[:m.tunCursor], m.tunnels[m.tunCursor+1:]...)
				if m.tunCursor > 0 && m.tunCursor >= len(m.tunnels) {
					m.tunCursor--
				}
			}
			return m, nil
		}
		// Not in tunnel section — forward to the active text input.
		fallthrough

	default:
		return m.updateInput(key)
	}
	return m, nil
}

// updateInput forwards a key event to whichever text input currently has focus.
func (m formModel) updateInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focusField {
	case fldName:
		m.nameInp, cmd = m.nameInp.Update(key)
	case fldHost:
		m.hostInp, cmd = m.hostInp.Update(key)
	case fldPort:
		m.portInp, cmd = m.portInp.Update(key)
	case fldUser:
		m.userInp, cmd = m.userInp.Update(key)
	case fldPassword:
		m.passInp, cmd = m.passInp.Update(key)
	case fldKeyPath:
		m.keyInp, cmd = m.keyInp.Update(key)
	case fldKeyPass:
		m.kpassInp, cmd = m.kpassInp.Update(key)
	}
	return m, cmd
}

func (m formModel) openTunnelEditor(idx int) (tea.Model, tea.Cmd) {
	var t config.Tunnel
	isNew := idx < 0
	if !isNew {
		t = m.tunnels[idx]
	} else {
		t = config.Tunnel{Direction: config.TunnelLocal, LocalHost: "127.0.0.1", RemoteHost: "127.0.0.1"}
	}
	ed := newTunnelEditor(t, isNew, idx)
	m.tunEditor = &ed
	return m, textinput.Blink
}

func (m formModel) updateTunnelEditor(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.tunEditor = nil
		m.applyFocus()
		return m, textinput.Blink

	case "ctrl+s":
		t, ok := m.tunEditor.getResult()
		if !ok {
			m.tunEditor.err = "invalid port values (1–65535 required)"
			return m, nil
		}
		if m.tunEditor.isNew {
			m.tunnels = append(m.tunnels, t)
			m.tunCursor = len(m.tunnels) - 1
		} else {
			m.tunnels[m.tunEditor.editIdx] = t
		}
		m.tunEditor = nil
		m.focusField = fldTunnels
		m.applyFocus()
		return m, textinput.Blink
	}

	updated, cmd := m.tunEditor.update(key)
	m.tunEditor = &updated
	return m, cmd
}

func (m formModel) submit() (tea.Model, tea.Cmd) {
	m.err = ""
	name := strings.TrimSpace(m.nameInp.Value())
	host := strings.TrimSpace(m.hostInp.Value())
	if name == "" {
		m.err = "name is required"
		m.setFocus(fldName)
		return m, nil
	}
	if host == "" {
		m.err = "host is required"
		m.setFocus(fldHost)
		return m, nil
	}
	port, err := strconv.Atoi(m.portInp.Value())
	if err != nil || port < 1 || port > 65535 {
		m.err = "port must be 1–65535"
		m.setFocus(fldPort)
		return m, nil
	}

	p := m.origProfile
	p.Name = name
	p.Host = host
	p.Port = port
	p.User = strings.TrimSpace(m.userInp.Value())
	switch m.authIdx {
	case authPassword:
		p.AuthType = config.AuthPassword
		p.Password = m.passInp.Value()
		p.KeyPath, p.KeyPassword = "", ""
	case authKey:
		p.AuthType = config.AuthKey
		p.KeyPath = strings.TrimSpace(m.keyInp.Value())
		p.KeyPassword = m.kpassInp.Value()
		p.Password = ""
	default:
		p.AuthType = config.AuthAgent
		p.Password, p.KeyPath, p.KeyPassword = "", "", ""
	}
	p.Tunnels = m.tunnels
	return m, func() tea.Msg { return formSavedMsg{p} }
}

// ── View styles ───────────────────────────────────────────────────────────────

const frmLabelW = 12

var (
	frmLabelStyle = lipgloss.NewStyle().
			Width(frmLabelW).
			Foreground(clrDim)

	frmFocusLabelStyle = lipgloss.NewStyle().
				Width(frmLabelW).
				Foreground(clrFocus).
				Bold(true)

	frmSectionStyle = lipgloss.NewStyle().
			Foreground(clrAccent).
			Bold(true)

	frmSepStyle = lipgloss.NewStyle().Foreground(clrMuted)

	frmErrStyle = lipgloss.NewStyle().
			Foreground(clrError).
			Bold(true)

	frmDimStyle = lipgloss.NewStyle().Foreground(clrDim)

	frmHelpStyle = lipgloss.NewStyle().Foreground(clrDim)

	frmBarStyle = lipgloss.NewStyle().
			Foreground(clrFocus).
			Bold(true)

	radioOnStyle  = lipgloss.NewStyle().Foreground(clrFocus).Bold(true)
	radioOffStyle = lipgloss.NewStyle().Foreground(clrDim)

	tunSelStyle   = lipgloss.NewStyle().Foreground(clrFocus).Bold(true)
	tunRowStyle   = lipgloss.NewStyle().Foreground(clrText)
	tunAddStyle   = lipgloss.NewStyle().Foreground(clrSuccess)
	tunEmptyStyle = lipgloss.NewStyle().Foreground(clrDim).Italic(true)
)

// ── View ──────────────────────────────────────────────────────────────────────

func (m formModel) View() string {
	if m.tunEditor != nil {
		return m.tunEditor.view(m.width)
	}

	innerW := m.width - 10
	if innerW < 40 {
		innerW = 40
	}
	if innerW > 80 {
		innerW = 80
	}
	inputW := innerW - frmLabelW - 8

	var sb strings.Builder

	title := "New Profile"
	if m.isEdit {
		title = "Edit Profile"
	}

	// ── Connection section ────────────────────────────────────────────────────
	sb.WriteString(frmSectionStyle.Render("Connection") + "\n")
	for _, f := range []struct {
		label string
		field int
		inp   *textinput.Model
	}{
		{"Name", fldName, &m.nameInp},
		{"Host", fldHost, &m.hostInp},
		{"Port", fldPort, &m.portInp},
		{"User", fldUser, &m.userInp},
	} {
		f.inp.Width = inputW
		sb.WriteString(m.renderField(f.label, f.field, f.inp))
	}

	// ── Authentication section ────────────────────────────────────────────────
	sb.WriteString("\n" + frmSectionStyle.Render("Authentication") + "\n")

	// Auth type radio row.
	authLbl := frmLabelStyle.Render("Auth type")
	if m.focusField == fldAuthType {
		authLbl = frmFocusLabelStyle.Render("Auth type")
	}
	var radios []string
	for i, name := range authLabels {
		if i == m.authIdx {
			radios = append(radios, radioOnStyle.Render("● "+name))
		} else {
			radios = append(radios, radioOffStyle.Render("○ "+name))
		}
	}
	arrowHint := ""
	if m.focusField == fldAuthType {
		arrowHint = frmDimStyle.Render("  ←/→")
	}
	bar := frmBarStyle.Render("│")
	if m.focusField != fldAuthType {
		bar = frmDimStyle.Render("│")
	}
	fmt.Fprintf(&sb, "  %s %s %s%s\n", authLbl, bar, strings.Join(radios, "  "), arrowHint)

	switch m.authIdx {
	case authPassword:
		m.passInp.Width = inputW
		sb.WriteString(m.renderField("Password", fldPassword, &m.passInp))
	case authKey:
		m.keyInp.Width = inputW
		m.kpassInp.Width = inputW
		sb.WriteString(m.renderField("Key path", fldKeyPath, &m.keyInp))
		sb.WriteString(m.renderField("Passphrase", fldKeyPass, &m.kpassInp))
	}

	// ── Tunnels section ───────────────────────────────────────────────────────
	sb.WriteString("\n" + frmSectionStyle.Render("Tunnels") + "\n")

	inTun := m.focusField == fldTunnels

	if len(m.tunnels) == 0 {
		sb.WriteString(tunEmptyStyle.Render("  No tunnels configured") + "\n")
	}
	for i, t := range m.tunnels {
		selected := inTun && m.tunCursor == i
		prefix := "    "
		row := tunRowStyle
		hint := ""
		if selected {
			prefix = tunSelStyle.Render("  ❯ ")
			row = tunSelStyle
			hint = frmDimStyle.Render("  enter=edit  d=delete")
		}
		line := fmt.Sprintf("%s%-8s  %s:%d  %s  %s:%d%s",
			prefix,
			t.Direction,
			t.LocalHost, t.LocalPort,
			frmDimStyle.Render("→"),
			t.RemoteHost, t.RemotePort,
			hint,
		)
		sb.WriteString(row.Render(line) + "\n")
	}

	// "Add" row.
	if inTun && m.tunCursor == len(m.tunnels) {
		sb.WriteString(tunSelStyle.Render("  ❯ ") + tunAddStyle.Bold(true).Render("[ + Add tunnel ]") + "\n")
	} else {
		sb.WriteString(tunAddStyle.Render("    [ + Add tunnel ]") + "\n")
	}

	// Error line.
	if m.err != "" {
		sb.WriteString("\n" + frmErrStyle.Render("  ✗ "+m.err) + "\n")
	}

	// Footer.
	sb.WriteString("\n" + frmSepStyle.Render(strings.Repeat("─", innerW-6)) + "\n")
	sb.WriteString(frmHelpStyle.Render("  tab/↑↓ navigate   ctrl+s save   esc cancel"))

	return renderModal("◆", title, innerW, sb.String())
}

func (m formModel) renderField(label string, field int, inp *textinput.Model) string {
	focused := m.focusField == field
	lbl := frmLabelStyle.Render(label)
	bar := frmDimStyle.Render("│")
	if focused {
		lbl = frmFocusLabelStyle.Render(label)
		bar = frmBarStyle.Render("│")
	}
	return fmt.Sprintf("  %s %s %s\n", lbl, bar, inp.View())
}
