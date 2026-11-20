package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/master2sid2/sshm/internal/config"
	"github.com/master2sid2/sshm/internal/version"
)

// ── Layout ────────────────────────────────────────────────────────────────────

const (
	headerH = 1
	footerH = 1
)

// ── Palette ───────────────────────────────────────────────────────────────────

var (
	clrAccent  = lipgloss.Color("63")
	clrFocus   = lipgloss.Color("81")
	clrSuccess = lipgloss.Color("42")
	clrError   = lipgloss.Color("196")
	clrWarn    = lipgloss.Color("214")
	clrText    = lipgloss.Color("252")
	clrDim     = lipgloss.Color("241")
	clrMuted   = lipgloss.Color("238")
	clrHdrBg   = lipgloss.Color("235")
)

// ── Header / footer styles ────────────────────────────────────────────────────

var (
	hdrAppStyle         = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrFocus).Bold(true).Padding(0, 2)
	hdrVerStyle         = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrDim).Padding(0, 2)
	hdrOkStyle          = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrSuccess).Padding(0, 2)
	hdrErrStyle         = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrError).Padding(0, 2)
	hdrGapStyle         = lipgloss.NewStyle().Background(clrHdrBg)
	hdrBreadStyle       = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrDim)
	hdrBreadSepStyle    = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrMuted)
	hdrBreadActiveStyle = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrText)

	footerBgStyle  = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrDim)
	footerKeyStyle = lipgloss.NewStyle().Background(clrHdrBg).Foreground(clrFocus).Bold(true)
)

// ── Panel styles ──────────────────────────────────────────────────────────────

var (
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(clrMuted)

	detailSectionStyle = lipgloss.NewStyle().Foreground(clrFocus).Bold(true)
	detailKeyStyle     = lipgloss.NewStyle().Width(12).Foreground(clrDim)
	detailValStyle     = lipgloss.NewStyle().Foreground(clrText)
	detailDimStyle     = lipgloss.NewStyle().Foreground(clrDim)
	detailWarnStyle    = lipgloss.NewStyle().Foreground(clrWarn)
	emptyHintStyle     = lipgloss.NewStyle().Foreground(clrDim).Italic(true).Padding(2, 2)
)

// ── View ──────────────────────────────────────────────────────────────────────

func (a *App) View() string {
	bg := a.listView()
	modal := a.modalContent()
	if modal == "" {
		return bg
	}
	return centerOverlay(bg, modal, a.width, a.height)
}

func (a *App) modalContent() string {
	switch a.currentView {
	case viewForm:
		return a.form.View()
	case viewGroupForm:
		return a.groupForm.View()
	case viewMove:
		return a.movePicker.View()
	case viewConfirmDelete:
		return confirmDeleteView(a.deleteTargetName, a.deleteIsGroup)
	}
	switch a.activeModal {
	case modalUnlock:
		return a.unlockMdl.view()
	case modalPassMgr:
		return a.passMgrMdl.view()
	case modalHelp:
		return helpModalView()
	}
	return ""
}

