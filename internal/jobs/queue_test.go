package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

func script(t *testing.T, body string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{path}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for job state")
}

func testQueue(t *testing.T) *Queue {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewQueue(ctx, Hooks{})
}

func jobByID(q *Queue, id string) Job {
	for _, job := range q.List() {
		if job.ID == id {
			return job
		}
	}
	return Job{}
}

func awaitState(t *testing.T, q *Queue, id string, state State) Job {
	t.Helper()
	waitFor(t, func() bool { return jobByID(q, id).State == state })
	return jobByID(q, id)
}

func TestCompletionAndRetention(t *testing.T) {
	q := testQueue(t)
	if err := q.Input("missing", "answer"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown input = %v", err)
	}
	success := func(context.Context, Sink) error { return nil }
	first := q.Submit("one", Spec{Run: success})
	awaitState(t, q, first.ID, StateSucceeded)
	second := q.Submit("one", Spec{Run: func(context.Context, Sink) error { return errors.New("herdr said no") }})
	got := awaitState(t, q, second.ID, StateFailed)
	if got.Err != "herdr said no" || len(q.List()) != 1 || q.List()[0].ID != second.ID {
		t.Errorf("retained jobs = %+v", q.List())
	}
	third := q.Submit("two", Spec{Run: success})
	awaitState(t, q, third.ID, StateSucceeded)
	if len(q.List()) != 2 {
		t.Errorf("retained %d jobs, want one per connection", len(q.List()))
	}
	if err := q.Input(third.ID, "answer"); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("finished input = %v", err)
	}
}

func TestSubmissionOrderAndParallelConnections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := testQueue(t)
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		started := make(chan string, 4)
		run := func(name string, block bool) func(context.Context, Sink) error {
			return func(context.Context, Sink) error {
				started <- name
				if block {
					<-release
				}
				return nil
			}
		}
		first := q.Submit("one", Spec{Run: run("first", true)})
		synctest.Wait()
		disconnect := q.Submit("one", Spec{Run: run("disconnect", false)})
		connect := q.Submit("one", Spec{Run: run("connect", false)})
		other := q.Submit("two", Spec{Run: run("other", false)})
		synctest.Wait()
		if jobByID(q, first.ID).State != StateRunning || jobByID(q, disconnect.ID).State != StateQueued || jobByID(q, connect.ID).State != StateQueued || jobByID(q, other.ID).State != StateSucceeded {
			t.Errorf("connection scheduling = %+v", q.List())
		}
		unblock()
		synctest.Wait()
		close(started)
		var order []string
		for name := range started {
			order = append(order, name)
		}
		if !slices.Equal(order, []string{"first", "other", "disconnect", "connect"}) {
			t.Errorf("submission order = %v", order)
		}
	})
}

func TestConcurrentSubmissionsSettlePerConnection(t *testing.T) {
	q := testQueue(t)
	var wg sync.WaitGroup
	for conn := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				q.Submit(string(rune('a'+conn)), Spec{Run: func(context.Context, Sink) error { return nil }})
			}
		}()
	}
	wg.Wait()
	waitFor(t, func() bool {
		jobs := q.List()
		if len(jobs) != 4 {
			return false
		}
		for _, job := range jobs {
			if !job.State.Terminal() {
				return false
			}
		}
		return true
	})
}

