package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

// Colours are ANSI indexes, and secondary text is faint rather than grey, so
// the popup follows the terminal's dark or light theme. Yellow is left out:
// it is unreadable on most light themes.
var (
	boldStyle    = lipgloss.NewStyle().Bold(true)
	faintStyle   = lipgloss.NewStyle().Faint(true)
	accentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	magentaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
)

const (
	// wideMin is the narrowest pane that holds the list and the panel side by
	// side.
	wideMin = 76
	// defaultCols and defaultRows apply until the first resize arrives.
	defaultCols, defaultRows = 100, 26
	// chromeRows is the header, its rule, the status rule and the hint line.
	chromeRows = 4
	// keyWidth is the column a panel's setting names take.
	keyWidth = 10
)

// look is how a state appears wherever it is shown: one glyph, one colour and
// one word.
type look struct {
	glyph, word string
	style       lipgloss.Style
}

func (l look) String() string { return l.style.Render(l.glyph + " " + l.word) }

var (
	lookConnected    = look{"●", "connected", greenStyle}
	lookDisconnected = look{"○", "disconnected", faintStyle}
	lookAsking       = look{"◆", "needs answer", magentaStyle}
	progressWords    = map[jobs.Kind]string{jobs.KindConnect: "connecting…", jobs.KindDisconnect: "disconnecting…", jobs.KindRename: "renaming…", jobs.KindForget: "forgetting…"}
)

func jobLook(job jobs.Job) look {
	kind := string(job.Kind)
	switch job.State {
	case jobs.StateQueued:
		return look{"◌", kind + " queued", accentStyle}
	case jobs.StateAwaitingInput:
		return lookAsking
	case jobs.StateSucceeded:
		return look{"✓", kind + " succeeded", greenStyle}
	case jobs.StateFailed:
		return look{"✕", kind + " failed", redStyle}
	case jobs.StateCancelled:
		return look{"⊘", kind + " cancelled", faintStyle}
	}
	return look{"◐", or(progressWords[job.Kind], kind+"…"), accentStyle}
}

// connLook is the state a connection shows: a job in flight wins over the
// stored state, and a failure stays until the next job replaces it, together
// with its error.
func (m model) connLook(c daemon.Connection) (look, string) {
	if job, ok := m.pendingJob(c.ID); ok {
		return jobLook(job), ""
	}
	if job, ok := m.lastJob(c.ID); ok && job.State == jobs.StateFailed {
		return jobLook(job), job.Err
	}
	if c.Active {
		return lookConnected, ""
	}
	return lookDisconnected, ""
}

func (m model) cols() int      { return or(m.width, defaultCols) }
func (m model) rows() int      { return or(m.height, defaultRows) }
func (m model) wide() bool     { return m.cols() >= wideMin }
func (m model) bodyRows() int  { return max(1, m.rows()-chromeRows) }
func (m model) aliasRows() int { return max(1, m.bodyRows()-3) }

// listWidth and panelWidth exclude the one-column margins and the divider.
func (m model) listWidth() int {
	if !m.wide() {
		return m.cols() - 2
	}
	return min(44, m.cols()*2/5)
}

func (m model) panelWidth() int {
	if !m.wide() {
		return m.cols() - 2
	}
	return m.cols() - m.listWidth() - 5
}

type hint struct{ key, action string }

