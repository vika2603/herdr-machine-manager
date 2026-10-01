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

// The palette has a variant for dark and for light terminal backgrounds, each
// readable on its background; Lip Gloss picks the variant and degrades the
// colour on terminals with fewer colours. Text without a role keeps the
// terminal's own foreground. One accent marks focus and action; the state
// colours appear only where a state is shown.
var (
	accentColor = lipgloss.AdaptiveColor{Light: "#6A4BE0", Dark: "#A891FF"}
	badgeColor  = lipgloss.AdaptiveColor{Light: "#6A4BE0", Dark: "#7D56F4"}
	greenColor  = lipgloss.AdaptiveColor{Light: "#1F7A3A", Dark: "#8FDB9A"}
	blueColor   = lipgloss.AdaptiveColor{Light: "#1D5FC4", Dark: "#89B4FA"}
	amberColor  = lipgloss.AdaptiveColor{Light: "#985200", Dark: "#F9B562"}
	redColor    = lipgloss.AdaptiveColor{Light: "#C02B3C", Dark: "#F38BA8"}
	greyColor   = lipgloss.AdaptiveColor{Light: "#5E6472", Dark: "#9399B2"}
	pinkColor   = lipgloss.AdaptiveColor{Light: "#A8307E", Dark: "#F0A6D8"}
	// Backgrounds of the selected row, of blocks of command output, and of
	// key caps and unselected buttons.
	selectedBg = lipgloss.AdaptiveColor{Light: "#ECE7FC", Dark: "#312A4D"}
	blockBg    = lipgloss.AdaptiveColor{Light: "#EFEFF3", Dark: "#282838"}
	capBg      = lipgloss.AdaptiveColor{Light: "#ECEAF4", Dark: "#313149"}
	noBg       = lipgloss.NoColor{}

	boldStyle   = lipgloss.NewStyle().Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(greyColor)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor)
	greenStyle  = lipgloss.NewStyle().Foreground(greenColor)
	blueStyle   = lipgloss.NewStyle().Foreground(blueColor)
	amberStyle  = lipgloss.NewStyle().Foreground(amberColor)
	redStyle    = lipgloss.NewStyle().Foreground(redColor)
	badgeStyle  = lipgloss.NewStyle().Background(badgeColor).Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	capStyle    = lipgloss.NewStyle().Background(capBg).Foreground(accentColor).Bold(true)
)

const (
	// wideMin is the narrowest pane that holds the list and the panel side by
	// side.
	wideMin = 76
	// defaultCols and defaultRows apply until the first resize arrives.
	defaultCols, defaultRows = 100, 26
	// chromeRows is the header, the space under it, the status line and the
	// hint line.
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
	lookDisconnected = look{"○", "disconnected", mutedStyle}
	lookAsking       = look{"◆", "needs answer", amberStyle}
	progressWords    = map[jobs.Kind]string{jobs.KindConnect: "connecting…", jobs.KindDisconnect: "disconnecting…", jobs.KindRename: "renaming…", jobs.KindForget: "forgetting…"}
)

func jobLook(job jobs.Job) look {
	kind := string(job.Kind)
	switch job.State {
	case jobs.StateQueued:
		return look{"◌", kind + " queued", blueStyle}
	case jobs.StateAwaitingInput:
		return lookAsking
	case jobs.StateSucceeded:
		return look{"✓", kind + " succeeded", greenStyle}
	case jobs.StateFailed:
		return look{"✕", kind + " failed", redStyle}
	case jobs.StateCancelled:
		return look{"⊘", kind + " cancelled", mutedStyle}
	}
	return look{"◐", or(progressWords[job.Kind], kind+"…"), blueStyle}
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

func (m model) cols() int     { return or(m.width, defaultCols) }
func (m model) rows() int     { return or(m.height, defaultRows) }
func (m model) wide() bool    { return m.cols() >= wideMin }
func (m model) bodyRows() int { return max(1, m.rows()-chromeRows) }

// panelRows is the rows inside the panel's border, which a body shorter than
// three rows has no room for.
func (m model) panelRows() int {
	if rows := m.bodyRows(); rows >= 3 {
		return rows - 2
	}
	return m.bodyRows()
}

func (m model) aliasRows() int { return max(1, m.panelRows()-3) }

// listWidth and panelWidth exclude the margins and the gap between them.
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
	return m.cols() - m.listWidth() - 4
}

