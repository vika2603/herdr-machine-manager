package ui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

type screen int

const (
	screenList screen = iota
	screenAliases
	screenForm
	screenConfirm
	// screenOutput shows what the command behind one connection is doing, and
	// is where an answer it is waiting for is typed. There is no job list: a
	// job belongs to a connection, so its state is shown on that row.
	screenOutput
)

// outputTail is how many lines of a job's output the job view shows.
const outputTail = 12

type listMsg daemon.ListResult
type aliasesMsg daemon.AliasesResult
type jobMsg jobs.Job
type outputMsg struct {
	jobID string
	lines []string
}
type statusMsg string
type failureMsg string
type reconnectMsg struct{}

type model struct {
	ctx    context.Context
	client *herdr.Client

	screen        screen
	width, height int

	conns   []daemon.Connection
	active  []jobs.Job
	allJobs []jobs.Job
	outputs map[string][]string
	cursor  int
	offset  int

	status  string
	failure string

	aliases     []sshconfig.Alias
	aliasCursor int
	aliasOffset int
	aliasFilter textinput.Model

	// editing is the connection the form is editing, empty when it creates one.
	editing                string
	formBack               screen
	label, target, session textinput.Model
	field                  int
	install                bool
	installDefault         bool

	confirm *daemon.Connection

	dialog           *promptDialog
	dismissedPrompts map[string]bool
}

func newModel(ctx context.Context, client *herdr.Client) model {
	input := func(placeholder string) textinput.Model {
		in := textinput.New()
		in.Placeholder = placeholder
		in.Prompt = "› "
		return in
	}
	return model{
		ctx:         ctx,
		client:      client,
		outputs:     map[string][]string{},
		aliasFilter: input("alias or host"),
		label:       input("label shown in the sidebar"),
		target:      input("ssh target"),
		session:     input("remote session (optional)"),
	}
}

func (m model) Init() tea.Cmd {
	// The aliases are loaded up front as well: the list shows the resolved
	// host behind each target.
	return tea.Batch(m.loadList(), m.loadAliases())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.offset = scroll(m.offset, m.cursor, m.rows())
		m.aliasOffset = scroll(m.aliasOffset, m.aliasCursor, m.aliasRows())
		return m, nil

	case listMsg:
		m.conns = msg.Connections
		m.mergeJobs(msg.Jobs)
		m.failure = msg.Error
		if m.cursor >= len(m.conns) {
			m.cursor = max(0, len(m.conns)-1)
		}
		return m, nil

	case aliasesMsg:
		m.aliases = msg.Aliases
		m.status = ""
		if msg.Error != "" {
			m.failure = msg.Error
		}
		return m, nil

	case jobMsg:
		m.mergeJob(jobs.Job(msg))
		return m, nil

	case outputMsg:
		m.outputs[msg.jobID] = append(m.outputs[msg.jobID], msg.lines...)
		if extra := len(m.outputs[msg.jobID]) - 200; extra > 0 {
			m.outputs[msg.jobID] = m.outputs[msg.jobID][extra:]
		}
		return m, nil

	case statusMsg:
		m.status = string(msg)
		m.failure = ""
		return m, nil

	case failureMsg:
		m.failure = string(msg)
		return m, nil

	case reconnectMsg:
		m.status = "reconnecting to the daemon"
		return m, m.loadList()

	case promptReplyMsg:
		if m.dialog != nil && m.dialog.jobID == msg.jobID && m.dialog.prompt == msg.prompt {
			m.dialog.sending = false
			if msg.err != nil {
				m.dialog.failure = msg.err.Error()
			} else {
				m.dismissPrompt()
				m.syncPrompt()
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// forgetOutputs drops the output buffered for jobs the daemon no longer keeps.
func (m *model) forgetOutputs() {
	known := make(map[string]bool, len(m.allJobs))
	for _, job := range m.allJobs {
		known[job.ID] = true
	}
	for id := range m.outputs {
		if !known[id] {
			delete(m.outputs, id)
		}
	}
}

// mergeJobs replaces the known jobs with the set the daemon keeps. Its tails
// come from the daemon, which saw everything, so they replace what the popup
// buffered: a reconnect would otherwise keep showing what it had before the
// stream dropped.
func (m *model) mergeJobs(list []jobs.Job) {
	m.allJobs = list
	m.active = activeJobs(list)
	for _, job := range list {
		if len(job.Tail) > 0 {
			m.outputs[job.ID] = job.Tail
		}
	}
	m.forgetOutputs()
	m.syncPrompt()
}

// activeJobs is the set of jobs still in flight.
func activeJobs(list []jobs.Job) []jobs.Job {
	var out []jobs.Job
	for _, job := range list {
		if !job.State.Terminal() {
			out = append(out, job)
		}
	}
	return out
}

func (m *model) mergeJob(job jobs.Job) {
	replace := func(list []jobs.Job) []jobs.Job {
		for i := range list {
			if list[i].ID == job.ID {
				list[i] = job
				return list
			}
		}
		return append(list, job)
	}
	m.allJobs = replace(m.allJobs)
	m.forgetOutputs()
	m.active = activeJobs(m.allJobs)

	switch job.State {
	case jobs.StateFailed:
		m.failure = job.Title + ": " + job.Err
	case jobs.StateSucceeded:
		m.status = job.Title + " finished"
	}
	m.syncPrompt()
}

func (m model) current() (daemon.Connection, bool) {
	if m.cursor < 0 || m.cursor >= len(m.conns) {
		return daemon.Connection{}, false
	}
	return m.conns[m.cursor], true
}

// lastJobFor returns the most recent job of a connection, running or not.
func (m model) lastJobFor(id string) (jobs.Job, bool) {
	for i := len(m.allJobs) - 1; i >= 0; i-- {
		if m.allJobs[i].ConnID == id {
			return m.allJobs[i], true
		}
	}
	return jobs.Job{}, false
}

// filtered narrows the alias list by the substring typed into the filter.
func (m model) filtered() []sshconfig.Alias {
	needle := strings.ToLower(strings.TrimSpace(m.aliasFilter.Value()))
	if needle == "" {
		return m.aliases
	}
	var out []sshconfig.Alias
	for _, a := range m.aliases {
		if strings.Contains(strings.ToLower(a.Name), needle) || strings.Contains(strings.ToLower(a.Host), needle) {
			out = append(out, a)
		}
	}
	return out
}

// jobFor reports the job in flight for a connection, so a row can show what is
// happening to it instead of its stored state.
func (m model) jobFor(id string) (jobs.Job, bool) {
	for _, j := range m.active {
		if j.ConnID == id {
			return j, true
		}
	}
	return jobs.Job{}, false
}

// rows uses the same footer height as page so paging keeps the cursor visible.
func (m model) rows() int {
	if m.height <= 0 {
		return 10
	}
	return max(1, m.bodyRows(listHelp))
}

// aliasRows is the picker's window, which also carries the filter line.
func (m model) aliasRows() int {
	if m.height <= 0 {
		return 9
	}
	return max(1, m.bodyRows(aliasHelp)-1)
}

// scroll keeps the cursor inside the visible window.
func scroll(offset, cursor, rows int) int {
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+rows {
		return cursor - rows + 1
	}
	return offset
}

// aliasFor reports the ssh alias a target points at, so a row can show where
// it actually connects.
func (m model) aliasFor(target string) (sshconfig.Alias, bool) {
	for _, a := range m.aliases {
		if a.Name == target {
			return a, true
		}
	}
	return sshconfig.Alias{}, false
}

func clamp(i, length int) int {
	if length == 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= length {
		return length - 1
	}
	return i
}
