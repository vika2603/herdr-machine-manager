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

func TestQuestionOpensInPlaceAndKeepsTheUnderlyingWork(t *testing.T) {
	job := waitingJob("job-1", "c1", "continue? [y/N]")
	m := newModel(context.Background(), nil)
	m.conns = []daemon.Connection{conn("c1", "Production", "prod-host", false), conn("c2", "Other", "other", false)}
	m, _ = promptUpdate(t, m, listMsg{Connections: m.conns, Jobs: []jobs.Job{job}})
	if m.dialog == nil || m.dialog.jobID != job.ID || !strings.Contains(m.View(), "Production") {
		t.Fatalf("snapshot did not open the question naming its connection: %+v", m.dialog)
	}

	for _, setup := range [][]string{{"down"}, {"a", "x"}, {"e", "tab", "tab"}, {"down", "d"}} {
		m := sized(newModel(context.Background(), nil), 100, 26)
		m, _ = promptUpdate(t, m, listMsg{Connections: []daemon.Connection{conn("c1", "One", "one", false), conn("c2", "Two", "two", false)}})
		m, _ = press(t, m, setup...)
		before := m
		m, _ = promptUpdate(t, m, jobMsg(waitingJob("job-1", "c2", "Enter code:")))
		if m.typing() {
			m, _ = press(t, m, "ctrl+o")
		}
		if m.dialog == nil {
			t.Fatalf("after %v: live question did not open", setup)
		}
		m, _ = press(t, m, "esc")
		if m.mode != before.mode || m.cursor != before.cursor || m.field != before.field || m.filter.Value() != before.filter.Value() || m.forget != before.forget {
			t.Errorf("after %v: dismissing the question changed the work underneath: mode %v→%v cursor %d→%d", setup, before.mode, m.mode, before.cursor, m.cursor)
		}
	}
}

func TestQuestionWaitsWhileTheUserTypes(t *testing.T) {
	job := waitingJob("job-1", "c2", "Enter code:")
	for _, c := range []struct {
		setup, leave []string
	}{
		{[]string{"a", "n", "e"}, []string{"esc"}},
		{[]string{"a", "n", "e"}, []string{"enter", "esc"}},
		{[]string{"e", "x"}, []string{"esc"}},
		{[]string{"e", "x"}, []string{"enter"}},
	} {
		m := sized(newModel(context.Background(), nil), 100, 26)
		m, _ = promptUpdate(t, m, listMsg{Connections: []daemon.Connection{conn("c1", "One", "one", false), conn("c2", "Two", "two", false)}})
		m, _ = press(t, m, c.setup...)
		m, _ = promptUpdate(t, m, jobMsg(job))
		if m.dialog != nil {
			t.Fatalf("after %v: the question took focus from the input", c.setup)
		}
		if view := m.View(); !strings.Contains(view, "1 needs answer") || !strings.Contains(view, "ctrl+o answer") {
			t.Errorf("after %v: the waiting question is not indicated:\n%s", c.setup, view)
		}
		typed := m.filter.Value() + m.fields[0].Value()
		m, _ = press(t, m, "z")
		if got := m.filter.Value() + m.fields[0].Value(); got != typed+"z" {
			t.Errorf("after %v: typing gave %q, want %q", c.setup, got, typed+"z")
		}
		m, _ = press(t, m, c.leave...)
		if m.typing() || m.dialog == nil || m.dialog.jobID != job.ID {
			t.Errorf("after %v then %v: mode %v; want the question open once the input is left", c.setup, c.leave, m.mode)
		}
	}
}

