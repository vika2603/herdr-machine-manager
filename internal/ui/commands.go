// The requests the TUI makes. Each returns a tea.Cmd that calls the daemon on
// its own goroutine, so no keystroke waits on a request.

package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
)

// call requests the connection list, through connections.list or the
// connections.refresh that re-reads herdr first.
func (m model) call(method string) tea.Cmd {
	return func() tea.Msg {
		var result daemon.ListResult
		if err := m.client.Call(m.ctx, method, nil, &result); err != nil {
			return linkMsg(err.Error())
		}
		return listMsg(result)
	}
}

func (m model) loadAliases() tea.Cmd {
	return func() tea.Msg {
		var result daemon.AliasesResult
		if err := m.client.Call(m.ctx, ipc.MethodAliasesList, nil, &result); err != nil {
			return linkMsg(err.Error())
		}
		return aliasesMsg(result)
	}
}

// request sends params and reports done as the status line once it is accepted.
func (m model) request(method string, params any, done string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.Call(m.ctx, method, params, nil); err != nil {
			return failureMsg(err.Error())
		}
		return statusMsg(done)
	}
}
