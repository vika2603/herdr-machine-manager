package ui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

// mode is what the panel beside the connection list shows. In a pane too
// narrow for both, the panel replaces the list.
type mode int

const (
	modeList mode = iota
	// modeDetail opens the selected connection's panel on its own, which only
	// a narrow pane needs: a wide one shows it beside the list.
	modeDetail
	modePick
	modeForm
	modeForget
)

// outputLimit bounds the output buffered per job, as the daemon does.
const outputLimit = 200

// listMsg is a list the subscription delivered, in order with the job events
// around it; replyMsg is the reply to a request, which travels apart from the
// subscription and may be older than events that arrived before it. asked is
// the number of subscription messages the model had received when it sent
// the request.
type listMsg daemon.ListResult
type replyMsg struct {
	daemon.ListResult
	asked int
}
type aliasesMsg daemon.AliasesResult
type jobMsg jobs.Job
type outputMsg struct {
	jobID string
	lines []string
}
type statusMsg string
type failureMsg string

// linkMsg is a failure to reach the daemon, cleared by the next list it sends.
type linkMsg string
type reconnectMsg struct{}

type model struct {
	ctx    context.Context
	client *herdr.Client

	width, height int
	mode          mode
	// back is the mode the picker, the form and the forget confirmation
	// return to.
	back mode

	conns   []daemon.Connection
	jobs    []jobs.Job
	outputs map[string][]string
	cursor  int
	offset  int

	// failure is a rejected request, cleared by the next accepted one; link is
	// a failure to reach the daemon or the error it reports; config is the
	// config file's error, which lasts until the popup is reopened.
	status, failure, link, config string

	aliases     []sshconfig.Alias
	aliasCursor int
	filter      textinput.Model

	// editing is the connection the form edits, empty when it creates one.
	editing        string
	fields         [3]textinput.Model // label, target, session
	field          int                // 0-2 the inputs, 3 the install switch
	install        bool
	installDefault bool

	forget string

	// received counts the messages the subscription delivered; listed,
	// jobSeen and outSeen record the count at the last list and at the last
	// update and output of each job, so a reply never undoes what arrived
	// after it was requested. revision is that of the last list applied, and
	// loaded tells whether one was.
	received, listed, revision int
	jobSeen, outSeen           map[string]int
	loaded                     bool

	dialog    *promptDialog
	dismissed map[string]bool
}

func newModel(ctx context.Context, client *herdr.Client) model {
	input := func(placeholder string) textinput.Model {
		in := textinput.New()
		in.Placeholder, in.Prompt, in.PlaceholderStyle = placeholder, "", mutedStyle
		return in
	}
	return model{
		ctx:     ctx,
		client:  client,
		outputs: map[string][]string{},
		jobSeen: map[string]int{},
		outSeen: map[string]int{},
		filter:  input("type to filter"),
		fields:  [3]textinput.Model{input("shown in the sidebar"), input("ssh alias or user@host"), input("default")},
	}
}