func TestConfirmationDefaultsToNo(t *testing.T) {
	m := sized(newModel(context.Background(), nil), 100, 26)
	m, _ = promptUpdate(t, m, jobMsg(waitingJob("job-1", "c1", "Continue? [Y/n]")))
	if m.dialog == nil || m.dialog.info.Kind != jobs.PromptConfirm || m.dialog.yes {
		t.Fatalf("confirmation: %+v; want No selected even when the prompt defaults to yes", m.dialog)
	}
	m, _ = press(t, m, "right")
	if !m.dialog.yes {
		t.Error("right did not choose Yes")
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.dialog.yes {
		t.Error("left did not choose No")
	}
	m, _ = press(t, m, "y")
	if m.dialog.yes {
		t.Error("a letter changed the choice; only the arrows choose")
	}
	m, cmd := press(t, m, "enter")
	if cmd == nil || !m.dialog.sending {
		t.Error("enter did not send the choice")
	}
	if m, cmd = press(t, m, "esc"); m.dialog == nil || cmd != nil {
		t.Error("keys acted on the question while its answer was being sent")
	}
}

func TestSecretIsMaskedAndTheDraftSurvivesUpdates(t *testing.T) {
	job := waitingJob("job-1", "c1", "alice@host password:")
	m := sized(newModel(context.Background(), nil), 100, 26)
	m, _ = promptUpdate(t, m, jobMsg(job))
	secret := "sample-secret-123"
	m, _ = press(t, m, secret)
	if strings.Contains(m.View(), secret) {
		t.Error("the typed secret was rendered")
	}
	m, _ = promptUpdate(t, m, jobMsg(job))
	m, _ = promptUpdate(t, m, listMsg{Jobs: []jobs.Job{job}})
	m, _ = promptUpdate(t, m, outputMsg{jobID: job.ID, lines: []string{"progress"}})
	if m.dialog == nil || m.dialog.input.Value() != secret {
		t.Errorf("background updates replaced the question or erased the draft")
	}
}

func TestDismissedQuestionStaysDismissedUntilOpenedOrReplaced(t *testing.T) {
	job := waitingJob("job-1", "c1", "Enter code:")
	m := sized(newModel(context.Background(), nil), 100, 26)
	m, _ = promptUpdate(t, m, listMsg{Connections: []daemon.Connection{conn("c1", "One", "host", false)}, Jobs: []jobs.Job{job}})
	m, _ = press(t, m, "esc")
	m, _ = promptUpdate(t, m, jobMsg(job))
	m, _ = promptUpdate(t, m, listMsg{Connections: m.conns, Jobs: []jobs.Job{job}})
	if m.dialog != nil {
		t.Fatal("the dismissed question reopened by itself")
	}
	m, cmd := press(t, m, "enter")
	if m.dialog == nil || cmd != nil {
		t.Fatalf("enter on the waiting connection did not reopen its question")
	}
	m, _ = press(t, m, "esc")
	job.Prompt = "Enter a second code:"
	m, _ = promptUpdate(t, m, jobMsg(job))
	if m.dialog == nil || m.dialog.prompt != job.Prompt {
		t.Error("a new question on the same job did not open")
	}
}

func TestQuestionsAreHandledOneAtATime(t *testing.T) {
	first, second := waitingJob("job-1", "c1", "First code:"), waitingJob("job-2", "c2", "Second code:")
	m := newModel(context.Background(), nil)
	m, _ = promptUpdate(t, m, listMsg{Jobs: []jobs.Job{first, second}})
	if m.dialog == nil || m.dialog.jobID != first.ID {
		t.Fatalf("first question = %+v", m.dialog)
	}
	m, _ = promptUpdate(t, m, promptReplyMsg{jobID: first.ID, prompt: first.Prompt})
	if m.dialog == nil || m.dialog.jobID != second.ID {
		t.Fatalf("after answering the first: %+v, want the second", m.dialog)
	}
	second.State = jobs.StateSucceeded
	m, _ = promptUpdate(t, m, jobMsg(second))
	if m.dialog != nil {
		t.Errorf("a question whose job finished stayed open: %+v", m.dialog)
	}
}

func TestFailedAnswerKeepsTheQuestionWithItsError(t *testing.T) {
	job := waitingJob("job-1", "c1", "Continue? [y/N]")
	m := sized(newModel(context.Background(), nil), 100, 26)
	m, _ = promptUpdate(t, m, jobMsg(job))
	m, _ = press(t, m, "enter")
	m, _ = promptUpdate(t, m, promptReplyMsg{jobID: job.ID, prompt: job.Prompt, err: errors.New("connection dropped")})
	if m.dialog == nil || m.dialog.sending || !strings.Contains(m.View(), "connection dropped") {
		t.Error("a failed answer did not leave the question open with the error")
	}
}

func standalone(t *testing.T, list listMsg) promptModel {
	t.Helper()
	p := promptModel{model: sized(newModel(context.Background(), nil), 64, 13)}
	next, cmd := p.Update(list)
	p = next.(promptModel)
	if p.dialog != nil && cmd != nil {
		t.Fatal("the prompt pane closed with a question open")
	}
	return p
}

func paneUpdate(t *testing.T, p promptModel, msgs ...tea.Msg) (promptModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = p.Update(msg)
		p = next.(promptModel)
	}
	return p, cmd
}

func TestPromptPaneShowsOnlyTheQuestion(t *testing.T) {
	p := standalone(t, listMsg{
		Connections: []daemon.Connection{conn("c1", "Production", "prod-host", false)},
		Jobs:        []jobs.Job{waitingJob("job-1", "c1", "Continue? [y/N]")},
	})
	view := p.View()
	for _, want := range []string{"Production", "Continue?", "No", "Yes", "esc later"} {
		if !strings.Contains(view, want) {
			t.Errorf("prompt pane is missing %q", want)
		}
	}
	for _, manager := range []string{"Machines", "a add", "e edit", "connected"} {
		if strings.Contains(view, manager) {
			t.Errorf("prompt pane shows manager UI %q", manager)
		}
	}
}

func TestPromptPaneClosesAfterTheLastQuestion(t *testing.T) {
	first, second := waitingJob("job-1", "c1", "First code:"), waitingJob("job-2", "c2", "Password:")

	if _, cmd := paneUpdate(t, promptModel{model: newModel(context.Background(), nil)}, listMsg{}); !isQuit(cmd) {
		t.Error("the pane stayed open with nothing to ask")
	}

	p := standalone(t, listMsg{Jobs: []jobs.Job{first, second}})
	p, cmd := paneUpdate(t, p, promptReplyMsg{jobID: first.ID, prompt: first.Prompt})
	if p.dialog == nil || p.dialog.jobID != second.ID || cmd != nil {
		t.Fatalf("after the first answer: %+v; want the second question, pane open", p.dialog)
	}
	p, _ = paneUpdate(t, p, keys("example-typed-secret")...)
	if strings.Contains(p.View(), "example-typed-secret") {
		t.Error("the pane rendered a typed password")
	}
	p, _ = paneUpdate(t, p, keys("enter")...)
	if p, cmd = paneUpdate(t, p, promptReplyMsg{jobID: second.ID, prompt: second.Prompt, err: errors.New("dropped")}); cmd != nil || p.dialog == nil {
		t.Fatal("a failed answer closed the pane")
	}
	if _, cmd = paneUpdate(t, p, keys("esc")...); !isQuit(cmd) {
		t.Error("dismissing the last question did not close the pane")
	}

	p = standalone(t, listMsg{Jobs: []jobs.Job{first}})
	if _, cmd = paneUpdate(t, p, promptReplyMsg{jobID: first.ID, prompt: first.Prompt}); !isQuit(cmd) {
		t.Error("answering the last question did not close the pane")
	}
}
