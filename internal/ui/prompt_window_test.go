package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func promptWindowUpdate(t *testing.T, m promptModel, msg tea.Msg) (promptModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	got, ok := updated.(promptModel)
	if !ok {
		t.Fatalf("Update returned %T, want ui.promptModel", updated)
	}
	return got, cmd
}

// This is called only for transitions that return a direct tea.Quit command.
// A command returned by Enter could contact the daemon and is never executed.
func requireQuitCommand(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected the standalone prompt to close")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected a QuitMsg from the standalone prompt")
	}
}

func TestStandalonePromptShowsOnlyQuestion(t *testing.T) {
	m := promptModel{model: newModel(context.Background(), nil)}
	m.width, m.height = 80, 20
	connection := conn("c1", "Production", "prod-host", false)
	m, cmd := promptWindowUpdate(t, m, listMsg{
		Connections: []daemon.Connection{connection},
		Jobs:        []jobs.Job{waitingJob("job-1", "c1", "Continue? [y/N]")},
	})
	if !m.loaded || m.dialog == nil || cmd != nil {
		t.Fatalf("loaded question: loaded = %v, dialog = %+v, command present = %v", m.loaded, m.dialog, cmd != nil)
	}
	view := m.View()
	for _, wanted := range []string{"Confirmation required", "Production", "Continue?", "No", "Yes"} {
		if !strings.Contains(view, wanted) {
			t.Errorf("question view is missing %q", wanted)
		}
	}
	for _, managerUI := range []string{"SSH connections", "Ctrl+A add", "Ctrl+E edit", "Enter details"} {
		if strings.Contains(view, managerUI) {
			t.Errorf("standalone question included manager UI %q", managerUI)
		}
	}
}

func TestStandalonePromptMasksSecret(t *testing.T) {
	m := promptModel{model: newModel(context.Background(), nil)}
	m.width, m.height = 80, 20
	m, _ = promptWindowUpdate(t, m, listMsg{Jobs: []jobs.Job{waitingJob("job-1", "c1", "Password:")}})
	secret := "example-typed-secret"
	m, _ = promptWindowUpdate(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secret)})
	if m.dialog == nil {
		t.Fatal("password question was not open")
	}
	if m.dialog.input.Value() != secret {
		t.Errorf("password entry was not retained in the input")
	}
	if strings.Contains(m.View(), secret) {
		t.Error("password entry was exposed in the standalone prompt")
	}
}

func TestStandalonePromptClosesWhenLoadedWithoutQuestions(t *testing.T) {
	m := promptModel{model: newModel(context.Background(), nil)}
	m, cmd := promptWindowUpdate(t, m, listMsg{})
	if !m.loaded || m.dialog != nil {
		t.Fatalf("empty snapshot: loaded = %v, dialog = %+v", m.loaded, m.dialog)
	}
	requireQuitCommand(t, cmd)
}

func TestStandalonePromptEscapeClosesWithoutAnswering(t *testing.T) {
	job := waitingJob("job-1", "c1", "Enter code:")
	m := promptModel{model: newModel(context.Background(), nil)}
	m, _ = promptWindowUpdate(t, m, listMsg{Jobs: []jobs.Job{job}})
	m, cmd := promptWindowUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil {
		t.Errorf("Esc left a dialog open: %+v", m.dialog)
	}
	requireQuitCommand(t, cmd)
}

func TestStandalonePromptClosesAfterSuccessfulReplyAndKeepsSendFailure(t *testing.T) {
	job := waitingJob("job-1", "c1", "Enter code:")
	newWindow := func(t *testing.T) promptModel {
		t.Helper()
		m := promptModel{model: newModel(context.Background(), nil)}
		m, _ = promptWindowUpdate(t, m, listMsg{Jobs: []jobs.Job{job}})
		return m
	}

	t.Run("success", func(t *testing.T) {
		m := newWindow(t)
		m, send := promptWindowUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if send == nil || m.dialog == nil || !m.dialog.sending {
			t.Fatalf("Enter did not start a reply")
		}
		// Do not run send: it would call the daemon. Deliver its success event.
		m, cmd := promptWindowUpdate(t, m, promptReplyMsg{jobID: job.ID, prompt: job.Prompt})
		if m.dialog != nil {
			t.Errorf("successful reply left a dialog open: %+v", m.dialog)
		}
		requireQuitCommand(t, cmd)
	})

	t.Run("failure", func(t *testing.T) {
		m := newWindow(t)
		m, send := promptWindowUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if send == nil {
			t.Fatal("Enter did not return a send command")
		}
		m, cmd := promptWindowUpdate(t, m, promptReplyMsg{jobID: job.ID, prompt: job.Prompt, err: errors.New("connection dropped")})
		if cmd != nil || m.dialog == nil || m.dialog.sending || !strings.Contains(m.View(), "connection dropped") {
			t.Errorf("failed reply did not keep the question and error visible")
		}
	})
}

func TestStandalonePromptHandlesAnotherQuestionBeforeClosing(t *testing.T) {
	first := waitingJob("job-1", "c1", "First code:")
	second := waitingJob("job-2", "c2", "Second code:")
	m := promptModel{model: newModel(context.Background(), nil)}
	m, _ = promptWindowUpdate(t, m, listMsg{Jobs: []jobs.Job{first, second}})
	if m.dialog == nil || m.dialog.jobID != first.ID {
		t.Fatalf("first question = %+v", m.dialog)
	}
	m, send := promptWindowUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if send == nil {
		t.Fatal("first answer did not return a send command")
	}
	m, cmd := promptWindowUpdate(t, m, promptReplyMsg{jobID: first.ID, prompt: first.Prompt})
	if m.dialog == nil || m.dialog.jobID != second.ID || cmd != nil {
		t.Fatalf("after first answer: dialog = %+v, command present = %v; want second question without closing", m.dialog, cmd != nil)
	}
	m, cmd = promptWindowUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil {
		t.Errorf("last dismissal left a dialog open")
	}
	requireQuitCommand(t, cmd)
}
