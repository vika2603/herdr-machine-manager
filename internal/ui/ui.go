// Package ui is the popup TUI. It is a client of the daemon and runs no herdr
// command itself, so no keystroke waits on a process.
package ui

import (
	"context"
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-machine-manager/internal/config"
	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

// reconnectDelay is how long the event loop waits before dialling the daemon
// again after the stream dropped.
const reconnectDelay = time.Second

// Run starts the daemon if it is not up, then runs the TUI until the popup
// closes.
func Run(ctx context.Context, env *plugin.Env) error {
	if err := daemon.Ensure(ctx, env); err != nil {
		return err
	}
	client := daemon.Connect(env)

	cfg, cfgErr := config.Load(env.ConfigDir)
	model := newModel(ctx, client)
	model.install = cfg.InstallRemote
	model.installDefault = cfg.InstallRemote
	if cfgErr != nil {
		model.failure = cfgErr.Error()
	}

	// No alternate screen: the popup is a pane herdr destroys when it closes,
	// so there is no scrollback to protect, and staying on the main screen
	// keeps the view readable to pane.read.
	program := tea.NewProgram(model, tea.WithContext(ctx))
	go stream(ctx, client, program)
	_, err := program.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// stream keeps a subscription open and forwards what the daemon pushes into
// the program. A dropped stream is reopened: the daemon can be replaced by a
// newer build while the popup is open.
func stream(ctx context.Context, client *herdr.Client, program *tea.Program) {
	for ctx.Err() == nil {
		s, err := client.OpenStream(ctx, ipc.MethodSubscribe, nil)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectDelay):
			}
			continue
		}
		for {
			event, err := s.Next(ctx)
			if err != nil {
				break
			}
			if msg := decodeEvent(event); msg != nil {
				program.Send(msg)
			}
		}
		_ = s.Close()
		if ctx.Err() != nil {
			return
		}
		program.Send(reconnectMsg{})
		time.Sleep(reconnectDelay)
	}
}

func decodeEvent(event *herdr.RawEvent) tea.Msg {
	switch event.Event {
	case ipc.EventConnectionsChanged:
		var result daemon.ListResult
		if err := json.Unmarshal(event.Data, &result); err != nil {
			return nil
		}
		return listMsg(result)

	case ipc.EventJobUpdated:
		var job jobs.Job
		if err := json.Unmarshal(event.Data, &job); err != nil {
			return nil
		}
		return jobMsg(job)

	case ipc.EventJobOutput:
		var out struct {
			JobID string   `json:"job_id"`
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(event.Data, &out); err != nil {
			return nil
		}
		return outputMsg{jobID: out.JobID, lines: out.Lines}
	}
	return nil
}