func TestQueuedCancellationSkipsRunner(t *testing.T) {
	q := testQueue(t)
	release := make(chan struct{})
	first := q.Submit("one", Spec{Run: func(context.Context, Sink) error { <-release; return nil }})
	awaitState(t, q, first.ID, StateRunning)
	ran := false
	second := q.Submit("one", Spec{Run: func(context.Context, Sink) error { ran = true; return nil }})
	if err := q.Cancel(second.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitState(t, q, second.ID, StateCancelled)
	if ran {
		t.Fatal("cancelled runner executed")
	}
}

func TestPTYExitAndOutput(t *testing.T) {
	q := testQueue(t)
	var mu sync.Mutex
	var output []string
	q.hooks.OnOutput = func(_ string, lines []string) {
		mu.Lock()
		output = append(output, lines...)
		mu.Unlock()
	}
	args := script(t, "echo first\necho second\nexit 3\n")
	job := q.Submit("one", Spec{Run: func(ctx context.Context, sink Sink) error { return Exec(ctx, args, nil, sink) }})
	got := awaitState(t, q, job.ID, StateFailed)
	if !strings.Contains(got.Err, "exit status 3") || !slices.Equal(got.Tail, []string{"first", "second"}) {
		t.Errorf("failed PTY job = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(output, got.Tail) {
		t.Errorf("streamed output = %q, tail = %q", output, got.Tail)
	}
}

func TestPTYPrompts(t *testing.T) {
	for _, tc := range []struct {
		name, body, input, want string
		answers                 []Answer
	}{
		{"automatic", "printf 'install the remote herdr binary? [y/N] '; read a; echo answered:$a", "", "answered:y",
			[]Answer{{Match: regexp.MustCompile(`install the remote herdr binary\?`), Reply: "y\n"}}},
		{"manual", "printf 'passphrase: '; read a; echo answered:$a", "secret\n", "answered:secret", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := testQueue(t)
			args := script(t, tc.body)
			job := q.Submit("one", Spec{Run: func(ctx context.Context, sink Sink) error { return Exec(ctx, args, tc.answers, sink) }})
			if tc.input != "" {
				got := awaitState(t, q, job.ID, StateAwaitingInput)
				if !strings.Contains(got.Prompt, "passphrase") {
					t.Errorf("prompt = %q", got.Prompt)
				}
				if err := q.Input(job.ID, tc.input); err != nil {
					t.Fatal(err)
				}
			}
			got := awaitState(t, q, job.ID, StateSucceeded)
			if !strings.Contains(strings.Join(got.Tail, "\n"), tc.want) {
				t.Errorf("output = %q", got.Tail)
			}
		})
	}
}

func TestPendingOutputIsBoundedOnRuneBoundary(t *testing.T) {
	q := testQueue(t)
	args := script(t, "i=0; while [ $i -lt 2000 ]; do printf '界'; i=$((i+1)); done; printf 'passphrase: '; read a; echo done")
	job := q.Submit("one", Spec{Run: func(ctx context.Context, sink Sink) error { return Exec(ctx, args, nil, sink) }})
	got := awaitState(t, q, job.ID, StateAwaitingInput)
	if len(got.Prompt) > maxPendingBytes || !utf8.ValidString(got.Prompt) || !strings.Contains(got.Prompt, "passphrase:") {
		t.Errorf("prompt has invalid bounded tail: length %d, last bytes %q", len(got.Prompt), got.Prompt[max(0, len(got.Prompt)-30):])
	}
	if !strings.Contains(strings.Join(got.Tail, "\n"), "[long output line truncated]") {
		t.Errorf("missing truncation notice: %q", got.Tail)
	}
	if err := q.Input(job.ID, "secret\n"); err != nil {
		t.Fatal(err)
	}
	awaitState(t, q, job.ID, StateSucceeded)
}

func TestTailIsBounded(t *testing.T) {
	q := testQueue(t)
	args := script(t, "i=0; while [ $i -lt 400 ]; do echo line$i; i=$((i+1)); done")
	job := q.Submit("one", Spec{Run: func(ctx context.Context, sink Sink) error { return Exec(ctx, args, nil, sink) }})
	got := awaitState(t, q, job.ID, StateSucceeded)
	if len(got.Tail) != tailLines || got.Tail[0] != "line200" || got.Tail[tailLines-1] != "line399" {
		t.Errorf("bounded tail = %q", got.Tail)
	}
}

func TestProgressDoesNotDismissPrompt(t *testing.T) {
	q := testQueue(t)
	marker := filepath.Join(t.TempDir(), "start-progress")
	args := script(t, fmt.Sprintf("printf 'passphrase: '; (while [ ! -e %q ]; do sleep 0.01; done; i=0; while [ $i -lt 20 ]; do printf '.'; sleep 0.05; i=$((i+1)); done) & read a; echo answered:$a", marker))
	job := q.Submit("one", Spec{Run: func(ctx context.Context, sink Sink) error { return Exec(ctx, args, nil, sink) }})
	awaitState(t, q, job.ID, StateAwaitingInput)
	// Start progress only after the question is visible, and inspect it before
	// the idle detector could turn an incorrectly cleared prompt back on.
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		time.Sleep(20 * time.Millisecond)
		if got := jobByID(q, job.ID).State; got != StateAwaitingInput {
			t.Fatalf("state during progress = %s", got)
		}
	}
	if err := q.Input(job.ID, "secret\n"); err != nil {
		t.Fatal(err)
	}
	awaitState(t, q, job.ID, StateSucceeded)
}

func TestPTYCancellationEscalates(t *testing.T) {
	q := testQueue(t)
	args := script(t, "trap '' TERM; echo ready; while :; do sleep 1; done")
	job := q.Submit("one", Spec{Run: func(ctx context.Context, sink Sink) error { return Exec(ctx, args, nil, sink) }})
	waitFor(t, func() bool { return strings.Contains(strings.Join(jobByID(q, job.ID).Tail, "\n"), "ready") })
	if err := q.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	awaitState(t, q, job.ID, StateCancelled)
}
