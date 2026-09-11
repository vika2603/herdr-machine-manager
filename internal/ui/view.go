package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

// Colors are ANSI indexes rather than hex, so the popup follows the terminal
// theme herdr is rendered in.
var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	rowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	fieldStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
)

// defaultCols is used until the first resize message arrives.
const defaultCols = 72

// cols is the width to lay out for. A narrow popup is laid out narrow rather
// than overflowing it, so rules and rows never wrap.
func (m model) cols() int {
	if m.width <= 0 {
		return defaultCols
	}
	return m.width
}

// page lays out every screen the same way: a header with a right-aligned
// summary, a rule, the body, a rule, and the key hints with the status line
// under them.
func (m model) page(title, summary string, body []string, help string) string {
	w := m.cols()
	rule := dimStyle.Render(strings.Repeat("─", w))

	head := titleStyle.Render(title)
	if summary != "" {
		head = head + strings.Repeat(" ", max(1, w-lipgloss.Width(head)-lipgloss.Width(summary))) + dimStyle.Render(summary)
	}

	footer := m.footer()

	// The popup is a fixed number of rows and the program does not use the
	// alternate screen, so a frame taller than the pane would scroll the top
	// of it away. The body is cut to what is left after the chrome.
	if m.height > 0 {
		chrome := 4 // header, two rules, help
		if footer != "" {
			chrome++
		}
		if room := m.height - chrome; room < len(body) {
			body = body[:max(0, room)]
		}
	}

	out := []string{head, rule}
	out = append(out, body...)
	out = append(out, rule, dimStyle.Render(help))
	if footer != "" {
		out = append(out, footer)
	}
	return strings.Join(out, "\n")
}

func (m model) View() string {
	switch m.screen {
	case screenAliases:
		return m.viewAliases()
	case screenForm:
		return m.viewForm()
	case screenConfirm:
		return m.viewConfirm()
	case screenOutput:
		return m.viewOutput()
	default:
		return m.viewList()
	}
}

func (m model) viewList() string {
	var body []string
	if len(m.conns) == 0 {
		body = append(body, "", dimStyle.Render("  no connections yet"),
			dimStyle.Render("  press a to add one from ~/.ssh/config"))
	}
	for i, conn := range visible(m.conns, m.offset, m.rows()) {
		body = append(body, m.connRow(m.offset+i, conn))
	}
	return m.page("SSH connections", m.counts(), body,
		"a add · e edit · space connect/disconnect · enter details · d forget · q close")
}

// connRow shows the stored connection and, when something is happening to it,
// what that is. The state of a command lives on the row it belongs to.
func (m model) connRow(i int, conn daemon.Connection) string {
	state, detail := m.connState(conn)

	target := conn.Target
	if alias, ok := m.aliasFor(conn.Target); ok && alias.Host != "" {
		target = conn.Target + dimStyle.Render(" → "+endpoint(alias.User, alias.Host, alias.Port))
	}
	if detail == "" {
		detail = target
		if conn.Session != "" {
			detail += dimStyle.Render("  session: " + conn.Session)
		}
	}

	row := fmt.Sprintf(" %s %s  %s", state, pad(conn.Label, 16), detail)
	if i == m.cursor {
		return rowStyle.Render("❯") + row
	}
	return " " + row
}

// connState reports the marker and the trailing text for a connection: a job
// in flight wins over the stored state, and a failure stays visible until the
// next job replaces it.
func (m model) connState(conn daemon.Connection) (marker, detail string) {
	if job, ok := m.jobFor(conn.ID); ok {
		switch job.State {
		case jobs.StateAwaitingInput:
			return errStyle.Render("!"), warnStyle.Render("needs an answer — press enter")
		case jobs.StateQueued:
			return warnStyle.Render("⟳"), dimStyle.Render(progressWord(job.Kind) + " · queued")
		default:
			return warnStyle.Render("⟳"), warnStyle.Render(progressWord(job.Kind) + "…")
		}
	}
	if job, ok := m.lastJobFor(conn.ID); ok && job.State == jobs.StateFailed {
		return errStyle.Render("✕"), errStyle.Render(truncate(job.Err, max(20, m.cols()-28)))
	}
	if conn.Active {
		return okStyle.Render("●"), ""
	}
	return dimStyle.Render("○"), ""
}

func progressWord(kind jobs.Kind) string {
	switch kind {
	case jobs.KindConnect:
		return "connecting"
	case jobs.KindDisconnect:
		return "disconnecting"
	case jobs.KindForget:
		return "removing"
	case jobs.KindRename:
		return "renaming"
	}
	return string(kind)
}

func (m model) counts() string {
	active := 0
	for _, conn := range m.conns {
		if conn.Active {
			active++
		}
	}
	parts := []string{fmt.Sprintf("%d connected", active)}
	if off := len(m.conns) - active; off > 0 {
		parts = append(parts, fmt.Sprintf("%d off", off))
	}
	if n := len(m.active); n > 0 {
		parts = append(parts, fmt.Sprintf("%d working", n))
	}
	return strings.Join(parts, " · ")
}

