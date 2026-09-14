package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

type promptDialog struct {
	jobID, prompt, title string
	info                 jobs.PromptInfo
	input                textinput.Model
	yes, sending         bool
	failure              string
}

type promptReplyMsg struct {
	jobID, prompt string
	err           error
}

func promptKey(id, prompt string) string { return id + "\x00" + prompt }

// syncPrompt handles both live job events and the snapshot loaded after the
// manager reopens. One dialog owns focus; additional questions wait their turn.
func (m *model) syncPrompt() {
	waiting := make(map[string]bool)
	for _, job := range m.active {
		if job.State == jobs.StateAwaitingInput {
			waiting[promptKey(job.ID, job.Prompt)] = true
		}
	}
	for key := range m.dismissedPrompts {
		if !waiting[key] {
			delete(m.dismissedPrompts, key)
		}
	}
	if m.dialog != nil && !waiting[promptKey(m.dialog.jobID, m.dialog.prompt)] {
		m.dialog.input.SetValue("")
		m.dialog = nil
	}
	if m.dialog != nil {
		return
	}
	for _, job := range m.active {
		if job.State == jobs.StateAwaitingInput && !m.dismissedPrompts[promptKey(job.ID, job.Prompt)] {
			m.openPrompt(job)
			return
		}
	}
}

func (m *model) openPrompt(job jobs.Job) {
	input := textinput.New()
	input.Prompt = "› "
	input.Focus()
	info := jobs.DescribePrompt(job.Prompt)
	if info.Kind == jobs.PromptSecret {
		input.EchoMode = textinput.EchoPassword
	}
	title := job.Title
	for _, conn := range m.conns {
		if conn.ID == job.ConnID {
			title = conn.Label + " · " + conn.Target
			break
		}
	}
	m.dialog = &promptDialog{jobID: job.ID, prompt: job.Prompt, title: title, info: info, input: input}
}

func (m *model) dismissPrompt() {
	if m.dialog == nil {
		return
	}
	if m.dismissedPrompts == nil {
		m.dismissedPrompts = make(map[string]bool)
	}
	m.dismissedPrompts[promptKey(m.dialog.jobID, m.dialog.prompt)] = true
	m.dialog.input.SetValue("")
	m.dialog = nil
}

func (m model) keyPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.dialog
	if d.sending {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.dismissPrompt()
		m.syncPrompt()
		return m, nil
	case "ctrl+x":
		id := d.jobID
		m.dismissPrompt()
		m.syncPrompt()
		return m, m.cancel(id)
	case "enter":
		answer := d.input.Value()
		if d.info.Kind == jobs.PromptConfirm {
			answer = d.info.No
			if d.yes {
				answer = d.info.Yes
			}
		}
		d.sending, d.failure = true, ""
		d.input.SetValue("")
		id, prompt := d.jobID, d.prompt
		return m, func() tea.Msg {
			err := m.client.Call(m.ctx, ipc.MethodJobInput, ipc.InputParams{JobID: id, Data: answer + "\n"}, nil)
			return promptReplyMsg{jobID: id, prompt: prompt, err: err}
		}
	}
	if d.info.Kind == jobs.PromptConfirm {
		switch msg.String() {
		case "tab", "shift+tab", "left", "right", "up", "down", "ctrl+n", "ctrl+p", " ":
			d.yes = !d.yes
		}
		return m, nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return m, cmd
}

func (m model) viewPrompt(base string) string {
	w := min(58, max(1, m.cols()-6))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("4")).Padding(0, 1).Width(w + 2).Render(m.promptContent(w))
	return overlayPrompt(base, box, m.cols(), m.height)
}

func (m model) promptContent(w int) string {
	d := m.dialog
	title := "Input required"
	switch d.info.Kind {
	case jobs.PromptConfirm:
		title = "Confirmation required"
	case jobs.PromptSecret:
		title = "Password required"
	}
	wrap := lipgloss.NewStyle().Width(w).MaxWidth(w)
	content := []string{titleStyle.Render(title), dimStyle.Render(ansi.Truncate(d.title, w, "…")), ""}
	question := strings.Split(wrap.Render(ansi.Strip(d.prompt)), "\n")
	// Long command output can precede a question. Keep its tail and reserve
	// the controls even when the popup is short.
	limit := 4
	if m.height > 0 {
		limit = max(1, m.height-11)
	}
	if len(question) > limit {
		question = question[len(question)-limit:]
	}
	content = append(content, question...)
	content = append(content, "")
	if d.info.Kind == jobs.PromptConfirm {
		no, yes := "  No  ", "  Yes  "
		selected := lipgloss.NewStyle().Reverse(true).Bold(true)
		if d.yes {
			yes = selected.Render(yes)
		} else {
			no = selected.Render(no)
		}
		content = append(content, no+"   "+yes)
	} else {
		input := d.input
		input.Width = max(1, w-2)
		content = append(content, input.View())
	}
	hint := "Enter submit · Esc later"
	if d.info.Kind == jobs.PromptConfirm {
		hint = "Tab select · Enter confirm · Esc later"
	}
	if d.sending {
		hint = "Sending…"
	}
	if d.failure != "" {
		content = append(content, errStyle.Render(ansi.Truncate(d.failure, w, "…")))
	}
	content = append(content, "", dimStyle.Render(ansi.Truncate(hint, w, "")))
	return strings.Join(content, "\n")
}

// Compose the modal onto the existing frame without replacing its screen or
// changing its selection. ANSI-aware slicing keeps terminal columns aligned.
func overlayPrompt(base, box string, width, height int) string {
	background := strings.Split(base, "\n")
	if height <= 0 {
		height = max(len(background), lipgloss.Height(box))
	}
	for len(background) < height {
		background = append(background, "")
	}
	background = background[:height]
	foreground := strings.Split(box, "\n")
	x := max(0, (width-lipgloss.Width(box))/2)
	y := max(0, (height-len(foreground))/2)
	for i, line := range foreground {
		if y+i >= height {
			break
		}
		under := background[y+i]
		under += strings.Repeat(" ", max(0, width-lipgloss.Width(under)))
		background[y+i] = ansi.Cut(under, 0, x) + line + ansi.Cut(under, x+lipgloss.Width(line), width)
		background[y+i] = ansi.Truncate(background[y+i], width, "")
	}
	return strings.Join(background, "\n")
}