func (a *App) listView() string {
	// ── Header bar: app · breadcrumb ······ status · version ──────────────
	appS := hdrAppStyle.Render("⬡  SSH Manager")
	breadS := a.breadcrumb()

	var statusS string
	switch {
	case a.statusMsg != "" && a.statusIsErr:
		statusS = hdrErrStyle.Render("✗  " + a.statusMsg)
	case a.statusMsg != "":
		statusS = hdrOkStyle.Render("✓  " + a.statusMsg)
	}
	verS := hdrVerStyle.Render(version.Version)

	leftSide := lipgloss.JoinHorizontal(lipgloss.Top, appS, breadS)
	// A long status must not push the version off screen or wrap the
	// header: clip it to what is left.
	if room := a.width - lipgloss.Width(leftSide) - lipgloss.Width(verS); lipgloss.Width(statusS) > room {
		statusS = lipgloss.NewStyle().MaxWidth(max(room, 0)).Render(statusS)
	}
	rightS := lipgloss.JoinHorizontal(lipgloss.Top, statusS, verS)
	gapW := max(0, a.width-lipgloss.Width(leftSide)-lipgloss.Width(rightS))
	header := lipgloss.JoinHorizontal(lipgloss.Top, leftSide, hdrGapStyle.Width(gapW).Render(""), rightS)

	// ── Content panels ────────────────────────────────────────────────────
	leftW := a.width/2 - 2
	rightW := a.width - leftW - 5
	contentH := a.height - headerH - footerH - 2

	leftContent := a.list.view()
	if a.filtering || a.filterQuery != "" {
		leftContent = a.searchLine(leftW) + "\n" + leftContent
	}
	left := panelStyle.Width(leftW).Height(contentH).Render(leftContent)
	right := panelStyle.Width(rightW).Height(contentH).Render(a.detailsView(rightW - 4))
	panels := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)

	return header + "\n" + panels + "\n" + a.helpBar()
}

// searchLine is the line above the list while searching: the input while
// typing, the applied query (and hit count) once enter has been pressed.
func (a *App) searchLine(width int) string {
	n := len(a.list.inner.Items())
	count := detailDimStyle.Render(fmt.Sprintf("  %d match(es)", n))
	if a.filtering {
		a.filterInput.Width = max(width-lipgloss.Width(count)-4, 8)
		return a.filterInput.View() + count
	}
	return lipgloss.NewStyle().Foreground(clrFocus).Render("🔍 "+a.filterQuery) + count +
		detailDimStyle.Render("  / edit  esc clear")
}

// breadcrumb builds the path segment shown in the header: "  ›  Prod  ›  US East"
func (a *App) breadcrumb() string {
	sep := hdrBreadSepStyle.Render("  ›  ")
	var parts []string
	for i, g := range a.groupStack {
		if i == len(a.groupStack)-1 && a.filterQuery == "" {
			parts = append(parts, hdrBreadActiveStyle.Render(g.Name))
		} else {
			parts = append(parts, hdrBreadStyle.Render(g.Name))
		}
	}
	if a.filterQuery != "" {
		parts = append(parts, hdrBreadActiveStyle.Render("search"))
	}
	if len(parts) == 0 {
		return ""
	}
	return sep + strings.Join(parts, sep)
}

// helpBar renders a context-aware footer.
func (a *App) helpBar() string {
	if a.filtering {
		return renderHelpBar(a.width, "type", "search", "↑↓", "navigate", "enter", "connect", "esc", "clear")
	}
	arrow := "↑"
	if a.sortDesc {
		arrow = "↓"
	}
	// Essentials first: on a narrow terminal the bar is cut from the end.
	pairs := []string{"↑↓", "navigate", "enter", "open", "?", "help"}
	switch {
	case a.filterQuery != "":
		pairs = append(pairs, "esc", "clear search")
	case len(a.groupStack) > 0:
		pairs = append(pairs, "esc", "back")
	default:
		pairs = append(pairs, "q", "quit")
	}
	pairs = append(pairs,
		"/", "search",
		"n", "new host",
		"g", "new folder",
		"e", "edit",
		"m", "move",
		"d", "delete",
		"p", "lock",
		"s", "sort:"+a.sortMode.label()+arrow,
	)
	return renderHelpBar(a.width, pairs...)
}