func (m model) Init() tea.Cmd {
	// Aliases are loaded up front: the panel shows the address behind a target.
	return tea.Batch(m.call(ipc.MethodList), m.loadAliases())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.wide() {
			m.mode, m.back = map[bool]mode{true: modeList, false: m.mode}[m.mode == modeDetail], modeList
		}
	case listMsg:
		m.received++
		m.listed = m.received
		m.applyList(daemon.ListResult(msg), m.received)
	case replyMsg:
		// A list the subscription delivered after the request is at least
		// as new as the reply, and every later change follows it as an event.
		if m.listed <= msg.asked {
			m.applyList(msg.ListResult, msg.asked)
		}
	case aliasesMsg:
		m.aliases = msg.Aliases
		if msg.Error != "" {
			m.link = msg.Error
		}
	case jobMsg:
		m.received++
		m.jobSeen[msg.ID] = m.received
		m.mergeJob(jobs.Job(msg))
	case outputMsg:
		m.received++
		m.outSeen[msg.jobID] = m.received
		out := append(m.outputs[msg.jobID], msg.lines...)
		m.outputs[msg.jobID] = out[max(0, len(out)-outputLimit):]
	case statusMsg:
		m.status, m.failure = string(msg), ""
	case failureMsg:
		m.failure = string(msg)
	case linkMsg:
		m.link = string(msg)
	case reconnectMsg:
		// A new subscription may reach a restarted daemon, whose revisions
		// start again; replies requested before it are dropped.
		m.received++
		m.listed, m.revision = m.received, 0
		return m, m.call(ipc.MethodList)
	case promptReplyMsg:
		if d := m.dialog; d != nil && d.jobID == msg.jobID && d.prompt == msg.prompt {
			d.sending = false
			if msg.err != nil {
				d.failure = msg.err.Error()
			} else {
				m.dismissPrompt()
			}
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// applyList takes the connections and jobs of a list unless a list with a
// higher revision was applied. The list's jobs replace the known ones, and its
// tails what the popup buffered: the daemon saw everything, including what
// streamed while this popup was disconnected from it. A job update or output
// the subscription delivered after asked is newer than the list and stays.
func (m *model) applyList(list daemon.ListResult, asked int) {
	if list.Revision < m.revision {
		return
	}
	m.revision, m.loaded = list.Revision, true
	c, _ := m.current()
	selected := c.ID
	m.conns, m.link = list.Connections, list.Error
	m.cursor = clamp(m.cursor, len(m.conns))
	if i := index(m.conns, func(c daemon.Connection) bool { return c.ID == selected }); i >= 0 {
		m.cursor = i
	}

	merged := append([]jobs.Job(nil), list.Jobs...)
	for i, job := range merged {
		if current, ok := find(m.jobs, func(j jobs.Job) bool { return j.ID == job.ID }); ok && m.jobSeen[job.ID] > asked {
			merged[i] = current
		}
	}
	for _, job := range m.jobs {
		if m.jobSeen[job.ID] > asked && index(merged, func(j jobs.Job) bool { return j.ID == job.ID }) < 0 {
			merged = append(merged, job)
		}
	}
	m.jobs = merged
	for _, job := range merged {
		if len(job.Tail) > 0 && m.outSeen[job.ID] <= asked {
			m.outputs[job.ID] = job.Tail
		}
	}
	m.forgetOutputs()
}

func (m *model) mergeJob(job jobs.Job) {
	if i := index(m.jobs, func(j jobs.Job) bool { return j.ID == job.ID }); i >= 0 {
		m.jobs[i] = job
	} else {
		m.jobs = append(m.jobs, job)
	}
	switch job.State {
	case jobs.StateSucceeded:
		m.status = job.Title + " finished"
	case jobs.StateFailed:
		m.status = job.Title + " failed"
	}
	m.forgetOutputs()
}

// forgetOutputs drops what is kept about jobs the daemon no longer keeps,
// then lets the open question follow the jobs.
func (m *model) forgetOutputs() {
	gone := func(id string) bool { return index(m.jobs, func(j jobs.Job) bool { return j.ID == id }) < 0 }
	for id := range m.outputs {
		if gone(id) {
			delete(m.outputs, id)
		}
	}
	for _, seen := range []map[string]int{m.jobSeen, m.outSeen} {
		for id := range seen {
			if gone(id) {
				delete(seen, id)
			}
		}
	}
	m.syncPrompt()
}

// pendingJob is the job a connection is busy with. The daemon lists jobs
// oldest first and runs a connection's jobs in order, so the first unfinished
// one is running, waiting for an answer, or next in line.
func (m model) pendingJob(connID string) (jobs.Job, bool) {
	return find(m.jobs, func(j jobs.Job) bool { return j.ConnID == connID && !j.State.Terminal() })
}

// lastJob is the newest job of a connection, finished or not.
func (m model) lastJob(connID string) (jobs.Job, bool) {
	for i := len(m.jobs) - 1; i >= 0; i-- {
		if m.jobs[i].ConnID == connID {
			return m.jobs[i], true
		}
	}
	return jobs.Job{}, false
}

func (m model) current() (daemon.Connection, bool) {
	if m.cursor >= len(m.conns) {
		return daemon.Connection{}, false
	}
	return m.conns[m.cursor], true
}

func (m model) connByID(id string) (daemon.Connection, bool) {
	return find(m.conns, func(c daemon.Connection) bool { return c.ID == id })
}

// filtered narrows the aliases to those whose name or host contains the filter.
func (m model) filtered() []sshconfig.Alias {
	needle := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var out []sshconfig.Alias
	for _, a := range m.aliases {
		if strings.Contains(strings.ToLower(a.Name), needle) || strings.Contains(strings.ToLower(a.Host), needle) {
			out = append(out, a)
		}
	}
	return out
}

func index[T any](items []T, match func(T) bool) int {
	for i, item := range items {
		if match(item) {
			return i
		}
	}
	return -1
}

func find[T any](items []T, match func(T) bool) (T, bool) {
	var zero T
	if i := index(items, match); i >= 0 {
		return items[i], true
	}
	return zero, false
}

// scroll keeps the cursor inside a window of rows starting at offset.
func scroll(offset, cursor, rows int) int {
	return min(max(offset, cursor-rows+1), cursor)
}

func clamp(i, length int) int {
	return max(0, min(i, length-1))
}
