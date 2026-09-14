package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func TestAddAnswersLeavesInstallForUserWhenAllowed(t *testing.T) {
	for _, prompt := range []string{
		"install the remote herdr binary?",
		`Install the 0.9.0 stable asset for linux-x86_64 to "$HOME/.local/bin/herdr"? [Y/n]`,
	} {
		for _, answer := range addAnswers(true) {
			if answer.Match.MatchString(prompt) {
				t.Fatalf("install prompt %q was answered automatically: %+v", prompt, answer)
			}
		}
	}
}

func TestAddAnswersDeclinesInstallWhenDisallowed(t *testing.T) {
	for _, prompt := range []string{
		"install the remote herdr binary?",
		`Install the 0.9.0 stable asset for linux-x86_64 to "$HOME/.local/bin/herdr"? [Y/n]`,
	} {
		found := false
		for _, answer := range addAnswers(false) {
			if answer.Match.MatchString(prompt) {
				found = true
				if answer.Reply != "n\n" {
					t.Errorf("install prompt %q reply = %q, want no", prompt, answer.Reply)
				}
			}
		}
		if !found {
			t.Errorf("no policy for disallowed install prompt %q", prompt)
		}
	}
}

func TestAddAnswersDoesNotDeclineOtherAsset(t *testing.T) {
	prompt := `Install the 0.9.0 stable asset for linux-x86_64 to "$HOME/.local/bin/other"? [Y/n]`
	for _, answer := range addAnswers(false) {
		if answer.Match.MatchString(prompt) {
			t.Fatalf("unrelated asset prompt was answered automatically: %+v", answer)
		}
	}
}

func TestAddAnswersLeavesIncompatibleServerForUser(t *testing.T) {
	for _, install := range []bool{false, true} {
		for _, answer := range addAnswers(install) {
			if answer.Match.MatchString("stop incompatible remote server?") {
				t.Errorf("install %v: server prompt was answered automatically: %+v", install, answer)
			}
		}
	}
}

func TestInstallQuestionWaitsForUserWhenAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ask.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'install the remote herdr binary? [y/N] '\nread answer\necho answered:$answer\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q := jobs.NewQueue(ctx, jobs.Hooks{})
	job, err := q.Submit(jobs.Spec{Kind: jobs.KindConnect, Title: "connect", Args: []string{path}, Answers: addAnswers(true)})
	if err != nil {
		t.Fatal(err)
	}
	waitForJobState(t, q, job.ID, jobs.StateAwaitingInput)
	if prompt := q.List()[0].Prompt; !strings.Contains(prompt, "install the remote herdr binary?") {
		t.Fatalf("prompt = %q", prompt)
	}
	if err := q.Input(job.ID, "n\n"); err != nil {
		t.Fatal(err)
	}
	waitForJobState(t, q, job.ID, jobs.StateSucceeded)
}

func TestInstallQuestionIsDeclinedWhenDisallowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ask.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'install the remote herdr binary? [y/N] '\nread answer\necho answered:$answer\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	q := jobs.NewQueue(ctx, jobs.Hooks{})
	job, err := q.Submit(jobs.Spec{Kind: jobs.KindConnect, Title: "connect", Args: []string{path}, Answers: addAnswers(false)})
	if err != nil {
		t.Fatal(err)
	}
	waitForJobState(t, q, job.ID, jobs.StateSucceeded)
	got := q.List()[0]
	if got.Prompt != "" || !strings.Contains(strings.Join(got.Tail, "\n"), "answered:n") {
		t.Errorf("job = %+v, want an automatic no without a pending prompt", got)
	}
}

func waitForJobState(t *testing.T, q *jobs.Queue, id string, state jobs.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range q.List() {
			if job.ID == id && job.State == state {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %q did not reach %s: %+v", id, state, q.List())
}
