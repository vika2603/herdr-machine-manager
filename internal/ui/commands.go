// The commands the TUI issues. Each returns a tea.Cmd that calls the daemon on
// its own goroutine, so no keystroke waits on a request.

package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
)

func (m model) loadList() tea.Cmd {
	return func() tea.Msg {
		var result daemon.ListResult
		if err := m.client.Call(m.ctx, ipc.MethodList, nil, &result); err != nil {
			return failureMsg(err.Error())
		}
		return listMsg(result)
	}
}

func (m model) refresh() tea.Cmd {
	return func() tea.Msg {
		var result daemon.ListResult
		if err := m.client.Call(m.ctx, ipc.MethodRefresh, nil, &result); err != nil {
			return failureMsg(err.Error())
		}
		return listMsg(result)
	}
}

func (m model) loadAliases() tea.Cmd {
	return func() tea.Msg {
		var result daemon.AliasesResult
		if err := m.client.Call(m.ctx, ipc.MethodAliasesList, nil, &result); err != nil {
			return failureMsg(err.Error())
		}
		return aliasesMsg(result)
	}
}

func (m model) save(params ipc.SaveParams) tea.Cmd {
	return func() tea.Msg {
		var result ipc.SaveResult
		if err := m.client.Call(m.ctx, ipc.MethodSave, params, &result); err != nil {
			return failureMsg(err.Error())
		}
		if len(result.Jobs) == 0 {
			return statusMsg(fmt.Sprintf("saved %s", params.Label))
		}
		return statusMsg(fmt.Sprintf("saved %s · %d job(s) queued", params.Label, len(result.Jobs)))
	}
}

func (m model) act(method, id string, install bool) tea.Cmd {
	return func() tea.Msg {
		var result ipc.SaveResult
		params := ipc.ConnectionTarget{ID: id, Install: install}
		if err := m.client.Call(m.ctx, method, params, &result); err != nil {
			return failureMsg(err.Error())
		}
		return statusMsg(strings.TrimPrefix(method, "connection.") + " queued")
	}
}

func (m model) cancel(jobID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.Call(m.ctx, ipc.MethodJobCancel, ipc.JobTarget{JobID: jobID}, nil); err != nil {
			return failureMsg(err.Error())
		}
		return statusMsg("cancelling " + jobID)
	}
}
