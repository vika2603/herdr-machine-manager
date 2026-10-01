package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

func conn(id, label, target string, active bool) daemon.Connection {
	return daemon.Connection{
		Connection: store.Connection{ID: id, Label: label, Target: target},
		Active:     active,
	}
}

func waitingJob(id, connID, question string) jobs.Job {
	return jobs.Job{ID: id, ConnID: connID, Kind: jobs.KindConnect, Title: "connect " + connID, State: jobs.StateAwaitingInput, Prompt: question}
}

func promptUpdate(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	got, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned %T, want ui.model", updated)
	}
	return got, cmd
}
