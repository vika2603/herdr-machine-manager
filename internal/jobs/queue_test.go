package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// script writes an executable shell script and returns the argv that runs it.
// The PTY path is exercised against a real process rather than a stub runner:
// the prompt handling only means anything against a terminal.
func script(t *testing.T, body string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{path}
}

// recorder collects what the queue reports.
type recorder struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func newRecorder() *recorder { return &recorder{jobs: map[string]Job{}} }

func (r *recorder) hooks() Hooks {
	return Hooks{OnUpdate: func(j Job) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.jobs[j.ID] = j
	}}
}

func (r *recorder) state(id string) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id].State
}

func (r *recorder) job(id string) Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id]
}

// waitFor polls until cond holds, which is how a test observes a queue that
// runs its jobs on their own goroutines.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newTestQueue(t *testing.T) (*Queue, *recorder) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rec := newRecorder()
	return NewQueue(ctx, rec.hooks()), rec
}

func TestRunJobSucceeds(t *testing.T) {
	q, rec := newTestQueue(t)
	job, err := q.Submit(Spec{Kind: KindDisconnect, Title: "disconnect one", ConnID: "c1",
		Run: func(context.Context, Sink) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to succeed", func() bool { return rec.state(job.ID) == StateSucceeded })
}

func TestRunJobFailureKeepsTheError(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{Kind: KindRename, Title: "rename", ConnID: "c1",
		Run: func(context.Context, Sink) error { return errors.New("herdr said no") }})

	waitFor(t, "the job to fail", func() bool { return rec.state(job.ID) == StateFailed })
	if got := q.List()[0].Err; got != "herdr said no" {
		t.Errorf("error = %q, want the one the command reported", got)
	}
}

func TestNonZeroExitFails(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args: script(t, "echo nope\nexit 3\n")})

	waitFor(t, "the job to fail", func() bool { return rec.state(job.ID) == StateFailed })
	list := q.List()
	if !strings.Contains(list[0].Err, "exit status 3") {
		t.Errorf("error = %q, want it to name the exit status", list[0].Err)
	}
	if len(list[0].Tail) == 0 || list[0].Tail[0] != "nope" {
		t.Errorf("tail = %q, want the command output", list[0].Tail)
	}
}

func TestKnownPromptIsAnswered(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{
		Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args:    script(t, "printf 'install the remote herdr binary? [y/N] '\nread a\necho \"answered:$a\"\n"),
		Answers: []Answer{{Match: regexp.MustCompile(`install the remote herdr binary\?`), Reply: "y\n"}},
	})

	waitFor(t, "the job to succeed", func() bool { return rec.state(job.ID) == StateSucceeded })
	tail := strings.Join(q.List()[0].Tail, "\n")
	if !strings.Contains(tail, "answered:y") {
		t.Errorf("tail = %q, want the configured answer to have been sent", tail)
	}
	if rec.job(job.ID).Prompt != "" {
		t.Error("a job answered from its spec should never have reported a prompt")
	}
	if !strings.Contains(tail, "install the remote herdr binary?") {
		t.Errorf("tail = %q, want the question that was answered to be visible", tail)
	}
}

func TestUnknownPromptWaitsForInput(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{
		Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args: script(t, "printf 'passphrase: '\nread p\necho \"got:$p\"\n"),
	})

	waitFor(t, "the job to ask", func() bool { return rec.state(job.ID) == StateAwaitingInput })
	if prompt := q.List()[0].Prompt; !strings.Contains(prompt, "passphrase") {
		t.Errorf("prompt = %q, want the text the command stopped on", prompt)
	}
	if err := q.Input(job.ID, "hunter2\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to succeed", func() bool { return rec.state(job.ID) == StateSucceeded })
	if tail := strings.Join(q.List()[0].Tail, "\n"); !strings.Contains(tail, "got:hunter2") {
		t.Errorf("tail = %q, want the answer to have reached the command", tail)
	}
}

func TestInputForUnknownJob(t *testing.T) {
	q, _ := newTestQueue(t)
	if err := q.Input("job-404", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Input = %v, want ErrNotFound", err)
	}
}

