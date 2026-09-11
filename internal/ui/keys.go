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
	case "j", "down":
		m.cursor = clamp(m.cursor+1, len(m.conns))
		m.offset = scroll(m.offset, m.cursor, m.rows())
	case "k", "up":
		m.cursor = clamp(m.cursor-1, len(m.conns))
		m.offset = scroll(m.offset, m.cursor, m.rows())
	case "R":
		m.status = "refreshing"
		return m, m.refresh()
	case "enter":
		if _, ok := m.current(); ok {
			m.screen = screenOutput
		}
	case "a":
		m.screen = screenAliases
		m.aliasCursor, m.aliasOffset = 0, 0
		m.aliasFilter.SetValue("")
		m.aliasFilter.Focus()
		m.status = "reading ~/.ssh/config"
		return m, m.loadAliases()
	case "e", "r":
		if cur, ok := m.current(); ok {
			m.openForm(cur)
		}
	case "d":
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
	case "down", "ctrl+n":
		m.aliasCursor = clamp(m.aliasCursor+1, len(m.filtered()))
		m.aliasOffset = scroll(m.aliasOffset, m.aliasCursor, m.aliasRows())
		return m, nil
	case "up", "ctrl+p":
		m.aliasCursor = clamp(m.aliasCursor-1, len(m.filtered()))
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
		m.screen = screenForm
		return m, nil
	}
	var cmd tea.Cmd
	m.aliasFilter, cmd = m.aliasFilter.Update(msg)
	m.aliasCursor, m.aliasOffset = 0, 0
	return m, cmd
}

func (m model) keyForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList
		return m, nil
	case "tab", "down":
		m.field = (m.field + 1) % 4
		m.focusField()
		return m, nil
	case "shift+tab", "up":
		m.field = (m.field + 3) % 4
		m.focusField()
		return m, nil
	case " ":
		if m.field == 3 {
			m.install = !m.install
			return m, nil
		}
	case "enter":
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
		m.screen = screenList
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
	case "y":
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

	if hasJob && job.State == jobs.StateAwaitingInput {
		switch msg.String() {
		case "esc":
			m.screen = screenList
			return m, nil
		case "enter":
			answer := m.jobInput.Value()
			m.jobInput.SetValue("")
			return m, m.sendInput(job.ID, answer+"\n")
		}
		var cmd tea.Cmd
		m.jobInput.Focus()
		m.jobInput, cmd = m.jobInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc", "q", "enter":
		m.screen = screenList
	case "x":
		if hasJob && !job.State.Terminal() {
			return m, m.cancel(job.ID)
		}
	case "R":
		return m, m.refresh()
	}
	return m, nil
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
