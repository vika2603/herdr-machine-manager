package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

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
			ctx, cancel := context.WithCancel(context.Background())
			// macOS unix sockets cannot use the long path derived from a test name.
			dir, err := os.MkdirTemp("", "prompt-")
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			path := filepath.Join(dir, "s")
			received := make(chan ipc.InputParams, 1)
			server, err := ipc.Listen(path, func(_ context.Context, method string, raw json.RawMessage) (any, error) {
				if method != ipc.MethodJobInput {
					return nil, ipc.Errorf(ipc.CodeUnknownMethod, "unexpected method")
				}
				var p ipc.InputParams
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				received <- p
				return map[string]string{"status": "ok"}, nil
			})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { _ = server.Serve(ctx); close(done) }()
			t.Cleanup(func() { cancel(); _ = server.Close(); <-done })
			m := newModel(ctx, herdr.New(path))
			m.mergeJobs([]jobs.Job{waitingJob("target-job", "c1", tc.prompt)})
			if tc.yes {
				m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyTab})
			}
			if tc.text != "" {
				m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.text)})
			}
			m, cmd := promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("submit did not produce a command")
			}
			response := cmd()
			if response.(promptReplyMsg).err != nil {
				t.Fatalf("submit to fake server: %v", response.(promptReplyMsg).err)
			}
			m, _ = promptUpdate(t, m, response)
			got := <-received
			if got.JobID != "target-job" || got.Data != tc.want || m.dialog != nil {
				t.Error("answer was not delivered to the correct job or dialog remained after acknowledgment")
			}
		})
	}
}
