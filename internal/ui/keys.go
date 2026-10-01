package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit
	case m.dialog != nil:
		return m.keyPrompt(msg)
	case msg.String() == "ctrl+o" && m.typing():
		if waiting := m.waiting(); len(waiting) > 0 {
			m.openPrompt(waiting[0])
		}
		return m, nil
	case m.mode == modePick:
		return m.keyPick(msg)
	case m.mode == modeForm:
		return m.keyForm(msg)
	case m.mode == modeForget:
		return m.keyForget(msg)
	}
	return m.keyBrowse(msg)
}

// keyBrowse handles the list and the detail panel, which act on the same
// selection.
func (m model) keyBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	conn, ok := m.current()
	job, busy := m.pendingJob(conn.ID)
	switch key := msg.String(); {
	case key == "esc" && m.mode == modeDetail:
		m.mode = modeList
	case key == "esc":
		return m, tea.Quit
	case strings.Contains(" up k down j pgup pgdown home end ", " "+key+" "):
		m.cursor = navigate(key, m.cursor, len(m.conns), m.bodyRows())
		m.offset = scroll(m.offset, m.cursor, m.bodyRows())
	case key == "a":
		m.back, m.mode, m.aliasCursor = m.mode, modePick, 0
		m.filter.SetValue("")
		m.filter.Focus()
		return m, m.loadAliases()
	case key == "r":
		return m, m.call(ipc.MethodRefresh)
	case !ok:
	case key == "enter":
		// In a narrow pane the details open under the question, so that
		// setting it aside leaves them reachable.
		if !m.wide() {
			m.mode = modeDetail
		}
		if busy && job.State == jobs.StateAwaitingInput {
			m.openPrompt(job)
		}
	case key == " " && busy:
		// The job in flight decides the state; cancel it to change course.
	case key == " " && conn.Active:
		return m, m.request(ipc.MethodDisconnect, ipc.ConnectionTarget{ID: conn.ID}, "disconnect "+conn.Label+" queued")
	case key == " ":
		return m, m.request(ipc.MethodConnect, ipc.ConnectionTarget{ID: conn.ID, Install: true}, "connect "+conn.Label+" queued")
	case key == "e":
		m.back = m.mode
		m.openForm(conn.ID, conn.Label, conn.Target, conn.Session)
	case key == "d":
		m.back, m.mode, m.forget = m.mode, modeForget, conn.ID
	case key == "x" && busy:
		return m, m.request(ipc.MethodJobCancel, ipc.JobTarget{JobID: job.ID}, "cancelling "+job.Title)
	}
	return m, nil
}

func (m model) keyPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.filtered()
	switch key := msg.String(); key {
	case "esc":
		m.mode = m.back
		m.syncPrompt()
		return m, nil
	case "up", "down", "pgup", "pgdown":
		m.aliasCursor = navigate(key, m.aliasCursor, len(list), m.aliasRows())
		return m, nil
	case "enter":
		if len(list) > 0 {
			alias := list[clamp(m.aliasCursor, len(list))]
			m.openForm("", alias.Name, alias.Name, "")
		} else {
			// A target missing from ~/.ssh/config, such as user@host, is typed
			// into the filter.
			typed := strings.TrimSpace(m.filter.Value())
			m.openForm("", typed, typed, "")
		}
		return m, nil
	}
	before := m.filter.Value()
	var cmd tea.Cmd
	if m.filter, cmd = m.filter.Update(msg); m.filter.Value() != before {
		m.aliasCursor = 0
	}
	return m, cmd
}

func (m *model) openForm(id, label, target, session string) {
	m.mode, m.editing, m.install = modeForm, id, m.installDefault
	for i, value := range []string{label, target, session} {
		m.fields[i].SetValue(value)
		m.fields[i].CursorEnd()
	}
	m.focus(0)
}

func (m *model) focus(field int) {
	m.field = (field + 4) % 4
	for i := range m.fields {
		if i == m.field {
			m.fields[i].Focus()
		} else {
			m.fields[i].Blur()
		}
	}
}

func (m model) keyForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch key := msg.String(); {
	case key == "esc":
		m.mode = m.back
		m.syncPrompt()
	case key == "tab" || key == "down":
		m.focus(m.field + 1)
	case key == "shift+tab" || key == "up":
		m.focus(m.field - 1)
	case key == "enter":
		if p := m.formParams(); p.Label == "" || p.Target == "" {
			m.failure = "a label and an ssh target are required"
		} else {
			m.mode = m.back
			m.syncPrompt()
			return m, m.request(ipc.MethodSave, p, "saved "+p.Label)
		}
	case m.field == 3:
		if key == " " {
			m.install = !m.install
		}
	default:
		m.fields[m.field], cmd = m.fields[m.field].Update(msg)
	}
	return m, cmd
}

func (m model) formParams() ipc.SaveParams {
	return ipc.SaveParams{
		ID:      m.editing,
		Label:   strings.TrimSpace(m.fields[0].Value()),
		Target:  strings.TrimSpace(m.fields[1].Value()),
		Session: strings.TrimSpace(m.fields[2].Value()),
		Install: m.install,
	}
}

// reconnects reports the active connection a save would disconnect and
// connect again, which the daemon does when its target or session changes.
func (m model) reconnects() (daemon.Connection, bool) {
	conn, ok := m.connByID(m.editing)
	p := m.formParams()
	return conn, ok && conn.Active && (conn.Target != p.Target || conn.Session != p.Session)
}

func (m model) keyForget(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = m.back
	case "enter":
		m.mode = modeList
		if conn, ok := m.connByID(m.forget); ok {
			return m, m.request(ipc.MethodForget, ipc.ConnectionTarget{ID: conn.ID}, "forget "+conn.Label+" queued")
		}
	}
	return m, nil
}

// navigate moves a cursor through length items shown rows at a time.
func navigate(key string, cursor, length, rows int) int {
	switch key {
	case "down", "j":
		cursor++
	case "up", "k":
		cursor--
	case "pgdown":
		cursor += rows
	case "pgup":
		cursor -= rows
	case "home":
		cursor = 0
	case "end":
		cursor = length - 1
	}
	return clamp(cursor, length)
}