func TestCancelStopsARunningJob(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args: script(t, "sleep 30\n")})

	waitFor(t, "the job to start", func() bool { return rec.state(job.ID) == StateRunning })
	if err := q.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to end as cancelled", func() bool { return rec.state(job.ID) == StateCancelled })
}

func TestJobsOfOneConnectionRunInOrder(t *testing.T) {
	q, rec := newTestQueue(t)
	var order []string
	var mu sync.Mutex
	record := func(name string) func(context.Context, Sink) error {
		return func(context.Context, Sink) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			return nil
		}
	}
	first, _ := q.Submit(Spec{Kind: KindDisconnect, Title: "disconnect", ConnID: "c1", Run: record("disconnect")})
	second, _ := q.Submit(Spec{Kind: KindConnect, Title: "connect", ConnID: "c1", Run: record("connect")})

	waitFor(t, "both jobs to finish", func() bool {
		return rec.state(first.ID).Terminal() && rec.state(second.ID).Terminal()
	})
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "disconnect" || order[1] != "connect" {
		t.Errorf("order = %v, want the reconnect pair in submission order", order)
	}
}

func TestJobsOfDifferentConnectionsRunInParallel(t *testing.T) {
	q, rec := newTestQueue(t)
	started := make(chan string, 2)
	block := make(chan struct{})
	hold := func(name string) func(context.Context, Sink) error {
		return func(ctx context.Context, _ Sink) error {
			started <- name
			<-block
			return nil
		}
	}
	one, _ := q.Submit(Spec{Kind: KindConnect, Title: "one", ConnID: "c1", Run: hold("c1")})
	two, _ := q.Submit(Spec{Kind: KindConnect, Title: "two", ConnID: "c2", Run: hold("c2")})

	// Both must be running before either is released; a serialized queue would
	// deadlock here instead.
	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("only %v started; the two connections did not run in parallel", seen)
		}
	}
	close(block)
	waitFor(t, "both jobs to finish", func() bool {
		return rec.state(one.ID).Terminal() && rec.state(two.ID).Terminal()
	})
}

func TestOnlyTheLastFinishedJobPerConnectionIsKept(t *testing.T) {
	q, rec := newTestQueue(t)
	done := func(context.Context, Sink) error { return nil }

	first, _ := q.Submit(Spec{Kind: KindConnect, Title: "first", ConnID: "c1", Run: done})
	waitFor(t, "the first job", func() bool { return rec.state(first.ID).Terminal() })
	second, _ := q.Submit(Spec{Kind: KindDisconnect, Title: "second", ConnID: "c1", Run: done})
	waitFor(t, "the second job", func() bool { return rec.state(second.ID).Terminal() })
	other, _ := q.Submit(Spec{Kind: KindConnect, Title: "other", ConnID: "c2", Run: done})
	waitFor(t, "the other connection's job", func() bool { return rec.state(other.ID).Terminal() })

	list := q.List()
	if len(list) != 2 {
		t.Fatalf("kept %d jobs, want one per connection: %+v", len(list), list)
	}
	kept := map[string]string{}
	for _, job := range list {
		kept[job.ConnID] = job.Title
	}
	if kept["c1"] != "second" || kept["c2"] != "other" {
		t.Errorf("kept %v, want the latest of each connection", kept)
	}
}

func TestOutputIsReportedAsItArrives(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var lines []string
	q := NewQueue(ctx, Hooks{OnOutput: func(_ string, out []string) {
		mu.Lock()
		lines = append(lines, out...)
		mu.Unlock()
	}})

	job, _ := q.Submit(Spec{Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args: script(t, "echo first\necho second\n")})

	waitFor(t, "the output", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(lines) >= 2
	})
	waitFor(t, "the job to finish", func() bool {
		for _, j := range q.List() {
			if j.ID == job.ID && j.State.Terminal() {
				return true
			}
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	if lines[0] != "first" || lines[1] != "second" {
		t.Errorf("lines = %q, want them in order", lines)
	}
}

func TestTailIsBounded(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args: script(t, "i=0\nwhile [ $i -lt 400 ]; do echo line$i; i=$((i+1)); done\n")})

	waitFor(t, "the job to finish", func() bool { return rec.state(job.ID).Terminal() })
	if got := len(q.List()[0].Tail); got != tailLines {
		t.Errorf("tail = %d lines, want it capped at %d", got, tailLines)
	}
}

