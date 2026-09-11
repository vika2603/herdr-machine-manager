package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// ptyCols and ptyRows are the terminal the command believes it has. A fixed
// size keeps the output stable however the popup is resized: the TUI shows the
// tail of it, it is not a terminal emulator.
const (
	ptyCols = 120
	ptyRows = 40
)

// promptIdle is how long output has to stop, with an unterminated last line on
// screen, before that line is treated as a prompt waiting for an answer.
const promptIdle = 1500 * time.Millisecond

// killGrace is how long a cancelled command has to act on SIGTERM before it is
// killed. Without it a command that ignores the signal would hold its
// connection's lane forever.
const killGrace = 5 * time.Second

// Sink receives what a running command produces.
type Sink struct {
	// Lines reports complete output lines.
	Lines func([]string)
	// Prompt reports the unterminated text the command stopped on, and ""
	// once it is running again.
	Prompt func(string)
	// Input carries what the user typed for a waiting job.
	Input <-chan string
}

// execute runs a job's command.
func execute(ctx context.Context, spec Spec, sink Sink) (int, error) {
	if spec.Run != nil {
		if err := spec.Run(ctx, sink); err != nil {
			return 1, err
		}
		return 0, nil
	}
	if len(spec.Args) == 0 {
		return 0, errors.New("jobs: empty command")
	}
	return execPTY(ctx, spec, sink)
}

func execPTY(ctx context.Context, spec Spec, sink Sink) (int, error) {
	cmd := exec.Command(spec.Args[0], spec.Args[1:]...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: ptyCols, Rows: ptyRows})
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	// The command runs in its own session, so cancelling signals the whole
	// group: ssh and whatever it started on the remote side go with it. SIGKILL
	// follows, because a command that ignores SIGTERM would otherwise never
	// release this job's lane.
	pid := cmd.Process.Pid
	released := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-time.After(killGrace):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case <-released:
		}
	})
	defer func() { stop(); close(released) }()

	// Reading ends at EOF, which on a PTY arrives as EIO once the child is
	// gone; either way the closed channel is what ends the loop, and cmd.Wait
	// reports the failure that matters.
	chunks := make(chan string)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				chunks <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	var pending string
	waiting := false
	idle := time.NewTimer(promptIdle)
	defer idle.Stop()

	// answer writes to the PTY, whether the reply came from the spec or from
	// the user. The prompt itself is reported as output first: it is the one
	// line the user never sees otherwise, because answering consumes it.
	answer := func(reply string) error {
		if text := strings.TrimSpace(pending); text != "" {
			sink.Lines([]string{text})
		}
		pending = ""
		if _, err := io.WriteString(f, reply); err != nil {
			return fmt.Errorf("jobs: cannot answer the prompt: %w", err)
		}
		if waiting {
			waiting = false
			sink.Prompt("")
		}
		return nil
	}

	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				stop()
				if rest := strings.TrimSpace(pending); rest != "" {
					sink.Lines([]string{rest})
				}
				return exitStatus(cmd.Wait())
			}
			pending += chunk
			if cut := strings.LastIndexByte(pending, '\n'); cut >= 0 {
				sink.Lines(splitLines(pending[:cut+1]))
				pending = pending[cut+1:]
				// The line the prompt was on has ended, so there is no longer
				// a prompt on screen. Output that does not end the line —
				// progress dots, a spinner — leaves the job waiting.
				if waiting {
					waiting = false
					sink.Prompt("")
				}
			}
			if reply, ok := matchAnswer(spec.Answers, pending); ok {
				if err := answer(reply); err != nil {
					return 0, err
				}
			}
			idle.Reset(promptIdle)

		case <-idle.C:
			if text := strings.TrimSpace(pending); text != "" && !waiting {
				waiting = true
				sink.Prompt(text)
			}
			idle.Reset(promptIdle)

		case in := <-sink.Input:
			if err := answer(in); err != nil {
				return 0, err
			}
			idle.Reset(promptIdle)
		}
	}
}

func matchAnswer(answers []Answer, text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	for _, a := range answers {
		if a.Match.MatchString(trimmed) {
			return a.Reply, true
		}
	}
	return "", false
}

// exitStatus turns what cmd.Wait reported into an exit code, so that a command
// that ran and failed is told apart from one that could not run.
func exitStatus(err error) (int, error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func splitLines(s string) []string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\r")
	}
	return lines
}
