package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
)

type groupSavedMsg struct{ group config.Group }
type groupCancelledMsg struct{}

type groupFormModel struct {
	orig   config.Group
	inp    textinput.Model
	isEdit bool
	err    string
	width  int
}

func newGroupFormModel(g config.Group, isEdit bool) groupFormModel {
	t := textinput.New()
	t.Placeholder = "folder name"
	t.CharLimit = 128
	t.PromptStyle = lipgloss.NewStyle().Foreground(clrAccent)
	t.TextStyle = lipgloss.NewStyle().Foreground(clrText)
	t.SetValue(g.Name)
	t.Focus()
	return groupFormModel{orig: g, inp: t, isEdit: isEdit}
}

func (m groupFormModel) Init() tea.Cmd { return textinput.Blink }

func (m groupFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsm.Width
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return groupCancelledMsg{} }
	case "ctrl+s", "enter":
		name := strings.TrimSpace(m.inp.Value())
		if name == "" {
			m.err = "name is required"
			return m, nil
		}
		g := m.orig
		g.Name = name
		return m, func() tea.Msg { return groupSavedMsg{g} }
	default:
		var cmd tea.Cmd
		m.inp, cmd = m.inp.Update(key)
		return m, cmd
	}
}

func (m groupFormModel) View() string {
	innerW := m.width - 10
	if innerW < 36 {
		innerW = 36
	}
	if innerW > 60 {
		innerW = 60
	}
	m.inp.Width = innerW - frmLabelW - 8

	title := "New Folder"
	if m.isEdit {
		title = "Rename Folder"
	}

	var sb strings.Builder

	lbl := frmLabelStyle.Render("Name")
	bar := frmBarStyle.Render("│")
	sb.WriteString("  " + lbl + " " + bar + " " + m.inp.View() + "\n")

	if m.err != "" {
		sb.WriteString("\n" + frmErrStyle.Render("  ✗ "+m.err) + "\n")
	}

	sb.WriteString("\n" + frmSepStyle.Render(strings.Repeat("─", innerW-6)) + "\n")
	sb.WriteString(frmHelpStyle.Render("  enter / ctrl+s save   esc cancel"))

	return renderModal("▸", title, innerW, sb.String())
}