// frame lays out every screen the same way: a header with a summary, a rule,
// the body, a rule carrying the status, and one line of key hints. Without the
// alternate screen a taller frame would scroll its own top away, so the frame
// is exactly the pane's size and no line is wider than it.
func (m model) frame(title, summary string, left, right []string, hints []hint) string {
	w, divider := m.cols(), -1
	if right != nil {
		divider = m.listWidth() + 2
	}
	head := " " + boldStyle.Render(title)
	summary = ansi.Truncate(summary, max(0, w-lipgloss.Width(head)-3), "…")
	head += strings.Repeat(" ", max(1, w-1-lipgloss.Width(head)-lipgloss.Width(summary))) + faintStyle.Render(summary)
	out := []string{head, rule(w, divider, '┬', "")}
	for i := range m.bodyRows() {
		line := " " + at(left, i)
		if right != nil {
			line = " " + fit(at(left, i), m.listWidth()) + faintStyle.Render(" │ ") + at(right, i)
		}
		out = append(out, line)
	}
	note := faintStyle.Render(m.status)
	if err := or(m.failure, or(m.link, m.config)); err != "" {
		note = redStyle.Render("✕ " + oneLine(err))
	}
	out = append(out, rule(w, divider, '┴', note), hintLine(hints, w))
	for i := range out {
		out[i] = ansi.Truncate(out[i], w, "")
	}
	return strings.Join(out[max(0, len(out)-m.rows()):], "\n")
}

// rule is a full-width line meeting the divider, or carrying note near its
// start instead.
func rule(w, divider int, junction rune, note string) string {
	line := []rune(strings.Repeat("─", w))
	if note == "" {
		if divider >= 0 && divider < w {
			line[divider] = junction
		}
		return faintStyle.Render(string(line))
	}
	note = ansi.Truncate(note, max(0, w-4), "…")
	return faintStyle.Render("─ ") + note + faintStyle.Render(" "+string(line[min(w, 3+lipgloss.Width(note)):]))
}

// hintLine drops hints from the end, keeping the last, until the line fits:
// hints are listed most important first, and the way out last.
func hintLine(hints []hint, w int) string {
	for {
		parts := make([]string, len(hints))
		for i, h := range hints {
			parts[i] = h.key + " " + faintStyle.Render(h.action)
		}
		line := " " + strings.Join(parts, "   ")
		if len(hints) <= 1 || lipgloss.Width(line) <= w {
			return line
		}
		hints = append(hints[:len(hints)-2:len(hints)-2], hints[len(hints)-1])
	}
}

func (m model) View() string {
	pw, rows := m.panelWidth(), m.bodyRows()
	conn, selected := m.current()
	var panel []string
	switch {
	case m.dialog != nil:
		panel = m.promptLines(pw, rows)
	case m.mode == modePick:
		panel = m.pickLines(pw, rows)
	case m.mode == modeForm:
		panel = m.formLines(pw, rows)
	case m.mode == modeForget:
		panel = m.forgetLines(pw, rows)
	case selected && (m.wide() || m.mode == modeDetail):
		panel = m.detailLines(conn, pw, rows)
	case m.wide():
		panel = []string{}
	}
	if !m.wide() && panel != nil {
		return m.frame("Machines", m.summary(), panel, nil, m.hints())
	}
	return m.frame("Machines", m.summary(), m.listLines(), panel, m.hints())
}

func (m model) summary() string {
	active := 0
	for _, c := range m.conns {
		if c.Active {
			active++
		}
	}
	s := fmt.Sprintf("%d of %d connected", active, len(m.conns))
	if n := len(m.waiting()); n > 0 {
		s += fmt.Sprintf(" · %d needs answer", n)
	}
	return s
}

func (m model) hints() []hint {
	switch {
	case m.dialog != nil && m.dialog.info.Kind == jobs.PromptConfirm:
		return []hint{{"←→", "choose"}, {"enter", "send"}, {"esc", "later"}}
	case m.dialog != nil:
		return []hint{{"enter", "send"}, {"esc", "later"}}
	case m.mode == modePick:
		return []hint{{"enter", "choose"}, {"esc", "cancel"}}
	case m.mode == modeForm && m.field == 3:
		return []hint{{"enter", "save"}, {"tab", "next field"}, {"space", "switch"}, {"esc", "cancel"}}
	case m.mode == modeForm:
		return []hint{{"enter", "save"}, {"tab", "next field"}, {"esc", "cancel"}}
	case m.mode == modeForget:
		return []hint{{"enter", "forget"}, {"esc", "keep"}}
	}
	back := hint{"esc", "close"}
	if m.mode == modeDetail {
		back.action = "back"
	}
	conn, ok := m.current()
	if !ok {
		return []hint{{"a", "add"}, back}
	}
	var hs []hint
	job, busy := m.pendingJob(conn.ID)
	if busy && job.State == jobs.StateAwaitingInput {
		hs = append(hs, hint{"enter", "answer"})
	} else if !m.wide() && m.mode == modeList {
		hs = append(hs, hint{"enter", "details"})
	}
	if busy {
		hs = append(hs, hint{"x", "cancel job"})
	} else {
		hs = append(hs, hint{"space", map[bool]string{true: "disconnect", false: "connect"}[conn.Active]})
	}
	return append(hs, hint{"a", "add"}, hint{"e", "edit"}, hint{"d", "forget"}, hint{"r", "refresh"}, back)
}

