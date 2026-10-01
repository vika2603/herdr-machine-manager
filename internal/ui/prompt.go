package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

// promptDialog is the question a waiting job asked. It is keyed by job and
// prompt text, so a background update of the same job keeps the draft.
type promptDialog struct {
	jobID, prompt, connID string
	info                  jobs.PromptInfo
	input                 textinput.Model
	yes, sending          bool
	failure               string
}

type promptReplyMsg struct {
	jobID, prompt string
	err           error
}

func promptKey(jobID, prompt string) string { return jobID + "\x00" + prompt }

// waiting is every question currently asked, in queue order.
func (m model) waiting() []jobs.Job {
	var out []jobs.Job
	for _, j := range m.jobs {
		if j.State == jobs.StateAwaitingInput {
			out = append(out, j)
		}
	}
	return out
}

// syncPrompt follows the waiting questions: it closes a dialog whose question
// is gone and opens the next question not dismissed. One dialog owns focus;
// others wait their turn. While the user types in the picker or the form, the
// next question waits until they leave it or open it with ctrl+o, so their
// keystrokes do not land in the answer.
func (m *model) syncPrompt() {
	asked := map[string]bool{}
	var next []jobs.Job
	for _, j := range m.waiting() {
		asked[promptKey(j.ID, j.Prompt)] = true
		if !m.dismissed[promptKey(j.ID, j.Prompt)] {
			next = append(next, j)
		}
	}
	for key := range m.dismissed {
		if !asked[key] {
			delete(m.dismissed, key)
		}
	}
	if m.dialog != nil && asked[promptKey(m.dialog.jobID, m.dialog.prompt)] {
		return
	}
	m.dialog = nil
	if len(next) > 0 && !m.typing() {
		m.openPrompt(next[0])
	}
}

func (m model) typing() bool { return m.mode == modePick || m.mode == modeForm }

func (m *model) openPrompt(job jobs.Job) {
	input := textinput.New()
	input.Prompt = ""
	input.Focus()
	info := jobs.DescribePrompt(job.Prompt)
	if info.Kind == jobs.PromptSecret {
		input.EchoMode = textinput.EchoPassword
	}
	m.dialog = &promptDialog{jobID: job.ID, prompt: job.Prompt, connID: job.ConnID, info: info, input: input}
}

// dismissPrompt sets the open question aside until the user opens it again or
// a different question arrives, then moves on to the next one.
func (m *model) dismissPrompt() {
	if m.dismissed == nil {
		m.dismissed = map[string]bool{}
	}
	m.dismissed[promptKey(m.dialog.jobID, m.dialog.prompt)] = true
	m.dialog = nil
	m.syncPrompt()
}

func (m model) keyPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.dialog
	if d.sending {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.dismissPrompt()
		return m, nil
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
		case "left":
			d.yes = false
		case "right":
			d.yes = true
		}
		return m, nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return m, cmd
}

// promptModel is the standalone prompt pane the daemon opens when no manager
// is: it shows only the questions and closes after the last is answered or set
// aside.
type promptModel struct {
	model
	loaded bool
}

func (m promptModel) Init() tea.Cmd { return m.call(ipc.MethodList) }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && m.dialog == nil {
		if key.String() == "esc" || key.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	next, cmd := m.model.Update(msg)
	m.model = next.(model)
	if _, ok := msg.(listMsg); ok {
		m.loaded = true
	}
	if m.loaded && m.dialog == nil {
		if cmd != nil {
			return m, tea.Sequence(cmd, tea.Quit)
		}
		return m, tea.Quit
	}
	return m, cmd
}
