package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

// fakeDaemon serves handler on a socket in a short temporary directory: macOS
// unix sockets cannot use the long path derived from a test name. stop ends
// the server and its subscriptions.
func fakeDaemon(t *testing.T, path string, handler ipc.Handler) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	server, err := ipc.Listen(path, handler)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = server.Serve(ctx); close(done) }()
	stop = func() { cancel(); _ = server.Close(); <-done }
	t.Cleanup(stop)
	return stop
}

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mm-ui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func TestPromptSendsSelectedAnswerToItsJob(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, text, want string
		yes                      bool
	}{
		{name: "default no", prompt: "Install? [Y/n]", want: "n\n"},
		{name: "selected yes", prompt: "Install? [y/N]", yes: true, want: "y\n"},
		{name: "full word yes", prompt: "Continue? (yes/no)", yes: true, want: "yes\n"},
		{name: "password", prompt: "Password:", text: "test-only-secret", want: "test-only-secret\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := socketPath(t)
			received := make(chan ipc.InputParams, 1)
			fakeDaemon(t, path, func(_ context.Context, method string, raw json.RawMessage) (any, error) {
				var p ipc.InputParams
				if method != ipc.MethodJobInput || json.Unmarshal(raw, &p) != nil {
					return nil, ipc.Errorf(ipc.CodeUnknownMethod, "unexpected request")
				}
				received <- p
				return map[string]string{"status": "ok"}, nil
			})
			m := newModel(context.Background(), herdr.New(path))
			m.mergeJobs([]jobs.Job{waitingJob("target-job", "c1", tc.prompt)})
			if tc.yes {
				m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyRight})
			}
			if tc.text != "" {
				m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.text)})
			}
			m, cmd := promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("submit did not produce a command")
			}
			response := cmd()
			if err := response.(promptReplyMsg).err; err != nil {
				t.Fatalf("submit to fake server: %v", err)
			}
			m, _ = promptUpdate(t, m, response)
			got := <-received
			if got.JobID != "target-job" || got.Data != tc.want || m.dialog != nil {
				t.Errorf("sent %+v, dialog open = %v; want %q to target-job and the dialog closed", got, m.dialog != nil, tc.want)
			}
		})
	}
}

// TestStreamReadsTheListAgainAfterTheDaemonReturns covers a daemon that goes
// away while the popup is open: the drop is shown, and once a subscription is
// open again the list is re-read, which clears the notice.
func TestStreamReadsTheListAgainAfterTheDaemonReturns(t *testing.T) {
	path := socketPath(t)
	handler := func(context.Context, string, json.RawMessage) (any, error) { return nil, nil }
	stop := fakeDaemon(t, path, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := make(chan tea.Msg, 16)
	go stream(ctx, herdr.New(path), func(msg tea.Msg) { msgs <- msg })
	next := func() tea.Msg {
		select {
		case msg := <-msgs:
			return msg
		case <-time.After(5 * time.Second):
			t.Fatal("no message from the stream")
			return nil
		}
	}

	if _, ok := next().(reconnectMsg); !ok {
		t.Fatal("opening the subscription did not request the list")
	}
	stop()
	if _, ok := next().(linkMsg); !ok {
		t.Fatal("a dropped subscription was not reported")
	}
	fakeDaemon(t, path, handler)
	if _, ok := next().(reconnectMsg); !ok {
		t.Fatal("the reopened subscription did not request the list")
	}
}
