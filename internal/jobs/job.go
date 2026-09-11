// Package jobs runs the herdr machine commands the plugin issues and keeps
// their state. Every mutation is a job so that the TUI never waits on a
// process: `herdr machine add` prepares a remote host and can take minutes.
package jobs

import (
	"context"
	"regexp"
)

// Kind is the mutation a job performs.
type Kind string

const (
	// KindConnect runs `herdr machine add`, which is how a stored connection
	// becomes one herdr holds.
	KindConnect Kind = "connect"
	// KindDisconnect runs `herdr machine remove`. The connection stays in the
	// plugin's own store, which is why the plugin needs no herdr disable.
	KindDisconnect Kind = "disconnect"
	KindRename     Kind = "rename"
	KindForget     Kind = "forget"
)

// State is where a job is in its life.
type State string

const (
	StateQueued        State = "queued"
	StateRunning       State = "running"
	StateAwaitingInput State = "awaiting_input"
	StateSucceeded     State = "succeeded"
	StateFailed        State = "failed"
	StateCancelled     State = "cancelled"
)

// Terminal reports whether s is an end state.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// tailLines bounds the output kept per job: enough to show what a command is
// doing and to match a prompt against, not a full transcript.
const tailLines = 200

// Job is one queued or finished mutation, as the TUI sees it.
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

// Spec describes the command a job runs. It either calls Run or executes Args
// under a PTY.
type Spec struct {
	Kind   Kind
	Title  string
	ConnID string
	// Run is the herdr commands that finish immediately, through the wrapper
	// that reports their stderr.
	Run func(ctx context.Context, sink Sink) error
	// Args is `herdr machine add`, which runs under a PTY because it asks
	// before installing herdr on the remote and ssh may ask for a password.
	Args []string
	// Answers reply to prompts the caller already decided about. Anything not
	// matched moves the job to StateAwaitingInput instead of being guessed at.
	Answers []Answer
}

// Answer replies to one expected prompt.
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

// state is the queue's own view of a job, with the parts the wire does not
// carry.
type state struct {
	job       Job
	spec      Spec
	input     chan string
	cancel    context.CancelFunc
	cancelled bool
}

// snapshot copies the job with its output, for a caller reading the list.
func (s *state) snapshot() Job {
	out := s.job
	out.Tail = append([]string(nil), s.job.Tail...)
	return out
}

// event copies the job without its output. Progress is reported many times per
// job and the lines travel on their own, so an event carries no tail.
func (s *state) event() Job {
	out := s.job
	out.Tail = nil
	return out
}

func (s *state) appendLines(lines []string) {
	s.job.Tail = append(s.job.Tail, lines...)
	if extra := len(s.job.Tail) - tailLines; extra > 0 {
		s.job.Tail = append([]string(nil), s.job.Tail[extra:]...)
	}
}