func TestQueuedJobIsCancelledBeforeItRuns(t *testing.T) {
	q, rec := newTestQueue(t)
	release := make(chan struct{})
	blocker, _ := q.Submit(Spec{Kind: KindConnect, Title: "blocker", ConnID: "c1",
		Run: func(context.Context, Sink) error { <-release; return nil }})
	waitFor(t, "the first job to start", func() bool { return rec.state(blocker.ID) == StateRunning })

	var ran atomic.Bool
	queued, _ := q.Submit(Spec{Kind: KindConnect, Title: "queued", ConnID: "c1",
		Run: func(context.Context, Sink) error { ran.Store(true); return nil }})
	if err := q.Cancel(queued.ID); err != nil {
		t.Fatal(err)
	}
	close(release)

	waitFor(t, "both jobs to end", func() bool {
		return rec.state(blocker.ID).Terminal() && rec.state(queued.ID).Terminal()
	})
	if got := rec.state(queued.ID); got != StateCancelled {
		t.Errorf("state = %s, want %s", got, StateCancelled)
	}
	if ran.Load() {
		t.Error("a cancelled job ran anyway")
	}
}

func TestCancelKillsACommandThatIgnoresSIGTERM(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{Kind: KindConnect, Title: "stubborn", ConnID: "c1",
		Args: script(t, "trap '' TERM\nwhile :; do sleep 1; done\n")})

	waitFor(t, "the job to start", func() bool { return rec.state(job.ID) == StateRunning })
	if err := q.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	// SIGTERM is ignored, so only the SIGKILL after killGrace ends this job.
	waitFor(t, "the job to end", func() bool { return rec.state(job.ID).Terminal() })
}

func TestOutputDoesNotClearAPendingPrompt(t *testing.T) {
	q, rec := newTestQueue(t)
	// The command asks, then keeps printing progress dots on the same line
	// while it waits. The job must stay in awaiting_input throughout.
	job, _ := q.Submit(Spec{Kind: KindConnect, Title: "connect", ConnID: "c1",
		Args: script(t, "printf 'passphrase: '\n(i=0; while [ $i -lt 6 ]; do printf '.'; sleep 0.5; i=$((i+1)); done) &\nread p\necho \"got:$p\"\n")})

	waitFor(t, "the job to ask", func() bool { return rec.state(job.ID) == StateAwaitingInput })
	for range 6 {
		time.Sleep(400 * time.Millisecond)
		if got := rec.state(job.ID); got != StateAwaitingInput {
			t.Fatalf("state = %s while the command was still waiting; output must not clear the prompt", got)
		}
	}
	if err := q.Input(job.ID, "secret\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to finish", func() bool { return rec.state(job.ID).Terminal() })
}

func TestInputIsRefusedWhenNoPromptIsPending(t *testing.T) {
	q, rec := newTestQueue(t)
	job, _ := q.Submit(Spec{Kind: KindDisconnect, Title: "disconnect", ConnID: "c1",
		Run: func(context.Context, Sink) error { return nil }})
	waitFor(t, "the job to finish", func() bool { return rec.state(job.ID).Terminal() })

	if err := q.Input(job.ID, "x"); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("Input on a finished job = %v, want ErrNotWaiting", err)
	}
}

func TestConcurrentSubmitsKeepTheQueueConsistent(t *testing.T) {
	q, _ := newTestQueue(t)
	done := func(context.Context, Sink) error { return nil }

	var wg sync.WaitGroup
	for conn := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				_, _ = q.Submit(Spec{
					Kind:   KindConnect,
					Title:  "connect",
					ConnID: fmt.Sprintf("c%d", conn),
					Run:    done,
				})
			}
		}()
	}
	wg.Wait()

	// Every id the queue lists must still resolve, and the list must settle to
	// one finished job per connection.
	waitFor(t, "the queue to settle", func() bool { return len(q.List()) == 8 })
	for _, job := range q.List() {
		if job.ID == "" || job.ConnID == "" {
			t.Fatalf("job %+v lost its identity", job)
		}
	}
}
