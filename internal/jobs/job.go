package jobs

import (
	"context"
	"regexp"
)

type Kind string

const (
	KindConnect    Kind = "connect"
	KindDisconnect Kind = "disconnect"
	KindRename     Kind = "rename"
	KindForget     Kind = "forget"
)

type State string

const (
	StateQueued        State = "queued"
	StateRunning       State = "running"
	StateAwaitingInput State = "awaiting_input"
	StateSucceeded     State = "succeeded"
	StateFailed        State = "failed"
	StateCancelled     State = "cancelled"
)

func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

const tailLines = 200

type Job struct {
	ID     string   `json:"id"`
	Kind   Kind     `json:"kind"`
	Title  string   `json:"title"`
	ConnID string   `json:"conn_id,omitempty"`
	State  State    `json:"state"`
	Prompt string   `json:"prompt,omitempty"`
	Err    string   `json:"error,omitempty"`
	Tail   []string `json:"tail,omitempty"`
}

type Spec struct {
	Kind  Kind
	Title string
	Run   func(ctx context.Context, sink Sink) error
}

type Answer struct {
	Match *regexp.Regexp
	Reply string
}

// Hooks report job progress. They are called off the caller's goroutine and
// must not block.
type Hooks struct {
	OnUpdate func(Job)
	OnOutput func(jobID string, lines []string)
}

type state struct {
	job    Job
	spec   Spec
	input  chan string
	cancel context.CancelFunc
	ctx    context.Context
	done   chan struct{}
}

// event copies the job without its output. Progress is reported many times per
// job and the lines travel on their own, so an event carries no tail.
func (s *state) event() Job {
	out := s.job
	out.Tail = nil
	return out
}