// listLines is one row per connection: the selection mark, the state glyph,
// the label and the state word in aligned columns, then the error or the
// target as room allows.
func (m model) listLines() []string {
	if len(m.conns) == 0 {
		return []string{"", faintStyle.Render(" No connections yet."), faintStyle.Render(" Press a to add one from ~/.ssh/config.")}
	}
	w := m.listWidth()
	m.offset = scroll(m.offset, m.cursor, m.bodyRows())
	shown := m.conns[m.offset:min(m.offset+m.bodyRows(), len(m.conns))]
	labelW, wordW := 0, 0
	for _, c := range shown {
		l, _ := m.connLook(c)
		labelW, wordW = max(labelW, lipgloss.Width(c.Label)), max(wordW, lipgloss.Width(l.word))
	}
	labelW = min(labelW, 22, max(4, w-6-wordW))

	var out []string
	for i, c := range shown {
		l, failure := m.connLook(c)
		mark, label := "  ", fit(c.Label, labelW)
		if m.offset+i == m.cursor {
			mark, label = accentStyle.Render("❯ "), boldStyle.Render(label)
		}
		row := mark + l.style.Render(l.glyph) + " " + label + "  " + l.style.Render(fit(l.word, wordW))
		if room := w - lipgloss.Width(row) - 2; room >= 6 && failure != "" {
			row += "  " + redStyle.Render(fit(oneLine(failure), room))
		} else if room >= 6 {
			row += "  " + faintStyle.Render(fit(c.Target, room))
		}
		out = append(out, row)
	}
	return out
}

func setting(name, value string, w int) string {
	return faintStyle.Render(fit(name, keyWidth)) + ansi.Truncate(value, max(0, w-keyWidth), "…")
}

// detailLines is the selected connection's panel: its state with the error or
// question behind it, its settings, then the latest job's output as far as it
// fits.
func (m model) detailLines(c daemon.Connection, w, rows int) []string {
	l, failure := m.connLook(c)
	lines := []string{boldStyle.Render(fit(c.Label, w)), l.String()}
	for _, line := range limit(wrap(failure, w-2), 3) {
		lines = append(lines, "  "+redStyle.Render(line))
	}
	// The panel follows the job the state line shows: one queued behind it
	// has neither output nor a question yet.
	job, hasJob := m.pendingJob(c.ID)
	if !hasJob {
		job, hasJob = m.lastJob(c.ID)
	}
	if hasJob && job.State == jobs.StateAwaitingInput {
		lines = append(lines, "  "+magentaStyle.Render(ansi.Truncate(oneLine(job.Prompt), w-2, "…")))
	}
	lines = append(lines, "", setting("Target", c.Target, w))
	if alias, ok := find(m.aliases, func(a sshconfig.Alias) bool { return a.Name == c.Target }); ok && alias.Host != "" {
		lines = append(lines, setting("Address", endpoint(alias.User, alias.Host, alias.Port), w))
	}
	lines = append(lines, setting("Session", or(c.Session, faintStyle.Render("default")), w))
	if !hasJob {
		return squeeze(lines, rows)
	}
	// The state line already shows a job that is unfinished or failed.
	if job.State == jobs.StateSucceeded || job.State == jobs.StateCancelled {
		lines = append(lines, setting("Last job", jobLook(job).String(), w))
	}
	if output, room := m.outputs[job.ID], rows-len(lines)-2; room > 0 && len(output) > 0 {
		lines = append(lines, "", faintStyle.Render("Output"))
		for _, line := range output[max(0, len(output)-room):] {
			lines = append(lines, faintStyle.Render(fit(oneLine(line), w)))
		}
	}
	return squeeze(lines, rows)
}