type hint struct{ key, action string }

// frame lays out every screen the same way: a header with the counts (Herdr
// already sets the pane title into the popup border), the body, a status line, and one line of key hints. Without the
// alternate screen a taller frame would scroll its own top away, so the frame
// is exactly the pane's size and no line is wider than it.
func (m model) frame(summary []string, left, right []string, hints []hint) string {
	w := m.cols()
	head := ""
	for _, part := range summary {
		if lipgloss.Width(head)+2+lipgloss.Width(part) > w-1 {
			break
		}
		head += "  " + part
	}
	head = strings.TrimPrefix(head, " ")
	out := []string{head, ""}
	for i := range m.bodyRows() {
		line := " " + at(left, i)
		if right != nil {
			line = " " + fit(at(left, i), m.listWidth()) + "  " + at(right, i)
		}
		out = append(out, line)
	}
	note := mutedStyle.Render(m.status)
	if err := or(m.failure, or(m.link, m.config)); err != "" {
		note = redStyle.Render("✕ " + oneLine(err))
	}
	out = append(out, " "+ansi.Truncate(note, max(0, w-2), "…"), hintLine(hints, w))
	for i := range out {
		out[i] = ansi.Truncate(out[i], w, "")
	}
	return strings.Join(out[max(0, len(out)-m.rows()):], "\n")
}

// hintLine drops hints from the end, keeping the last, until the line fits:
// hints are listed most important first, and the way out last.
func hintLine(hints []hint, w int) string {
	for {
		parts := make([]string, len(hints))
		for i, h := range hints {
			parts[i] = capStyle.Render(" "+h.key+" ") + " " + mutedStyle.Render(h.action)
		}
		line := " " + strings.Join(parts, "  ")
		if len(hints) <= 1 || lipgloss.Width(line) <= w {
			return line
		}
		hints = append(hints[:len(hints)-2:len(hints)-2], hints[len(hints)-1])
	}
}

