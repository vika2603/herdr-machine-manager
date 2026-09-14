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

const (
	listHelp  = "Ctrl+A add · Ctrl+E edit · Space toggle · Enter details · Esc close"
	aliasHelp = "Enter select · Esc back"
)

// cols is the width to lay out for. A narrow popup is laid out narrow rather
// than overflowing it, so rules and rows never wrap.
func (m model) cols() int {
	if m.width <= 0 {
		return defaultCols
	}
	return m.width
}

// page lays out every screen the same way: a header with a right-aligned
// summary, a rule, the body, and a bottom-aligned footer. Status messages sit
// above the footer rule so the key hints always occupy the last row.
func (m model) page(title, summary string, body []string, help string) string {
	w := m.cols()
	rule := dimStyle.Render(strings.Repeat("─", w))

	head := titleStyle.Render(title)
	if summary != "" {
		head = head + strings.Repeat(" ", max(1, w-lipgloss.Width(head)-lipgloss.Width(summary))) + dimStyle.Render(summary)
	}

	footer := m.footer()
	helpLines := m.helpLines(help)

	// The popup is a fixed number of rows and the program does not use the
	// alternate screen, so a frame taller than the pane would scroll the top
	// of it away. The body is cut to what is left after the chrome.
	if m.height > 0 {
		room := m.bodyRows(help)
		visibleBody := make([]string, room)
		copy(visibleBody, body)
		body = visibleBody
	}

	out := []string{head, rule}
	out = append(out, body...)
	if footer != "" {
		out = append(out, footer)
	}
	out = append(out, rule)
	for _, line := range helpLines {
		out = append(out, dimStyle.Render(line))
	}
	if m.height > 0 && len(out) > m.height {
		out = out[len(out)-m.height:]
	}
	return strings.Join(out, "\n")
}

// Keep the footer to one quiet line. Drop secondary hints first in narrow
// panes, retaining the primary action and the way back for as long as they fit.
func (m model) helpLines(help string) []string {
	hints := strings.Split(help, " · ")
	for len(hints) > 1 && lipgloss.Width(strings.Join(hints, " · ")) > m.cols() {
		i := len(hints) - 2
		hints = append(hints[:i], hints[i+1:]...)
	}
	return []string{lipgloss.NewStyle().MaxWidth(m.cols()).Render(strings.Join(hints, " · "))}
}

func (m model) bodyRows(help string) int {
	chrome := 3 + len(m.helpLines(help)) // header and two rules
	if m.footer() != "" {
		chrome++
	}
	return max(0, m.height-chrome)
}

func (m model) View() string {
	base := m.viewScreen()
	if m.dialog != nil {
		return m.viewPrompt(base)
	}
	return base
}

func (m model) viewScreen() string {
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
	m.offset = scroll(m.offset, m.cursor, m.rows())
	var body []string
	if len(m.conns) == 0 {
		body = append(body, "", dimStyle.Render("  no connections yet"),
			dimStyle.Render("  press Ctrl+A to add one from ~/.ssh/config"))
	}
	for i, conn := range visible(m.conns, m.offset, m.rows()) {
		body = append(body, m.connRow(m.offset+i, conn))
	}
	return m.page("SSH connections", m.counts(), body, listHelp)
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
	m.aliasOffset = scroll(m.aliasOffset, m.aliasCursor, m.aliasRows())
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
	return m.page("Add a connection — pick an alias", summary, body, aliasHelp)
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
		field(3, "", box+" allow remote herdr install (asks first)"),
		"",
		dimStyle.Render("  $ " + truncate(strings.Join(m.previewArgs(), " "), m.cols()-4)),
		dimStyle.Render("  runs in background; questions open a small popup"),
	}

	title, summary := "Add a connection", ""
	if m.editing != "" {
		title = "Edit connection"
		if conn, ok := m.current(); ok && conn.Active && conn.Target != strings.TrimSpace(m.target.Value()) {
			summary = "target changed — will reconnect"
		}
	}
	return m.page(title, summary, body, "Enter save · Tab field · Esc back")
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
	return m.page("Forget a connection", "", body, "Enter forget · Esc cancel")
}

// viewOutput always shows the saved connection settings, with task output below
// them when available.
func (m model) viewOutput() string {
	conn, ok := m.current()
	if !ok {
		return m.viewList()
	}
	field := func(name, value string) string {
		return "  " + dimStyle.Render(pad(name, 11)) + value
	}
	session := conn.Session
	if session == "" {
		session = dimStyle.Render("(default)")
	}
	state := dimStyle.Render("disconnected")
	if conn.Active {
		state = okStyle.Render("connected")
	}
	body := []string{
		field("Label", conn.Label),
		field("SSH target", conn.Target),
		field("Session", session),
		field("State", state),
	}
	if alias, found := m.aliasFor(conn.Target); found && alias.Host != "" {
		body = append(body, field("Address", endpoint(alias.User, alias.Host, alias.Port)))
	}
	help := "Ctrl+E edit · Esc back"
	job, hasJob := m.lastJobFor(conn.ID)
	if !hasJob {
		return m.page("Connection details", "", body, help)
	}
	if !job.State.Terminal() {
		help = "Ctrl+E edit · Ctrl+X cancel job · Esc back"
	}
	if job.State == jobs.StateAwaitingInput {
		help = "Enter answer · Ctrl+E edit · Esc back"
	}
	body = append(body, "", field("Task", stateLabel(job)))
	var prompt []string
	if job.State == jobs.StateAwaitingInput {
		prompt = []string{"  " + warnStyle.Render(job.Prompt), dimStyle.Render("  Press Enter to answer")}
	}
	rows := outputTail
	if m.height > 0 {
		// Reserve the answer prompt before allocating space to settings and
		// logs, so a short pane can still answer an interactive job.
		room := max(0, m.bodyRows(help)-len(prompt))
		body = body[:min(len(body), room)]
		rows = room - len(body)
	}
	for _, line := range m.tail(job, rows) {
		body = append(body, dimStyle.Render("  "+truncate(line, m.cols()-4)))
	}
	body = append(body, prompt...)
	return m.page("Connection details", "", body, help)
}

// tail is the output buffered for a job: the lines streamed while the popup
// was open, seeded from the daemon's own copy when the list was read.
func (m model) tail(job jobs.Job, rows int) []string {
	lines := m.outputs[job.ID]
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
