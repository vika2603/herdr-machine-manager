package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func promptUpdate(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	got, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned %T, want ui.model", updated)
	}
	return got, cmd
}

func waitingJob(id, connID, question string) jobs.Job {
	return jobs.Job{ID: id, ConnID: connID, Title: "Connect " + connID, State: jobs.StateAwaitingInput, Prompt: question}
}

func TestPromptAppearsFromSnapshotAndLiveEventsWithoutChangingScreen(t *testing.T) {
	job := waitingJob("job-1", "c1", "continue? [y/N]")
	m := newModel(context.Background(), nil)
	m.conns = []daemon.Connection{conn("c1", "Production", "prod-host", false)}
	m.cursor = 0
	m, _ = promptUpdate(t, m, listMsg{Connections: m.conns, Jobs: []jobs.Job{job}})
	if m.dialog == nil || m.dialog.jobID != job.ID || m.screen != screenList || m.cursor != 0 {
		t.Fatalf("snapshot: dialog = %+v, screen = %v, cursor = %d", m.dialog, m.screen, m.cursor)
	}
	if !strings.Contains(m.View(), "Production") {
		t.Error("prompt did not identify the affected connection")
	}

	for _, screen := range []screen{screenList, screenAliases, screenForm, screenConfirm, screenOutput} {
		t.Run(fmt.Sprintf("screen-%d", screen), func(t *testing.T) {
			m := newModel(context.Background(), nil)
			m.screen, m.field, m.cursor = screen, 2, 3
			m.session.SetValue("keep this value")
			m, _ = promptUpdate(t, m, jobMsg(job))
			if m.dialog == nil || m.dialog.jobID != job.ID || m.screen != screen || m.field != 2 || m.cursor != 3 || m.session.Value() != "keep this value" {
				t.Errorf("live event on screen %v: dialog = %+v, screen = %v, field = %d, cursor = %d, session = %q", screen, m.dialog, m.screen, m.field, m.cursor, m.session.Value())
			}
		})
	}
}

func TestPromptConfirmationDefaultsToNoAndCanSelectYes(t *testing.T) {
	m := newModel(context.Background(), nil)
	m, _ = promptUpdate(t, m, jobMsg(waitingJob("job-1", "c1", "Continue? [y/N]")))
	if m.dialog == nil || m.dialog.info.Kind != jobs.PromptConfirm || m.dialog.yes {
		t.Fatalf("confirmation: dialog = %+v; want No selected", m.dialog)
	}
	if view := m.View(); !strings.Contains(view, "Confirmation required") || !strings.Contains(view, "No") || !strings.Contains(view, "Yes") {
		t.Errorf("confirmation choices missing from dialog")
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.dialog.yes {
		t.Error("Tab did not select Yes")
	}
	m, cmd := promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.dialog == nil || !m.dialog.sending {
		t.Errorf("Enter: command present = %v, dialog = %+v; want pending submission", cmd != nil, m.dialog)
	}
	// The command is deliberately not called: it would send input to the daemon.
}

func TestSecretPromptMasksInputAndKeepsDraftAcrossRepeatedUpdates(t *testing.T) {
	job := waitingJob("job-1", "c1", "alice@host password:")
	m := newModel(context.Background(), nil)
	m.width, m.height = 80, 24
	m, _ = promptUpdate(t, m, jobMsg(job))
	secret := "sample-secret-123"
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(secret)})
	if m.dialog == nil {
		t.Fatal("secret prompt was not open")
	}
	if m.dialog.input.Value() != secret {
		t.Fatalf("secret draft = %q, want typed input retained", m.dialog.input.Value())
	}
	if strings.Contains(m.View(), secret) {
		t.Error("the rendered prompt exposed the typed secret")
	}
	m, _ = promptUpdate(t, m, jobMsg(job))
	if m.dialog == nil || m.dialog.input.Value() != secret {
		t.Errorf("repeated update replaced the prompt or erased the draft")
	}
}

