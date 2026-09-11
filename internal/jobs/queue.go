package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// laneDepth bounds how many jobs can wait on one connection. A connection has
// at most a disconnect and a connect in flight, so reaching this means
// something is submitting in a loop.
const laneDepth = 8

// laneIdle is how long a connection's worker waits with nothing to do before
// it retires, so that connections seen once do not each keep a goroutine.
const laneIdle = time.Minute

// ErrNotFound is returned for an unknown job id.
var ErrNotFound = errors.New("jobs: no such job")

// ErrNotWaiting is returned when input arrives for a job that is not asking
// for any.
var ErrNotWaiting = errors.New("jobs: the job is not waiting for input")

// Queue runs jobs and reports what they are doing.
//
// Jobs are serialized per connection and run in parallel across connections:
// preparing a remote host takes minutes and two of them have no reason to wait
// for each other, while the two jobs of a reconnect must stay in order.
//
// Nothing is kept for browsing. The queue holds the jobs that have not
// finished plus the last finished one per connection, which is the error a row
// shows and the output its detail view shows.
type Queue struct {
	ctx   context.Context
	hooks Hooks
	ids   atomic.Uint64

	mu    sync.Mutex
	order []string
	byID  map[string]*state
	lanes map[string]chan string
}

// NewQueue builds a queue whose jobs run under ctx.
func NewQueue(ctx context.Context, hooks Hooks) *Queue {
	if hooks.OnUpdate == nil {
		hooks.OnUpdate = func(Job) {}
	}
	if hooks.OnOutput == nil {
		hooks.OnOutput = func(string, []string) {}
	}
	return &Queue{
		ctx:   ctx,
		hooks: hooks,
		byID:  map[string]*state{},
		lanes: map[string]chan string{},
	}
}

// Submit accepts a job and returns it in its queued state.
func (q *Queue) Submit(spec Spec) (Job, error) {
	id := fmt.Sprintf("job-%d", q.ids.Add(1))
	st := &state{
		job: Job{
			ID:     id,
			Kind:   spec.Kind,
			Title:  spec.Title,
			ConnID: spec.ConnID,
			State:  StateQueued,
		},
		spec:  spec,
		input: make(chan string, 1),
	}

	// Registering the job and handing it to its lane happen under one lock:
	// the lane worker retires itself under the same lock, so a job can never
	// be delivered to a channel nobody reads.
	q.mu.Lock()
	lane := q.lane(spec.ConnID)
	select {
	case lane <- id:
	default:
		q.mu.Unlock()
		return Job{}, fmt.Errorf("jobs: %s already has %d jobs queued", spec.Title, laneDepth)
	}
	q.byID[id] = st
	q.order = append(q.order, id)
	event := st.event()
	q.mu.Unlock()

	q.hooks.OnUpdate(event)
	return event, nil
}

// List returns the jobs the queue still holds, oldest first, each with the
// tail of its output.
func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.order))
	for _, id := range q.order {
		if st, ok := q.byID[id]; ok {
			out = append(out, st.snapshot())
		}
	}
	return out
}

// Input forwards what the user typed to a job waiting on a prompt.
func (q *Queue) Input(id, data string) error {
	q.mu.Lock()
	st, ok := q.byID[id]
	waiting := ok && st.job.State == StateAwaitingInput
	q.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if !waiting {
		return ErrNotWaiting
	}
	select {
	case st.input <- data:
		return nil
	default:
		return ErrNotWaiting
	}
}

// Cancel stops a job, whether it is running or still queued.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	st, ok := q.byID[id]
	if !ok {
		q.mu.Unlock()
		return ErrNotFound
	}
	// A job that has not started yet has no context to cancel; the flag is
	// what its lane sees when it picks it up.
	st.cancelled = true
	cancel := st.cancel
	q.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// lane returns the channel that serializes one connection's jobs, starting its
// worker the first time the connection is used. The caller holds the lock.
func (q *Queue) lane(connID string) chan string {
	if ch, ok := q.lanes[connID]; ok {
		return ch
	}
	ch := make(chan string, laneDepth)
	q.lanes[connID] = ch
	go q.serveLane(connID, ch)
	return ch
}

// serveLane runs one connection's jobs in order and retires when the
// connection has been idle, so a daemon that outlives many connections does
// not accumulate goroutines.
func (q *Queue) serveLane(connID string, ch chan string) {
	idle := time.NewTimer(laneIdle)
	defer idle.Stop()
	for {
		select {
		case <-q.ctx.Done():
			return
		case id := <-ch:
			q.run(id)
			idle.Reset(laneIdle)
		case <-idle.C:
			q.mu.Lock()
			if len(ch) == 0 && q.lanes[connID] == ch {
				delete(q.lanes, connID)
				q.mu.Unlock()
				return
			}
			q.mu.Unlock()
			idle.Reset(laneIdle)
		}
	}
}

func (q *Queue) run(id string) {
	q.mu.Lock()
	st, ok := q.byID[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	if st.cancelled {
		q.mu.Unlock()
		q.finish(id, StateCancelled, nil)
		return
	}
	ctx, cancel := context.WithCancel(q.ctx)
	defer cancel()
	st.cancel = cancel
	st.job.State = StateRunning
	spec := st.spec
	event := st.event()
	q.mu.Unlock()
	q.hooks.OnUpdate(event)

	sink := Sink{
		Lines: func(lines []string) {
			q.mu.Lock()
			st.appendLines(lines)
			q.mu.Unlock()
			q.hooks.OnOutput(id, lines)
		},
		Prompt: func(text string) {
			q.mu.Lock()
			switch {
			case text != "":
				st.job.State, st.job.Prompt = StateAwaitingInput, text
			case st.job.State == StateAwaitingInput:
				st.job.State, st.job.Prompt = StateRunning, ""
			default:
				q.mu.Unlock()
				return
			}
			event := st.event()
			q.mu.Unlock()
			q.hooks.OnUpdate(event)
		},
		Input: st.input,
	}

	code, err := execute(ctx, spec, sink)
	switch {
	case ctx.Err() != nil && q.ctx.Err() == nil:
		q.finish(id, StateCancelled, nil)
	case err != nil:
		q.finish(id, StateFailed, err)
	case code != 0:
		q.finish(id, StateFailed, fmt.Errorf("exit status %d", code))
	default:
		q.finish(id, StateSucceeded, nil)
	}
}

func (q *Queue) finish(id string, s State, err error) {
	q.mu.Lock()
	st, ok := q.byID[id]
	if !ok {
		q.mu.Unlock()
		return
	}
	st.job.State = s
	st.job.Prompt = ""
	if err != nil {
		st.job.Err = err.Error()
	}
	event := st.event()
	q.prune()
	q.mu.Unlock()
	q.hooks.OnUpdate(event)
}

// prune drops every finished job but the latest of each connection. The caller
// holds the lock.
func (q *Queue) prune() {
	latest := make(map[string]string, len(q.order))
	for _, id := range q.order {
		if st, ok := q.byID[id]; ok && st.job.State.Terminal() {
			latest[st.job.ConnID] = id
		}
	}
	q.order = slices.DeleteFunc(q.order, func(id string) bool {
		st, ok := q.byID[id]
		if !ok {
			return true
		}
		if st.job.State.Terminal() && latest[st.job.ConnID] != id {
			delete(q.byID, id)
			return true
		}
		return false
	})
}
