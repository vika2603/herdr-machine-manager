package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-machine-manager/internal/config"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func attentionServer(t *testing.T) (*ipc.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "manager-attention-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "manager.sock")
	server, err := ipc.Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(ctx); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return server, path
}

func awaitPrompt(id string) jobs.Job {
	return jobs.Job{ID: id, State: jobs.StateAwaitingInput, Prompt: "Password:"}
}

func waitAttention(t *testing.T, calls <-chan herdr.PluginPaneOpenParams) herdr.PluginPaneOpenParams {
	t.Helper()
	select {
	case params := <-calls:
		return params
	case <-time.After(4 * time.Second):
		t.Fatal("manager popup was not requested")
		return herdr.PluginPaneOpenParams{}
	}
}

func TestRequestAttentionOpensOnceForClosedPopup(t *testing.T) {
	server, _ := attentionServer(t)
	calls := make(chan herdr.PluginPaneOpenParams, 2)
	release := make(chan struct{})
	d := &Daemon{
		env:    &plugin.Env{PluginID: "herdr.machine-manager"},
		server: server,
		cfg: config.Config{
			PopupWidth: "70%", PopupHeight: "24",
		},
		attentionOpen: func(_ context.Context, p herdr.PluginPaneOpenParams) error {
			calls <- p
			<-release
			return nil
		},
	}
	job := awaitPrompt("job-1")
	d.requestAttention(job)
	p := waitAttention(t, calls)
	if p.PluginID != d.env.PluginID || p.Entrypoint != "prompt" || !herdr.Value(p.Focus) {
		t.Errorf("popup params = %+v", p)
	}
	if p.Width != nil || p.Height != nil {
		t.Errorf("prompt popup inherited manager size overrides: %+v", p)
	}
	d.requestAttention(job)
	close(release)
	// The first call is in flight when the duplicate arrives. It must neither
	// block the job hook nor issue another pane.open.
	select {
	case <-calls:
		t.Fatal("same prompt opened another popup")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRequestAttentionSkipsOpenPopupAndReopensAfterClose(t *testing.T) {
	server, path := attentionServer(t)
	stream, err := herdr.New(path).OpenStream(context.Background(), ipc.MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	if !server.HasSubscribers() {
		t.Fatal("manager subscription was not registered")
	}
	calls := make(chan herdr.PluginPaneOpenParams, 1)
	d := &Daemon{
		env:    &plugin.Env{PluginID: "herdr.machine-manager"},
		server: server,
		attentionOpen: func(_ context.Context, p herdr.PluginPaneOpenParams) error {
			calls <- p
			return nil
		},
	}
	d.requestAttention(awaitPrompt("already-visible"))
	if len(calls) != 0 {
		t.Fatal("opened another popup while one was subscribed")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for server.HasSubscribers() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.HasSubscribers() {
		t.Fatal("closed popup still appeared subscribed")
	}
	d.requestAttention(awaitPrompt("new-prompt"))
	waitAttention(t, calls)
}

func TestRequestAttentionCanRetryFailedOpen(t *testing.T) {
	server, _ := attentionServer(t)
	calls := make(chan herdr.PluginPaneOpenParams, 2)
	d := &Daemon{
		env:    &plugin.Env{PluginID: "herdr.machine-manager"},
		server: server,
		attentionOpen: func(_ context.Context, p herdr.PluginPaneOpenParams) error {
			calls <- p
			return errors.New("Herdr unavailable")
		},
	}
	job := awaitPrompt("failed-open")
	d.requestAttention(job)
	waitAttention(t, calls)
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.attentionMu.Lock()
		opening := d.attentionOpening
		d.attentionMu.Unlock()
		if !opening {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed popup request did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	d.requestAttention(job)
	waitAttention(t, calls)
}

func TestPromptDuringPopupStartupIsNotLost(t *testing.T) {
	server, _ := attentionServer(t)
	calls := make(chan herdr.PluginPaneOpenParams, 2)
	release := make(chan struct{})
	var count atomic.Int32
	d := &Daemon{
		env:    &plugin.Env{PluginID: "herdr.machine-manager"},
		server: server,
		attentionOpen: func(_ context.Context, p herdr.PluginPaneOpenParams) error {
			calls <- p
			if count.Add(1) == 1 {
				<-release
			}
			return nil
		},
	}
	d.requestAttention(awaitPrompt("first"))
	waitAttention(t, calls)
	d.requestAttention(awaitPrompt("second"))
	close(release)
	// If the first pane never subscribes, the second waiting job still needs
	// an attention attempt once pane startup's grace period has passed.
	waitAttention(t, calls)
}
