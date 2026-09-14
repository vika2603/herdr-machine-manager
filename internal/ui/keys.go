package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if m.dialog != nil {
		return m.keyPrompt(msg)
	}
	switch m.screen {
	case screenList:
		return m.keyList(msg)
	case screenAliases:
		return m.keyAliases(msg)
	case screenForm:
		return m.keyForm(msg)
	case screenConfirm:
		return m.keyConfirm(msg)
	case screenOutput:
		return m.keyOutput(msg)
	}
	return m, nil
}

func (m model) keyList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "j", "down", "ctrl+n", "k", "up", "ctrl+p", "pgdown", "pgup", "home", "end":
		m.cursor = navigate(msg.String(), m.cursor, len(m.conns), m.rows())
		m.offset = scroll(m.offset, m.cursor, m.rows())
	case "R", "f5", "ctrl+r":
		m.status = "refreshing"
		return m, m.refresh()
	case "enter":
		if conn, ok := m.current(); ok {
			m.screen = screenOutput
			if job, found := m.jobFor(conn.ID); found && job.State == jobs.StateAwaitingInput {
				m.openPrompt(job)
			}
		}
	case "a", "ctrl+a", "insert":
		m.screen = screenAliases
		m.aliasCursor, m.aliasOffset = 0, 0
		m.aliasFilter.SetValue("")
		m.aliasFilter.Focus()
		m.status = "reading ~/.ssh/config"
		return m, m.loadAliases()
	case "e", "r", "ctrl+e", "f2":
		if cur, ok := m.current(); ok {
			m.openForm(cur)
		}
	case "d", "delete":
		if cur, ok := m.current(); ok {
			conn := cur
			m.confirm = &conn
			m.screen = screenConfirm
		}
	case " ":
		if cur, ok := m.current(); ok {
			if cur.Active {
				return m, m.act(ipc.MethodDisconnect, cur.ID, false)
			}
			return m, m.act(ipc.MethodConnect, cur.ID, true)
		}
	}
	return m, nil
}

// openForm fills the form from an existing connection.
func (m *model) openForm(conn daemon.Connection) {
	m.formBack = m.screen
	m.editing = conn.ID
	m.label.SetValue(conn.Label)
	m.target.SetValue(conn.Target)
	m.session.SetValue(conn.Session)
	m.install = m.installDefault
	m.field = 0
	m.focusField()
	m.screen = screenForm
}

func (m model) keyAliases(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList
		return m, nil
	case "down", "ctrl+n", "up", "ctrl+p", "pgdown", "pgup", "ctrl+home", "ctrl+end":
		m.aliasCursor = navigate(msg.String(), m.aliasCursor, len(m.filtered()), m.aliasRows())
		m.aliasOffset = scroll(m.aliasOffset, m.aliasCursor, m.aliasRows())
		return m, nil
	case "enter":
		list := m.filtered()
		if len(list) == 0 {
			return m, nil
		}
		alias := list[min(m.aliasCursor, len(list)-1)]
		m.editing = ""
		m.label.SetValue(alias.Name)
		m.target.SetValue(alias.Name)
		m.session.SetValue("")
		m.install = m.installDefault
		m.field = 0
		m.focusField()
		m.formBack = screenList
		m.screen = screenForm
		return m, nil
	}
	previousFilter := m.aliasFilter.Value()
	var cmd tea.Cmd
	m.aliasFilter, cmd = m.aliasFilter.Update(msg)
	if m.aliasFilter.Value() != previousFilter {
		m.aliasCursor, m.aliasOffset = 0, 0
	}
	return m, cmd
}

func (m model) keyForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = m.formBack
		return m, nil
	case "tab", "down", "ctrl+n":
		m.field = (m.field + 1) % 4
		m.focusField()
		return m, nil
	case "shift+tab", "up", "ctrl+p":
		m.field = (m.field + 3) % 4
		m.focusField()
		return m, nil
	case " ":
		if m.field == 3 {
			m.install = !m.install
			return m, nil
		}
	case "enter", "ctrl+s":
		label, target := strings.TrimSpace(m.label.Value()), strings.TrimSpace(m.target.Value())
		if label == "" || target == "" {
			m.failure = "a label and an ssh target are required"
			return m, nil
		}
		params := ipc.SaveParams{
			ID:      m.editing,
			Label:   label,
			Target:  target,
			Session: strings.TrimSpace(m.session.Value()),
			Install: m.install,
		}
		m.screen = m.formBack
		return m, m.save(params)
	}
	var cmd tea.Cmd
	switch m.field {
	case 0:
		m.label, cmd = m.label.Update(msg)
	case 1:
		m.target, cmd = m.target.Update(msg)
	case 2:
		m.session, cmd = m.session.Update(msg)
	}
	return m, cmd
}

func (m model) keyConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		target := m.confirm
		m.screen = screenList
		m.confirm = nil
		if target == nil {
			return m, nil
		}
		return m, m.act(ipc.MethodForget, target.ID, false)
	case "n", "esc", "q":
		m.screen = screenList
		m.confirm = nil
	}
	return m, nil
}

// keyOutput drives the per-connection output view: it forwards typing to a
// command that is waiting for an answer, and otherwise only navigates.
func (m model) keyOutput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	conn, ok := m.current()
	if !ok {
		m.screen = screenList
		return m, nil
	}
	job, hasJob := m.lastJobFor(conn.ID)
	if msg.String() == "ctrl+x" {
		if hasJob && !job.State.Terminal() {
			return m, m.cancel(job.ID)
		}
		return m, nil
	}
	if hasJob && job.State == jobs.StateAwaitingInput && msg.String() == "enter" {
		m.openPrompt(job)
		return m, nil
	}

	switch msg.String() {
	case "esc", "q", "enter":
		m.screen = screenList
	case "x":
		if hasJob && !job.State.Terminal() {
			return m, m.cancel(job.ID)
		}
	case "e", "r", "ctrl+e", "f2":
		m.openForm(conn)
	case "R", "f5", "ctrl+r":
		return m, m.refresh()
	}
	return m, nil
}

// navigate applies list navigation without changing text-input key bindings.
func navigate(key string, cursor, length, rows int) int {
	switch key {
	case "j", "down", "ctrl+n":
		cursor++
	case "k", "up", "ctrl+p":
		cursor--
	case "pgdown":
		cursor += rows
	case "pgup":
		cursor -= rows
	case "home", "ctrl+home":
		cursor = 0
	case "end", "ctrl+end":
		cursor = length - 1
	}
	return clamp(cursor, length)
}

func (m *model) focusField() {
	m.label.Blur()
	m.target.Blur()
	m.session.Blur()
	switch m.field {
	case 0:
		m.label.Focus()
	case 1:
		m.target.Focus()
	case 2:
		m.session.Focus()
	}
}