// renderHelpBar builds a full-width bar with alternating key/label pairs.
// Pairs that do not fit are dropped from the end rather than wrapping the
// bar onto a second line (which would push the whole layout down).
func renderHelpBar(width int, pairs ...string) string {
	var parts []string
	used := 0
	for i := 0; i+1 < len(pairs); i += 2 {
		k := footerKeyStyle.Render("  " + pairs[i])
		v := footerBgStyle.Render(" " + pairs[i+1] + "  ")
		w := lipgloss.Width(k) + lipgloss.Width(v)
		if width > 0 && used+w > width {
			break
		}
		used += w
		parts = append(parts, k, v)
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return bar + footerBgStyle.Width(max(0, width-lipgloss.Width(bar))).Render("")
}

func (a *App) detailsView(width int) string {
	e, ok := a.list.selectedEntry()
	if !ok {
		hint := "Select an item\nto view details"
		if a.filterQuery != "" {
			hint = "No host matches.\nLocked folders are searched\nonce unlocked."
		}
		return emptyHintStyle.Render(hint)
	}
	clip := lipgloss.NewStyle().MaxWidth(max(width, 10))
	row := func(k, v string) string {
		return clip.Render(detailKeyStyle.Render(k)+detailValStyle.Render(v)) + "\n"
	}

	var sb strings.Builder
	if e.isGroup {
		g := e.group
		sb.WriteString(detailSectionStyle.Render("▸ "+g.Name) + "\n\n")
		sb.WriteString(row("Type", "folder"))
		if path := a.pathLabel(g.ParentID); path != "" {
			sb.WriteString(row("Path", path))
		}
		if g.Locked() {
			state := "locked"
			if a.unlocked(g) {
				state = "unlocked for this session"
			}
			sb.WriteString(row("Password", state))
			if !a.unlocked(g) {
				return sb.String() // contents stay hidden until unlocked
			}
		}
		sub := a.store.GroupsInGroup(g.ID)
		prof := a.store.ProfilesInGroup(g.ID)
		sb.WriteString(row("Contents", fmt.Sprintf("%d item(s)", len(sub)+len(prof))))
		if len(sub) > 0 {
			sb.WriteString(row("Folders", fmt.Sprintf("%d", len(sub))))
		}
		if len(prof) > 0 {
			sb.WriteString(row("Hosts", fmt.Sprintf("%d", len(prof))))
		}
		return sb.String()
	}

	p := e.profile
	reveal := a.revealID == p.ID
	secret := func(s string) string {
		switch {
		case s == "":
			return "(none)"
		case reveal:
			return s
		default:
			return "••••••••"
		}
	}

	sb.WriteString(detailSectionStyle.Render(p.Name) + "\n\n")
	if path := a.pathLabel(p.GroupID); path != "" {
		sb.WriteString(row("Path", path))
	}
	sb.WriteString(row("Host", hostPort(p.Host, p.Port)))
	sb.WriteString(row("User", p.User))
	sb.WriteString(row("Auth", string(p.AuthType)))
	hasSecret := false
	switch p.AuthType {
	case config.AuthPassword:
		pw := secret(p.Password)
		if p.Password == "" {
			pw = "(asked when connecting)"
		}
		sb.WriteString(row("Password", pw))
		hasSecret = p.Password != ""
	case config.AuthKey:
		sb.WriteString(row("Key", p.KeyPath))
		pass := secret(p.KeyPassword)
		if p.KeyPassword == "" {
			pass = "(none, or asked when connecting)"
		}
		sb.WriteString(row("Passphrase", pass))
		hasSecret = p.KeyPassword != ""
	}
	if !p.UpdatedAt.IsZero() {
		sb.WriteString(row("Updated", p.UpdatedAt.Local().Format("2006-01-02 15:04")))
	}

	if len(p.Tunnels) > 0 {
		sb.WriteString("\n" + detailSectionStyle.Render("Tunnels") + "\n")
		for _, t := range p.Tunnels {
			from, to := hostPort(t.LocalHost, t.LocalPort), hostPort(t.RemoteHost, t.RemotePort)
			if t.Direction == config.TunnelRemote {
				from, to = to, from
			}
			sb.WriteString(clip.Render(fmt.Sprintf("  %s  %s %s %s",
				detailDimStyle.Render(fmt.Sprintf("%-6s", t.Direction)), from, detailDimStyle.Render("→"), to)) + "\n")
		}
	}

	if hasSecret {
		hint := "ctrl+r reveal"
		if reveal {
			hint = "ctrl+r hide"
		}
		sb.WriteString("\n" + detailDimStyle.Render(hint) + "\n")
	}
	if reveal {
		sb.WriteString(detailWarnStyle.Render("secrets are visible on screen") + "\n")
	}
	return sb.String()
}

func confirmDeleteView(name string, isGroup bool) string {
	label := "host"
	warning := ""
	if isGroup {
		label = "folder"
		warning = "\n" + lipgloss.NewStyle().Foreground(clrWarn).Render("  All contents will be deleted.")
	}
	const panelW = 40
	var sb strings.Builder
	sb.WriteString(
		lipgloss.NewStyle().Foreground(clrText).Render(fmt.Sprintf("Delete %s %q?", label, name)) +
			warning +
			"\n\n" +
			lipgloss.NewStyle().Foreground(clrError).Bold(true).Render("[y]") +
			lipgloss.NewStyle().Foreground(clrText).Render(" yes   ") +
			lipgloss.NewStyle().Foreground(clrDim).Bold(true).Render("[n]") +
			lipgloss.NewStyle().Foreground(clrText).Render(" no"),
	)
	return renderModal("✗", "Confirm Delete", panelW, sb.String())
}

// helpModalView is the "?" overlay. The columns are wide enough that no
// description wraps next to its key (a wrapped row breaks the alignment).
func helpModalView() string {
	const (
		panelW = 76 // text area = panelW-6 = 70 = 2 cols of 34 + gap 2
		colW   = 34
		kw     = 8
	)

	sep := frmSepStyle.Render(strings.Repeat("─", colW))
	keyStyle := lipgloss.NewStyle().Width(kw).Foreground(clrFocus).Bold(true)
	valStyle := lipgloss.NewStyle().Foreground(clrText)
	row := func(k, v string) string {
		return "  " + keyStyle.Render(k) + valStyle.Render(v) + "\n"
	}
	section := func(b *strings.Builder, title string) {
		b.WriteString(frmSectionStyle.Render(title) + "\n" + sep + "\n")
	}

	var left strings.Builder
	section(&left, "Navigation")
	left.WriteString(row("↑/↓", "navigate"))
	left.WriteString(row("enter", "open folder / connect"))
	left.WriteString(row("esc", "go up / clear search"))
	left.WriteString(row("/", "search here and below"))
	left.WriteString(row("s / S", "cycle sort / reverse"))
	left.WriteString(row("ctrl+r", "reveal / hide password"))
	left.WriteString(row("?", "this help"))
	left.WriteString(row("q", "quit"))

	var right strings.Builder
	section(&right, "Hosts")
	right.WriteString(row("n", "new host"))
	right.WriteString(row("e", "edit host"))
	right.WriteString(row("m", "move host"))
	right.WriteString(row("d", "delete host"))
	right.WriteString("\n")
	section(&right, "Folders")
	right.WriteString(row("g", "new folder"))
	right.WriteString(row("e / m", "rename / move folder"))
	right.WriteString(row("d", "delete folder"))
	right.WriteString(row("p", "set / change password"))

	cols := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(colW).Render(left.String()),
		"  ",
		lipgloss.NewStyle().Width(colW).Render(right.String()),
	)

	var sb strings.Builder
	sb.WriteString(cols + "\n\n")
	sb.WriteString(frmDimStyle.Render("  🔒 Locked folders ask for their password; deleting one or") + "\n")
	sb.WriteString(frmDimStyle.Render("     changing its password always asks again.") + "\n")
	sb.WriteString(frmDimStyle.Render("  Search skips locked folders until they are unlocked.") + "\n")
	sb.WriteString(frmDimStyle.Render("  CLI: sshm import-ssh · export-ssh · passwd · lock") + "\n\n")
	sb.WriteString(frmSepStyle.Render(strings.Repeat("─", panelW-6)) + "\n")
	sb.WriteString(frmHelpStyle.Render("  (press any key to close)"))

	return renderModal("?", "Keyboard Shortcuts", panelW, sb.String())
}
