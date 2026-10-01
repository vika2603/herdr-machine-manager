package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// laneDepth bounds how many jobs can wait on one connection. A connection has
// at most a disconnect and a connect in flight, so reaching this means
// something is submitting in a loop.
const laneDepth = 8

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
	ids   uint64

	mu   sync.Mutex
	jobs []*state
	last map[string]chan struct{}
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
		last:  map[string]chan struct{}{},
	}
}

// Submit accepts a job and returns it in its queued state.
func (q *Queue) Submit(spec Spec) (Job, error) {
	jobs, err := q.SubmitBatch(spec)
	if err != nil {
		return Job{}, err
	}
	return jobs[0], nil
}

// SubmitBatch queues jobs for one connection atomically: a reconnect must
// never enqueue its disconnect unless there is room for its connect too.
func (q *Queue) SubmitBatch(specs ...Spec) ([]Job, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	q.mu.Lock()
	if err := q.ctx.Err(); err != nil {
		q.mu.Unlock()
		return nil, err
	}
	connID := specs[0].ConnID
	for _, spec := range specs[1:] {
		if spec.ConnID != connID {
			q.mu.Unlock()
			return nil, errors.New("jobs: batch spans multiple connections")
		}
	}
	queued := 0
	for _, st := range q.jobs {
		if st.job.ConnID == connID && st.job.State == StateQueued {
			queued++
		}
	}
	if len(specs) > laneDepth-queued {
		q.mu.Unlock()
		return nil, fmt.Errorf("jobs: %s already has too many jobs queued (limit %d)", specs[0].Title, laneDepth)
	}
	previous := q.last[connID]
	done := make(chan struct{})
	q.last[connID] = done
	batch := make([]*state, 0, len(specs))
	out := make([]Job, 0, len(specs))
	for _, spec := range specs {
		q.ids++
		id := fmt.Sprintf("job-%d", q.ids)
		ctx, cancel := context.WithCancel(q.ctx)
		st := &state{
			ctx: ctx, cancel: cancel,
			job:   Job{ID: id, Kind: spec.Kind, Title: spec.Title, ConnID: spec.ConnID, State: StateQueued},
			spec:  spec,
			input: make(chan string, 1),
		}
		q.jobs = append(q.jobs, st)
		batch = append(batch, st)
		out = append(out, st.event())
	}
	q.mu.Unlock()
	for _, job := range out {
		q.hooks.OnUpdate(job)
	}
	// A completion channel orders batches without idle workers or timers.
	go func() {
		if previous != nil {
			<-previous
		}
		for _, st := range batch {
			q.run(st)
		}
		q.mu.Lock()
		if q.last[connID] == done {
			delete(q.last, connID)
		}
		close(done)
		q.mu.Unlock()
	}()
	return out, nil
}

// List returns the jobs the queue still holds, oldest first, each with the
// tail of its output.
func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for _, st := range q.jobs {
		out = append(out, st.snapshot())
	}
	return out
}

// Input forwards what the user typed to a job waiting on a prompt.
func (q *Queue) Input(id, data string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	st := q.find(id)
	if st == nil {
		return ErrNotFound
	}
	if st.job.State != StateAwaitingInput {
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
	defer q.mu.Unlock()
	st := q.find(id)
	if st == nil {
		return ErrNotFound
	}
	st.cancel()
	return nil
}

// find returns a retained job. The caller holds the lock.
func (q *Queue) find(id string) *state {
	for _, st := range q.jobs {
		if st.job.ID == id {
			return st
		}
	}
	return nil
}

func (q *Queue) run(st *state) {
	defer st.cancel()
	q.mu.Lock()
	if st.ctx.Err() != nil {
		q.mu.Unlock()
		q.finish(st, StateCancelled, nil)
		return
	}
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
			q.hooks.OnOutput(st.job.ID, lines)
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

	code, err := execute(st.ctx, spec, sink)
	switch {
	case st.ctx.Err() != nil:
		q.finish(st, StateCancelled, nil)
	case err != nil:
		q.finish(st, StateFailed, err)
	case code != 0:
		q.finish(st, StateFailed, fmt.Errorf("exit status %d", code))
	default:
		q.finish(st, StateSucceeded, nil)
	}
}

func (q *Queue) finish(st *state, s State, err error) {
	q.mu.Lock()
	st.job.State = s
	st.job.Prompt = ""
	if err != nil {
		st.job.Err = err.Error()
	}
	event := st.event()
	q.jobs = slices.DeleteFunc(q.jobs, func(other *state) bool {
		return other != st && other.job.ConnID == st.job.ConnID && other.job.State.Terminal()
	})
	q.mu.Unlock()
	q.hooks.OnUpdate(event)
}
