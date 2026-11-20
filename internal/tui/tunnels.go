package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
)

type tunnelEditorModel struct {
	dirIdx     int
	localHost  textinput.Model
	localPort  textinput.Model
	remoteHost textinput.Model
	remotePort textinput.Model
	focused    int
	isNew      bool
	editIdx    int
	err        string
}

const (
	tFldDir        = 0
	tFldLocalHost  = 1
	tFldLocalPort  = 2
	tFldRemoteHost = 3
	tFldRemotePort = 4
	tFldCount      = 5
)

func newTunnelEditor(t config.Tunnel, isNew bool, idx int) tunnelEditorModel {
	mkIn := func(placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = 128
		ti.PromptStyle = lipgloss.NewStyle().Foreground(clrAccent)
		ti.TextStyle = lipgloss.NewStyle().Foreground(clrText)
		return ti
	}

	dirIdx := 0
	if t.Direction == config.TunnelRemote {
		dirIdx = 1
	}

	lh := mkIn("127.0.0.1")
	lp := mkIn("port")
	rh := mkIn("127.0.0.1")
	rp := mkIn("port")

	lh.SetValue(t.LocalHost)
	rh.SetValue(t.RemoteHost)
	if t.LocalPort != 0 {
		lp.SetValue(strconv.Itoa(t.LocalPort))
	}
	if t.RemotePort != 0 {
		rp.SetValue(strconv.Itoa(t.RemotePort))
	}

	m := tunnelEditorModel{
		dirIdx:     dirIdx,
		localHost:  lh,
		localPort:  lp,
		remoteHost: rh,
		remotePort: rp,
		focused:    tFldDir,
		isNew:      isNew,
		editIdx:    idx,
	}
	m.applyFocus()
	return m
}

func (m tunnelEditorModel) update(key tea.KeyMsg) (tunnelEditorModel, tea.Cmd) {
	m.err = ""

	switch key.String() {
	case "tab", "down":
		m.focused = (m.focused + 1) % tFldCount
		m.applyFocus()
		return m, nil

	case "shift+tab", "up":
		m.focused = (m.focused - 1 + tFldCount) % tFldCount
		m.applyFocus()
		return m, nil

	case "left":
		if m.focused == tFldDir && m.dirIdx > 0 {
			m.dirIdx--
		}
		return m, nil

	case "right":
		if m.focused == tFldDir && m.dirIdx < 1 {
			m.dirIdx++
		}
		return m, nil
	}

	if m.focused > tFldDir {
		if inp := m.activeInput(); inp != nil {
			var cmd tea.Cmd
			*inp, cmd = inp.Update(key)
			return m, cmd
		}
	}
	return m, nil
}

func (m tunnelEditorModel) getResult() (config.Tunnel, bool) {
	lp, err1 := strconv.Atoi(m.localPort.Value())
	rp, err2 := strconv.Atoi(m.remotePort.Value())
	if err1 != nil || lp < 1 || lp > 65535 {
		return config.Tunnel{}, false
	}
	if err2 != nil || rp < 1 || rp > 65535 {
		return config.Tunnel{}, false
	}
	dir := config.TunnelLocal
	if m.dirIdx == 1 {
		dir = config.TunnelRemote
	}
	return config.Tunnel{
		Direction:  dir,
		LocalHost:  m.localHost.Value(),
		LocalPort:  lp,
		RemoteHost: m.remoteHost.Value(),
		RemotePort: rp,
	}, true
}

func (m *tunnelEditorModel) activeInput() *textinput.Model {
	switch m.focused {
	case tFldLocalHost:
		return &m.localHost
	case tFldLocalPort:
		return &m.localPort
	case tFldRemoteHost:
		return &m.remoteHost
	case tFldRemotePort:
		return &m.remotePort
	}
	return nil
}

func (m *tunnelEditorModel) applyFocus() {
	m.localHost.Blur()
	m.localPort.Blur()
	m.remoteHost.Blur()
	m.remotePort.Blur()
	if inp := m.activeInput(); inp != nil {
		inp.Focus()
	}
}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	tunFieldLabelStyle = lipgloss.NewStyle().
				Width(frmLabelW).
				Foreground(clrDim)

	tunFieldFocusStyle = lipgloss.NewStyle().
				Width(frmLabelW).
				Foreground(clrFocus).
				Bold(true)

	tunSepStyle    = lipgloss.NewStyle().Foreground(clrMuted)
	tunBarStyle    = lipgloss.NewStyle().Foreground(clrFocus).Bold(true)
	tunBarDimStyle = lipgloss.NewStyle().Foreground(clrDim)

	tunRadioOnStyle  = lipgloss.NewStyle().Foreground(clrFocus).Bold(true)
	tunRadioOffStyle = lipgloss.NewStyle().Foreground(clrDim)
)

func (m tunnelEditorModel) view(width int) string {
	innerW := width - 10
	if innerW < 40 {
		innerW = 40
	}
	if innerW > 72 {
		innerW = 72
	}
	inputW := innerW - frmLabelW - 8

	var sb strings.Builder

	title := "Add Tunnel"
	if !m.isNew {
		title = "Edit Tunnel"
	}

	// Direction radio.
	dirLbl := tunFieldLabelStyle.Render("Direction")
	bar := tunBarDimStyle.Render("│")
	if m.focused == tFldDir {
		dirLbl = tunFieldFocusStyle.Render("Direction")
		bar = tunBarStyle.Render("│")
	}
	var dirParts []string
	for i, d := range []string{"local", "remote"} {
		if i == m.dirIdx {
			dirParts = append(dirParts, tunRadioOnStyle.Render("● "+d))
		} else {
			dirParts = append(dirParts, tunRadioOffStyle.Render("○ "+d))
		}
	}
	arrowHint := ""
	if m.focused == tFldDir {
		arrowHint = lipgloss.NewStyle().Foreground(clrDim).Render("  ←/→")
	}
	fmt.Fprintf(&sb, "  %s %s %s%s\n\n", dirLbl, bar, strings.Join(dirParts, "   "), arrowHint)

	// Text fields.
	for _, f := range []struct {
		label string
		fld   int
		inp   *textinput.Model
	}{
		{"Local host", tFldLocalHost, &m.localHost},
		{"Local port", tFldLocalPort, &m.localPort},
		{"Remote host", tFldRemoteHost, &m.remoteHost},
		{"Remote port", tFldRemotePort, &m.remotePort},
	} {
		f.inp.Width = inputW
		focused := m.focused == f.fld
		lbl := tunFieldLabelStyle.Render(f.label)
		b := tunBarDimStyle.Render("│")
		if focused {
			lbl = tunFieldFocusStyle.Render(f.label)
			b = tunBarStyle.Render("│")
		}
		fmt.Fprintf(&sb, "  %s %s %s\n", lbl, b, f.inp.View())
	}

	if m.err != "" {
		sb.WriteString("\n" + lipgloss.NewStyle().Foreground(clrError).Bold(true).Render("  ✗ "+m.err) + "\n")
	}

	sb.WriteString("\n" + tunSepStyle.Render(strings.Repeat("─", innerW-6)) + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(clrDim).Render(
		"  tab/↑↓ navigate   ←/→ direction   ctrl+s confirm   esc cancel",
	))

	return renderModal("⇄", title, innerW, sb.String())
}
