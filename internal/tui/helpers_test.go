package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// execCmds executes a Cmd (including Batch) synchronously, returning all
// messages. A command that does not return promptly is a timer (cursor
// blink, status tick) and is dropped, so tests do not wait for it.
func execCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil
	}
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var all []tea.Msg
		for _, c := range batch {
			all = append(all, execCmds(c)...)
		}
		return all
	}
	return []tea.Msg{msg}
}