func TestDismissedPromptStaysDismissedUntilManualOpenOrAnewQuestion(t *testing.T) {
	job := waitingJob("job-1", "c1", "Enter code:")
	m := newModel(context.Background(), nil)
	m.conns = []daemon.Connection{conn("c1", "One", "host", false)}
	m, _ = promptUpdate(t, m, jobMsg(job))
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil || m.screen != screenList {
		t.Fatalf("Esc: dialog = %+v, screen = %v", m.dialog, m.screen)
	}
	m, _ = promptUpdate(t, m, jobMsg(job))
	if m.dialog != nil {
		t.Error("the same waiting question reopened after dismissal")
	}
	m, cmd := promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog == nil || m.dialog.jobID != job.ID || m.screen != screenOutput || cmd != nil {
		t.Fatalf("manual Enter from list: dialog = %+v, screen = %v, command present = %v; want selected job prompt only", m.dialog, m.screen, cmd != nil)
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil || m.screen != screenOutput {
		t.Errorf("Esc from manually opened prompt changed the underlying screen")
	}
	m, cmd = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog == nil || m.dialog.jobID != job.ID || m.screen != screenOutput || cmd != nil {
		t.Fatalf("manual Enter from details: dialog = %+v, screen = %v, command present = %v; must reopen without submitting input", m.dialog, m.screen, cmd != nil)
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	job.Prompt = "Enter a second code:"
	m, _ = promptUpdate(t, m, jobMsg(job))
	if m.dialog == nil || m.dialog.prompt != job.Prompt {
		t.Errorf("new question on the same job did not open")
	}
}

func TestPromptClearsAfterWaitingEndsAndQueuesOtherJobs(t *testing.T) {
	first := waitingJob("job-1", "c1", "First code:")
	second := waitingJob("job-2", "c2", "Second code:")
	m := newModel(context.Background(), nil)
	m, _ = promptUpdate(t, m, listMsg{Jobs: []jobs.Job{first, second}})
	if m.dialog == nil || m.dialog.jobID != first.ID {
		t.Fatalf("first queued prompt = %+v", m.dialog)
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog == nil || m.dialog.jobID != second.ID {
		t.Errorf("dismissing first did not show the next waiting job")
	}
	second.State = jobs.StateSucceeded
	m, _ = promptUpdate(t, m, jobMsg(second))
	if m.dialog != nil {
		t.Errorf("terminal event left a stale prompt open: %+v", m.dialog)
	}
	first.State = jobs.StateRunning
	m, _ = promptUpdate(t, m, jobMsg(first))
	first.State = jobs.StateAwaitingInput
	m, _ = promptUpdate(t, m, jobMsg(first))
	if m.dialog == nil || m.dialog.jobID != first.ID {
		t.Error("the question did not reopen after leaving and reentering the waiting state")
	}
}

func TestFailedPromptSubmissionShowsErrorWithoutLosingUnderlyingForm(t *testing.T) {
	job := waitingJob("job-1", "c1", "Continue? [y/N]")
	m := newModel(context.Background(), nil)
	m.screen, m.field = screenForm, 2
	m.session.SetValue("existing-session")
	m.focusField()
	m, _ = promptUpdate(t, m, jobMsg(job))
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.dialog == nil || !m.dialog.sending {
		t.Fatal("confirmation did not enter sending state")
	}
	m, _ = promptUpdate(t, m, promptReplyMsg{jobID: job.ID, prompt: job.Prompt, err: errors.New("connection dropped")})
	if m.dialog == nil || m.dialog.sending || !strings.Contains(m.View(), "connection dropped") {
		t.Errorf("failed submission did not keep an actionable dialog with the error")
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil || m.screen != screenForm || m.field != 2 || m.session.Value() != "existing-session" {
		t.Errorf("dismissing failed dialog changed the form: screen = %v, field = %d, session = %q", m.screen, m.field, m.session.Value())
	}
}