func (m model) pickLines(w, rows int) []string {
	list, filter := m.filtered(), m.filter
	filter.Width = max(1, w-keyWidth)
	lines := []string{
		boldStyle.Render("Add a connection") + faintStyle.Render(fmt.Sprintf("  %d of %d aliases", len(list), len(m.aliases))),
		faintStyle.Render(fit("Filter", keyWidth)) + filter.View(),
		"",
	}
	if len(list) == 0 {
		return append(lines, faintStyle.Render("No alias in ~/.ssh/config matches."))
	}
	nameW := 0
	for _, a := range list {
		nameW = max(nameW, lipgloss.Width(a.Name))
	}
	nameW = min(nameW, 24, w/2)
	offset := max(0, m.aliasCursor-m.aliasRows()+1)
	for i := offset; i < min(offset+m.aliasRows(), len(list)); i++ {
		a := list[i]
		mark, name := "  ", fit(a.Name, nameW)
		if i == m.aliasCursor {
			mark, name = accentStyle.Render("❯ "), boldStyle.Render(name)
		}
		addr := faintStyle.Render(endpoint(a.User, a.Host, a.Port))
		if index(m.conns, func(c daemon.Connection) bool { return c.Target == a.Name }) >= 0 {
			addr = fit(addr, max(0, w-nameW-13)) + "  " + greenStyle.Render("added")
		}
		lines = append(lines, mark+name+"  "+addr)
	}
	// In a pane too short for the whole panel the selected alias, which is
	// last in the window, outlasts the title and the filter.
	return lines[max(0, len(lines)-rows):]
}

func (m model) formLines(w, rows int) []string {
	lines := []string{boldStyle.Render(map[bool]string{true: "New connection", false: "Edit connection"}[m.editing == ""]), ""}
	for i, name := range []string{"Label", "Target", "Session", "Install"} {
		mark, key := "  ", faintStyle.Render(fit(name, keyWidth-1))
		if i == m.field {
			mark, key = accentStyle.Render("❯ "), accentStyle.Render(fit(name, keyWidth-1))
		}
		value := map[bool]string{true: "[x]", false: "[ ]"}[m.install] + " allow installing herdr on the remote"
		if i < 3 {
			in := m.fields[i]
			in.Width = max(1, w-keyWidth-2)
			value = in.View()
		}
		lines = append(lines, mark+key+ansi.Truncate(value, max(0, w-keyWidth-1), "…"))
	}
	lines = append(lines, "")
	if conn, ok := m.reconnects(); ok {
		lines = append(lines, redStyle.Render("!")+" Saving reconnects "+conn.Label+".")
	}
	p := m.formParams()
	command := "$ herdr machine add " + or(p.Target, "<target>") + " --label " + or(p.Label, "<label>")
	if p.Session != "" {
		command += " --remote-session " + p.Session
	}
	for _, line := range limit(wrap(command, w), 2) {
		lines = append(lines, faintStyle.Render(line))
	}
	return squeeze(lines, rows)
}

func (m model) forgetLines(w, rows int) []string {
	conn, _ := m.connByID(m.forget)
	note := "It is not connected, so only its saved settings here are removed."
	if conn.Active {
		note = "It is connected, so it is also removed from herdr. Sessions already running on that host keep running."
	}
	lines := []string{boldStyle.Render(fit("Forget "+conn.Label+"?", w)), "", setting("Target", conn.Target, w), ""}
	return squeeze(append(lines, wrap(note, w)...), rows)
}

