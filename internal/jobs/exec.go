package jobs

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
)

// promptIdle is how long output has to stop, with an unterminated last line on
// screen, before that line is treated as a prompt waiting for an answer.
const promptIdle = 1500 * time.Millisecond

// Keep only the end of an unterminated line for prompt detection. A remote
// command can otherwise exhaust the daemon's memory by writing without '\n'.
const maxPendingBytes = 4096

// A command ignoring SIGTERM must eventually release its connection's queue.
const killGrace = 5 * time.Second

type Sink struct {
	// Lines reports complete output lines.
	Lines func([]string)
	// Prompt reports the unterminated text the command stopped on, and ""
	// once it is running again.
	Prompt func(string)
	// Input carries what the user typed for a waiting job.
	Input <-chan string
}

func Exec(ctx context.Context, args []string, answers []Answer, sink Sink) error {
	if len(args) == 0 {
		return fmt.Errorf("jobs: empty command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	// Fixed dimensions keep command output stable as the popup resizes.
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		return err
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
	truncated := false
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
		truncated = false
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
				return cmd.Wait()
			}
			pending += chunk
			if cut := strings.LastIndexByte(pending, '\n'); cut >= 0 {
				sink.Lines(splitLines(pending[:cut+1]))
				pending = pending[cut+1:]
				truncated = false
				// The line the prompt was on has ended, so there is no longer
				// a prompt on screen. Output that does not end the line —
				// progress dots, a spinner — leaves the job waiting.
				if waiting {
					waiting = false
					sink.Prompt("")
				}
			}
			if len(pending) > maxPendingBytes {
				start := len(pending) - maxPendingBytes
				for start < len(pending) && !utf8.RuneStart(pending[start]) {
					start++
				}
				pending = pending[start:]
				if !truncated {
					sink.Lines([]string{"[long output line truncated]"})
					truncated = true
				}
			}
			for _, a := range answers {
				if text := strings.TrimSpace(pending); text != "" && a.Match.MatchString(text) {
					if err := answer(a.Reply); err != nil {
						return err
					}
					break
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
				return err
			}
			idle.Reset(promptIdle)
		}
	}
}

func splitLines(s string) []string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\r")
	}
	return lines
}
