package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

var ErrNotFound = errors.New("jobs: no such job")

var ErrNotWaiting = errors.New("jobs: the job is not waiting for input")

type Queue struct {
	ctx   context.Context
	hooks Hooks
	ids   uint64

	mu   sync.Mutex
	jobs []*state
}

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
	}
}

func (q *Queue) Submit(connID string, spec Spec) Job {
	q.mu.Lock()
	var previous <-chan struct{}
	for i := len(q.jobs) - 1; i >= 0; i-- {
		if q.jobs[i].job.ConnID == connID {
			previous = q.jobs[i].done
			break
		}
	}
	q.ids++
	ctx, cancel := context.WithCancel(q.ctx)
	st := &state{
		ctx: ctx, cancel: cancel,
		job:   Job{ID: fmt.Sprintf("job-%d", q.ids), Kind: spec.Kind, Title: spec.Title, ConnID: connID, State: StateQueued},
		spec:  spec,
		input: make(chan string, 1),
		done:  make(chan struct{}),
	}
	q.jobs = append(q.jobs, st)
	event := st.event()
	q.mu.Unlock()
	q.hooks.OnUpdate(event)
	go func() {
		if previous != nil {
			<-previous
		}
		q.run(st)
		close(st.done)
	}()
	return event
}

func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for _, st := range q.jobs {
		job := st.job
		job.Tail = slices.Clone(job.Tail)
		out = append(out, job)
	}
	return out
}

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
	if st.ctx.Err() != nil {
		q.update(st, StateCancelled, "", nil)
		return
	}
	q.update(st, StateRunning, "", nil)
	err := st.spec.Run(st.ctx, Sink{
		Lines: func(lines []string) {
			q.mu.Lock()
			st.job.Tail = append(st.job.Tail, lines...)
			if len(st.job.Tail) > tailLines {
				st.job.Tail = slices.Clone(st.job.Tail[len(st.job.Tail)-tailLines:])
			}
			q.mu.Unlock()
			q.hooks.OnOutput(st.job.ID, lines)
		},
		Prompt: func(text string) {
			phase := StateRunning
			if text != "" {
				phase = StateAwaitingInput
			}
			q.update(st, phase, text, nil)
		},
		Input: st.input,
	})
	phase := StateSucceeded
	if st.ctx.Err() != nil {
		phase, err = StateCancelled, nil
	} else if err != nil {
		phase = StateFailed
	}
	q.update(st, phase, "", err)
}

func (q *Queue) update(st *state, phase State, prompt string, err error) {
	q.mu.Lock()
	st.job.State, st.job.Prompt = phase, prompt
	if err != nil {
		st.job.Err = err.Error()
	}
	if phase.Terminal() {
		q.jobs = slices.DeleteFunc(q.jobs, func(other *state) bool {
			return other != st && other.job.ConnID == st.job.ConnID && other.job.State.Terminal()
		})
	}
	event := st.event()
	q.mu.Unlock()
	q.hooks.OnUpdate(event)
}