// promptLines is the waiting question: whose it is, what kind of answer it
// wants, as much of the question as fits, and the answer control, which is
// never cut.
func (m model) promptLines(w, rows int) []string {
	d := m.dialog
	who := boldStyle.Render(ansi.Truncate(d.jobID, w, "…"))
	if conn, ok := m.connByID(d.connID); ok {
		who = boldStyle.Render(ansi.Truncate(conn.Label, w, "…")) + faintStyle.Render(ansi.Truncate("  "+conn.Target, max(0, w-lipgloss.Width(conn.Label)), "…"))
	}
	kind := map[jobs.PromptKind]string{jobs.PromptConfirm: "confirmation", jobs.PromptSecret: "password"}[d.info.Kind]
	head := []string{who, lookAsking.String() + faintStyle.Render(" · "+or(kind, "text")), ""}

	control := button("No", !d.yes) + "  " + button("Yes", d.yes)
	if d.info.Kind != jobs.PromptConfirm {
		in := d.input
		in.Width, in.EchoCharacter = max(1, w-3), '•'
		control = faintStyle.Render("› ") + in.View()
	}
	keep := []string{control}
	if d.sending {
		keep = append(keep, faintStyle.Render("sending…"))
	} else if d.failure != "" {
		keep = append(keep, redStyle.Render(fit(oneLine(d.failure), w)))
	}
	if rows <= len(keep) {
		return limit(keep, rows)
	}

	// Blank separators give way first, so the question may use their rows;
	// its end, where the question is, is what stays.
	question := wrap(strings.TrimSpace(ansi.Strip(d.prompt)), w)
	question = question[max(0, len(question)-max(1, rows-len(keep)-nonBlank(head))):]
	top := squeeze(append(head, question...), rows-len(keep))
	return squeeze(append(append(top, ""), keep...), rows)
}

func button(label string, selected bool) string {
	if selected {
		return accentStyle.Bold(true).Render("[ " + label + " ]")
	}
	return faintStyle.Render("  " + label + "  ")
}

// View of the standalone prompt pane: the question alone, in the same frame.
func (m promptModel) View() string {
	body := []string{faintStyle.Render("Loading the question…")}
	if m.dialog != nil {
		body = m.promptLines(m.cols()-2, m.bodyRows())
	} else if err := or(m.failure, or(m.link, m.config)); err != "" {
		body = wrap(err, m.cols()-2)
	}
	summary := ""
	if n := len(m.waiting()); n > 1 {
		summary = fmt.Sprintf("%d questions waiting", n)
	}
	m.status = ""
	return m.frame("SSH input", summary, body, nil, m.hints())
}

func (m promptModel) hints() []hint {
	if m.dialog == nil {
		return []hint{{"esc", "close"}}
	}
	return m.model.hints()
}

// squeeze fits lines into rows, dropping blank separators from the bottom up
// before cutting the last lines.
func squeeze(lines []string, rows int) []string {
	for i := len(lines) - 1; i >= 0 && len(lines) > rows; i-- {
		if lines[i] == "" {
			lines = append(lines[:i], lines[i+1:]...)
		}
	}
	return limit(lines, rows)
}

func nonBlank(lines []string) int {
	n := 0
	for _, line := range lines {
		if line != "" {
			n++
		}
	}
	return n
}

func limit(lines []string, n int) []string { return lines[:max(0, min(len(lines), n))] }

// wrap breaks text at spaces only, so options such as --remote-session stay
// whole, and hard-wraps a word longer than the line.
func wrap(s string, w int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) > w {
			lines, line = append(lines, line), ""
		}
		line = strings.TrimPrefix(line+" "+word, " ")
		if lipgloss.Width(line) > w {
			parts := strings.Split(ansi.Hardwrap(line, max(1, w), false), "\n")
			lines, line = append(lines, parts[:len(parts)-1]...), parts[len(parts)-1]
		}
	}
	if line == "" {
		return lines
	}
	return append(lines, line)
}

// oneLine flattens command output and errors, which may carry control
// sequences and line breaks, into one row of plain text.
func oneLine(s string) string { return strings.Join(strings.Fields(ansi.Strip(s)), " ") }

// fit truncates or pads s to exactly w columns.
func fit(s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "…")
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func or[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
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