// box frames what fill draws in a rounded border of colour, with the title
// set into the top edge, w columns by h rows. Below three rows there is no
// room for a border and fill draws on its own.
func box(w, h int, colour lipgloss.TerminalColor, fill func(w, rows int) (string, []string)) []string {
	if h < 3 {
		_, lines := fill(w, h)
		return lines
	}
	title, lines := fill(w-4, h-2)
	edge := lipgloss.NewStyle().Foreground(colour)
	title = ansi.Truncate(title, max(0, w-6), "…")
	top := edge.Render("╭" + strings.Repeat("─", w-2) + "╮")
	if title != "" {
		top = edge.Render("╭─ ") + edge.Bold(true).Render(title) + edge.Render(" "+strings.Repeat("─", max(0, w-5-lipgloss.Width(title)))+"╮")
	}
	out := []string{top}
	for i := range h - 2 {
		out = append(out, edge.Render("│")+" "+fit(at(lines, i), w-4)+" "+edge.Render("│"))
	}
	return append(out, edge.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

// cell is plain text in a style; row draws cells on one background.
type cell struct {
	text  string
	style lipgloss.Style
}

// row renders cells side by side on bg and pads them to w columns. Each cell
// carries the background itself: a styled string nested in another would
// reset it.
func row(cells []cell, w int, bg lipgloss.TerminalColor) string {
	var b strings.Builder
	used := 0
	for _, c := range cells {
		text := ansi.Truncate(c.text, max(0, w-used), "…")
		used += lipgloss.Width(text)
		b.WriteString(c.style.Background(bg).Render(text))
	}
	b.WriteString(lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", max(0, w-used))))
	return b.String()
}

// selection is the bar, background and label style of a row, which mark the
// selected one.
func selection(selected bool) (cell, lipgloss.TerminalColor, lipgloss.Style) {
	if selected {
		return cell{"▌ ", accentStyle}, selectedBg, boldStyle
	}
	return cell{"  ", lipgloss.NewStyle()}, noBg, lipgloss.NewStyle()
}

func (m model) View() string {
	pw, rows := m.panelWidth(), m.bodyRows()
	conn, selected := m.current()
	var panel []string
	switch {
	case m.dialog != nil:
		panel = box(pw, rows, promptColour(m.dialog.info.Kind), m.promptLines)
	case m.mode == modePick:
		panel = box(pw, rows, accentColor, m.pickLines)
	case m.mode == modeForm:
		panel = box(pw, rows, accentColor, m.formLines)
	case m.mode == modeForget:
		panel = box(pw, rows, accentColor, m.forgetLines)
	case selected && (m.wide() || m.mode == modeDetail):
		l, _ := m.connLook(conn)
		panel = box(pw, rows, l.style.GetForeground(), func(w, rows int) (string, []string) { return conn.Label, m.detailLines(conn, w, rows) })
	case m.wide():
		panel = []string{}
	}
	if !m.wide() && panel != nil {
		return m.frame(m.summary(), panel, nil, m.hints())
	}
	return m.frame(m.summary(), m.listLines(), panel, m.hints())
}

// summary counts the connections by state, each count in its state's colour.
// The header drops counts from the end when it runs out of room.
func (m model) summary() []string {
	active, failed, busy := 0, 0, 0
	for _, c := range m.conns {
		l, _ := m.connLook(c)
		switch l.glyph {
		case "✕":
			failed++
		case "◐", "◌":
			busy++
		}
		if c.Active {
			active++
		}
	}
	parts := []string{count(greenStyle, "●", fmt.Sprintf("%d of %d", active, len(m.conns)), "connected")}
	if n := len(m.waiting()); n > 0 {
		parts = append(parts, count(amberStyle, "◆", fmt.Sprint(n), "needs answer"))
	}
	if failed > 0 {
		parts = append(parts, count(redStyle, "✕", fmt.Sprint(failed), "failed"))
	}
	if busy > 0 {
		parts = append(parts, count(blueStyle, "◐", fmt.Sprint(busy), "busy"))
	}
	return parts
}

func count(style lipgloss.Style, glyph, n, word string) string {
	return style.Render(glyph+" "+n) + " " + mutedStyle.Render(word)
}

func (m model) hints() []hint {
	switch {
	case m.dialog != nil && m.dialog.info.Kind == jobs.PromptConfirm:
		return []hint{{"←→", "choose"}, {"enter", "send"}, {"esc", "later"}}
	case m.dialog != nil:
		return []hint{{"enter", "send"}, {"esc", "later"}}
	case m.typing():
		return m.typingHints()
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

// typingHints puts the waiting question right after the primary action, so a
// narrow hint line keeps it.
func (m model) typingHints() []hint {
	hs := []hint{{"enter", "save"}}
	if m.mode == modePick {
		hs[0].action = map[bool]string{true: "choose", false: "add as typed"}[len(m.filtered()) > 0]
	}
	if len(m.waiting()) > 0 {
		hs = append(hs, hint{"ctrl+o", "answer"})
	}
	if m.mode == modeForm {
		hs = append(hs, hint{"tab", "next field"})
	}
	if m.mode == modeForm && m.field == 3 {
		hs = append(hs, hint{"space", "switch"})
	}
	return append(hs, hint{"esc", "cancel"})
}

// listLines is one row per connection: the selection bar, the state glyph,
// the label and the state word in aligned columns, then the error or the
// target as room allows.
func (m model) listLines() []string {
	if len(m.conns) == 0 {
		return []string{"", mutedStyle.Render(" No connections yet."), mutedStyle.Render(" Press a to add one from ~/.ssh/config.")}
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
		bar, bg, labelStyle := selection(m.offset+i == m.cursor)
		cells := []cell{bar, {l.glyph + " ", l.style}, {fit(c.Label, labelW) + "  ", labelStyle}, {fit(l.word, wordW), l.style}}
		if room := w - labelW - wordW - 8; room >= 6 && failure != "" {
			cells = append(cells, cell{"  " + fit(oneLine(failure), room), redStyle})
		} else if room >= 6 {
			cells = append(cells, cell{"  " + fit(c.Target, room), mutedStyle})
		}
		out = append(out, row(cells, w, bg))
	}
	return out
}

func setting(name, value string, w int) string {
	return mutedStyle.Render(fit(name, keyWidth)) + ansi.Truncate(value, max(0, w-keyWidth), "…")
}

// block is text set apart on its own background, as command output is.
func block(lines []string, w int, style lipgloss.Style) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = row([]cell{{" " + line, style}}, w, blockBg)
	}
	return out
}

// detailLines is the selected connection's panel: its state with the error or
// question behind it, its settings, then the latest job's output as far as it
// fits.
func (m model) detailLines(c daemon.Connection, w, rows int) []string {
	l, failure := m.connLook(c)
	lines := []string{l.style.Bold(true).Render(l.glyph + " " + l.word)}
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
		lines = append(lines, "  "+amberStyle.Render(ansi.Truncate(oneLine(job.Prompt), w-2, "…")))
	}
	lines = append(lines, "", setting("Target", c.Target, w))
	if alias, ok := find(m.aliases, func(a sshconfig.Alias) bool { return a.Name == c.Target }); ok && alias.Host != "" {
		lines = append(lines, setting("Address", endpoint(alias.User, alias.Host, alias.Port), w))
	}
	lines = append(lines, setting("Session", or(c.Session, mutedStyle.Render("default")), w))
	if !hasJob {
		return squeeze(lines, rows)
	}
	// The state line already shows a job that is unfinished or failed.
	if job.State == jobs.StateSucceeded || job.State == jobs.StateCancelled {
		lines = append(lines, setting("Last job", jobLook(job).String(), w))
	}
	if output, room := m.outputs[job.ID], rows-len(lines)-2; room > 0 && len(output) > 0 {
		lines = append(lines, "", mutedStyle.Render("Output"))
		var tail []string
		for _, line := range output[max(0, len(output)-room):] {
			tail = append(tail, oneLine(line))
		}
		lines = append(lines, block(tail, w, lipgloss.NewStyle())...)
	}
	return squeeze(lines, rows)
}

func (m model) pickLines(w, rows int) (string, []string) {
	list, filter := m.filtered(), m.filter
	filter.Width = max(1, w-keyWidth-1)
	lines := []string{
		accentStyle.Render(fit("Filter", keyWidth)) + filter.View(),
		mutedStyle.Render(fmt.Sprintf("%d of %d aliases", len(list), len(m.aliases))),
		"",
	}
	title := "Add a connection"
	if typed := strings.TrimSpace(m.filter.Value()); len(list) == 0 && typed != "" {
		return title, squeeze(append(lines, wrap("No alias in ~/.ssh/config matches. Press enter to add "+typed+" as the ssh target and label.", w)...), rows)
	} else if len(list) == 0 {
		return title, squeeze(append(lines, wrap("No alias in ~/.ssh/config. Press enter to type a target.", w)...), rows)
	}
	nameW := 0
	for _, a := range list {
		nameW = max(nameW, lipgloss.Width(a.Name))
	}
	nameW = min(nameW, 24, w/2)
	offset := max(0, m.aliasCursor-m.aliasRows()+1)
	for i := offset; i < min(offset+m.aliasRows(), len(list)); i++ {
		a := list[i]
		bar, bg, nameStyle := selection(i == m.aliasCursor)
		cells := []cell{bar, {fit(a.Name, nameW) + "  ", nameStyle}}
		addr := endpoint(a.User, a.Host, a.Port)
		if index(m.conns, func(c daemon.Connection) bool { return c.Target == a.Name }) >= 0 {
			cells = append(cells, cell{fit(addr, max(0, w-nameW-11)), mutedStyle}, cell{"  added", greenStyle})
		} else {
			cells = append(cells, cell{addr, mutedStyle})
		}
		lines = append(lines, row(cells, w, bg))
	}
	// In a pane too short for the whole panel the selected alias, which is
	// last in the window, outlasts the filter.
	return title, lines[max(0, len(lines)-rows):]
}

func (m model) formLines(w, rows int) (string, []string) {
	var lines []string
	for i, name := range []string{"Label", "Target", "Session", "Install"} {
		mark, key := "  ", mutedStyle.Render(fit(name, keyWidth-2))
		if i == m.field {
			mark, key = accentStyle.Render("▌ "), accentStyle.Bold(true).Render(fit(name, keyWidth-2))
		}
		value := mutedStyle.Render("[ ]") + " allow installing herdr on the remote"
		if m.install {
			value = accentStyle.Bold(true).Render("[✓]") + " allow installing herdr on the remote"
		}
		if i < 3 {
			in := m.fields[i]
			in.Width = max(1, w-keyWidth-1)
			value = in.View()
		}
		lines = append(lines, mark+key+ansi.Truncate(value, max(0, w-keyWidth), "…"))
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
	lines = append(lines, block(limit(wrap(command, w-1), 2), w, mutedStyle)...)
	return map[bool]string{true: "New connection", false: "Edit connection"}[m.editing == ""], squeeze(lines, rows)
}

func (m model) forgetLines(w, rows int) (string, []string) {
	conn, _ := m.connByID(m.forget)
	note := "It is not connected, so only its saved settings here are removed."
	if conn.Active {
		note = "It is connected, so it is also removed from herdr. Sessions already running on that host keep running."
	}
	lines := []string{setting("Target", conn.Target, w), ""}
	return "Forget " + conn.Label + "?", squeeze(append(lines, wrap(note, w)...), rows)
}

// promptColour is the accent of a question's card, which follows the kind of
// answer it wants.
func promptColour(kind jobs.PromptKind) lipgloss.TerminalColor {
	switch kind {
	case jobs.PromptConfirm:
		return amberColor
	case jobs.PromptSecret:
		return pinkColor
	}
	return accentColor
}

// promptLines is the waiting question: whose it is, what kind of answer it
// wants, as much of the question as fits, and the answer control, which is
// never cut.
func (m model) promptLines(w, rows int) (string, []string) {
	d := m.dialog
	kindStyle := lipgloss.NewStyle().Foreground(promptColour(d.info.Kind))
	who, target := d.jobID, ""
	if conn, ok := m.connByID(d.connID); ok {
		who, target = conn.Label, conn.Target+" · "
	}
	kind := map[jobs.PromptKind]string{jobs.PromptConfirm: "confirmation", jobs.PromptSecret: "password"}[d.info.Kind]
	head := []string{mutedStyle.Render(ansi.Truncate(target, max(0, w-14), "…")) + kindStyle.Render("◆ "+or(kind, "text")), ""}

	control := button("No", !d.yes) + "  " + button("Yes", d.yes)
	if d.info.Kind != jobs.PromptConfirm {
		in := d.input
		in.Width, in.EchoCharacter = max(1, w-3), '•'
		control = kindStyle.Bold(true).Render("› ") + in.View()
	}
	keep := []string{control}
	if d.sending {
		keep = append(keep, mutedStyle.Render("sending…"))
	} else if d.failure != "" {
		keep = append(keep, redStyle.Render(fit(oneLine(d.failure), w)))
	}
	if rows <= len(keep) {
		return who, limit(keep, rows)
	}

	// Blank separators give way first, so the question may use their rows;
	// its end, where the question is, is what stays.
	question := wrap(strings.TrimSpace(ansi.Strip(d.prompt)), w)
	question = question[max(0, len(question)-max(1, rows-len(keep)-nonBlank(head))):]
	top := squeeze(append(head, question...), rows-len(keep))
	return who, squeeze(append(append(top, ""), keep...), rows)
}

// button marks the selected choice with a pointer as well as the fill, so the
// choice still shows on a terminal without colour.
func button(label string, selected bool) string {
	if selected {
		return badgeStyle.Render(" ▸ " + label + "  ")
	}
	return lipgloss.NewStyle().Background(capBg).Foreground(greyColor).Render("   " + label + "  ")
}

// View of the standalone prompt pane: the question alone, in the same frame.
func (m promptModel) View() string {
	body := []string{mutedStyle.Render("Loading the question…")}
	if m.dialog != nil {
		body = box(m.cols()-2, m.bodyRows(), promptColour(m.dialog.info.Kind), m.promptLines)
	} else if err := or(m.failure, or(m.link, m.config)); err != "" {
		body = wrap(err, m.cols()-2)
	}
	var summary []string
	if n := len(m.waiting()); n > 1 {
		summary = []string{count(amberStyle, "◆", fmt.Sprint(n), "questions waiting")}
	}
	m.status = ""
	return m.frame(summary, body, nil, m.hints())
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