func (m model) viewAliases() string {
	list := m.filtered()
	body := []string{" " + accentStyle.Render("filter") + " " + m.aliasFilter.View()}

	if len(list) == 0 {
		body = append(body, dimStyle.Render("  nothing matches in ~/.ssh/config"))
	}
	for i, alias := range visible(list, m.aliasOffset, m.aliasRows()) {
		index := m.aliasOffset + i
		detail := dimStyle.Render(endpoint(alias.User, alias.Host, alias.Port))
		if m.savedTarget(alias.Name) {
			detail += "  " + okStyle.Render("added")
		}
		row := fmt.Sprintf(" %s  %s", pad(alias.Name, 18), detail)
		if index == m.aliasCursor {
			body = append(body, rowStyle.Render("❯")+row)
		} else {
			body = append(body, " "+row)
		}
	}

	summary := fmt.Sprintf("%d of %d aliases", len(list), len(m.aliases))
	return m.page("Add a connection — pick an alias", summary, body,
		"↑↓ move · enter select · type to filter · esc cancel")
}

func (m model) savedTarget(name string) bool {
	for _, conn := range m.conns {
		if conn.Target == name {
			return true
		}
	}
	return false
}

func (m model) viewForm() string {
	field := func(i int, name string, input string) string {
		marker, label := "  ", fieldStyle.Render(pad(name, 11))
		if i == m.field {
			marker, label = rowStyle.Render("❯ "), accentStyle.Render(pad(name, 11))
		}
		return marker + label + input
	}

	box := "[ ]"
	if m.install {
		box = okStyle.Render("[×]")
	}

	body := []string{
		field(0, "Label", m.label.View()),
		field(1, "SSH target", m.target.View()),
		field(2, "Session", m.session.View()),
		field(3, "", box+" install herdr on the remote if it is missing"),
		"",
		dimStyle.Render("  $ " + truncate(strings.Join(m.previewArgs(), " "), m.cols()-4)),
		dimStyle.Render("  runs in the background; you can close this popup"),
	}

	title, summary := "Add a connection", ""
	if m.editing != "" {
		title = "Edit connection"
		if conn, ok := m.current(); ok && conn.Active && conn.Target != strings.TrimSpace(m.target.Value()) {
			summary = "target changed — will reconnect"
		}
	}
	return m.page(title, summary, body, "tab next · space toggle · enter save · esc back")
}

// previewArgs shows the command the daemon will run, so the form is not a
// black box over herdr's own CLI.
func (m model) previewArgs() []string {
	label := strings.TrimSpace(m.label.Value())
	target := strings.TrimSpace(m.target.Value())
	if target == "" {
		target = "<ssh-target>"
	}
	if label == "" {
		label = "<label>"
	}
	args := []string{"herdr", "machine", "add", target, "--label", label}
	if session := strings.TrimSpace(m.session.Value()); session != "" {
		args = append(args, "--remote-session", session)
	}
	return args
}

func (m model) viewConfirm() string {
	if m.confirm == nil {
		return ""
	}
	note := "  it is not connected, so this only drops it here"
	if m.confirm.Active {
		note = "  it is removed from herdr too; sessions already running keep running"
	}
	body := []string{
		"",
		"  forget " + titleStyle.Render(m.confirm.Label) + dimStyle.Render("  ("+m.confirm.Target+")") + "?",
		"",
		dimStyle.Render(note),
	}
	return m.page("Forget a connection", "", body, "y forget · n cancel")
}

// viewOutput shows what the command behind the selected connection is doing.
func (m model) viewOutput() string {
	conn, ok := m.current()
	if !ok {
		return m.viewList()
	}
	job, hasJob := m.lastJobFor(conn.ID)
	if !hasJob {
		body := []string{"", dimStyle.Render("  nothing has run for this connection yet")}
		return m.page(conn.Label, conn.Target, body, "esc back")
	}

	body := []string{""}
	for _, line := range m.tail(job) {
		body = append(body, dimStyle.Render("  "+truncate(line, m.cols()-4)))
	}

	help := "esc back · x cancel"
	if job.State == jobs.StateAwaitingInput {
		body = append(body, "", "  "+warnStyle.Render(job.Prompt), "  "+m.jobInput.View())
		help = "enter answer · esc back · x cancel"
	}
	return m.page(conn.Label, stateLabel(job), body, help)
}

// tail is the output buffered for a job: the lines streamed while the popup
// was open, seeded from the daemon's own copy when the list was read.
func (m model) tail(job jobs.Job) []string {
	lines := m.outputs[job.ID]
	rows := outputTail
	if m.height > 0 {
		rows = max(3, m.height-9)
	}
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	return lines
}

func (m model) footer() string {
	switch {
	case m.failure != "":
		return errStyle.Render("! " + truncate(m.failure, m.cols()-2))
	case m.status != "":
		return dimStyle.Render("  " + truncate(m.status, m.cols()-2))
	}
	return ""
}

func stateLabel(job jobs.Job) string {
	switch job.State {
	case jobs.StateSucceeded:
		return okStyle.Render("succeeded")
	case jobs.StateFailed:
		return errStyle.Render("failed: " + truncate(job.Err, 40))
	case jobs.StateAwaitingInput:
		return warnStyle.Render("awaiting input")
	case jobs.StateCancelled:
		return dimStyle.Render("cancelled")
	default:
		return warnStyle.Render(progressWord(job.Kind) + "…")
	}
}

func endpoint(user, host, port string) string {
	if host == "" {
		return ""
	}
	if user != "" {
		host = user + "@" + host
	}
	if port != "" && port != "22" {
		host += ":" + port
	}
	return host
}

// visible returns the window of a list that fits on screen.
func visible[T any](items []T, offset, rows int) []T {
	if rows <= 0 || offset >= len(items) {
		return nil
	}
	if offset < 0 {
		offset = 0
	}
	end := min(offset+rows, len(items))
	return items[offset:end]
}

func pad(s string, width int) string {
	s = truncate(s, width)
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func truncate(s string, width int) string {
	if width <= 1 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
