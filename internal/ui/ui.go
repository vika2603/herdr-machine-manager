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
	return run(ctx, env, false)
}

// RunPrompt displays only pending questions, then closes. It connects to the
// daemon that owns the waiting PTY; Ensure preserves compatible busy daemons.
func RunPrompt(ctx context.Context, env *plugin.Env) error {
	return run(ctx, env, true)
}

func run(ctx context.Context, env *plugin.Env, promptOnly bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := daemon.Ensure(ctx, env); err != nil {
		return err
	}
	client := daemon.Connect(env)

	cfg, cfgErr := config.Load(env.ConfigDir)
	model := newModel(ctx, client)
	model.installDefault = cfg.InstallRemote
	if cfgErr != nil {
		model.config = cfgErr.Error()
	}

	// No alternate screen: the popup is a pane herdr destroys when it closes,
	// so there is no scrollback to protect, and staying on the main screen
	// keeps the view readable to pane.read.
	var initial tea.Model = model
	if promptOnly {
		initial = promptModel{model: model}
	}
	program := tea.NewProgram(initial, tea.WithContext(ctx))
	go stream(ctx, client, program.Send)
	_, err := program.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// stream keeps a subscription open and forwards what the daemon pushes into
// the program through send. A dropped stream is reopened: the daemon can be replaced by a
// newer build while the popup is open.
func stream(ctx context.Context, client *herdr.Client, send func(tea.Msg)) {
	for ctx.Err() == nil {
		if s, err := client.OpenStream(ctx, ipc.MethodSubscribe, nil); err == nil {
			// The subscription does not replay what it missed, so the list
			// is read again once it is open.
			send(reconnectMsg{})
			for event, err := s.Next(ctx); err == nil; event, err = s.Next(ctx) {
				if msg := decodeEvent(event); msg != nil {
					send(msg)
				}
			}
			_ = s.Close()
			if ctx.Err() == nil {
				send(linkMsg("lost the daemon, reconnecting…"))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func decodeEvent(event *herdr.RawEvent) tea.Msg {
	var list daemon.ListResult
	var job jobs.Job
	var out struct {
		JobID string   `json:"job_id"`
		Lines []string `json:"lines"`
	}
	switch {
	case event.Event == ipc.EventConnectionsChanged && json.Unmarshal(event.Data, &list) == nil:
		return listMsg(list)
	case event.Event == ipc.EventJobUpdated && json.Unmarshal(event.Data, &job) == nil:
		return jobMsg(job)
	case event.Event == ipc.EventJobOutput && json.Unmarshal(event.Data, &out) == nil:
		return outputMsg{jobID: out.JobID, lines: out.Lines}
	}
	return nil
}
