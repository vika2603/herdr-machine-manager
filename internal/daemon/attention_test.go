package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/herdrtest"
	"github.com/vika2603/herdr-client/plugin/plugintest"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func attentionServer(t *testing.T, handler ipc.Handler) (*ipc.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "manager-attention-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "manager.sock")
	server, err := ipc.Listen(path, handler)
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
	t.Cleanup(func() { cancel(); <-done })
	return server, path
}

func awaitPrompt(id string) jobs.Job {
	return jobs.Job{ID: id, State: jobs.StateAwaitingInput, Prompt: "Password:"}
}

func waitAttention(t *testing.T, server *plugintest.Server, index int) herdrtest.Call {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	call, err := server.WaitCall(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func TestPromptDuringPopupStartupIsIncludedInSnapshot(t *testing.T) {
	host := plugintest.NewServer(t)
	release := make(chan struct{})
	defer close(release)
	host.Handle(herdr.MethodPluginPaneOpen, func(ctx context.Context, _ herdrtest.Call) (herdr.Result, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return herdr.PluginPaneOpenedResponse{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{env: host.Env()}
	d.queue = jobs.NewQueue(ctx, jobs.Hooks{OnUpdate: d.requestAttention})
	server, path := attentionServer(t, d.handle)
	d.server = server
	submit := func(id string) jobs.Job {
		job, err := d.queue.Submit(jobs.Spec{ConnID: id, Run: func(ctx context.Context, sink jobs.Sink) error {
			sink.Prompt("Password:")
			<-ctx.Done()
			return ctx.Err()
		}})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	first := submit("first")
	call := waitAttention(t, host, 0)
	var params herdr.PluginPaneOpenParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatal(err)
	}
	if call.Method != herdr.MethodPluginPaneOpen || params.Entrypoint != "prompt" || !params.Focus.ValueOrZero() || params.Width.IsSet() || params.Height.IsSet() {
		t.Fatalf("prompt popup request = %+v", call)
	}
	second := submit("second")
	deadline := time.Now().Add(3 * time.Second)
	for {
		waiting := 0
		for _, job := range d.queue.List() {
			if job.State == jobs.StateAwaitingInput {
				waiting++
			}
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second job did not reach its prompt")
		}
		time.Sleep(time.Millisecond)
	}
	// Subscription precedes the snapshot, as it does in the prompt pane.
	client := herdr.New(path)
	stream, err := client.OpenStream(ctx, ipc.MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var snapshot ListResult
	if err := client.Call(ctx, ipc.MethodList, nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Jobs) != 2 || snapshot.Jobs[0].ID != first.ID || snapshot.Jobs[1].ID != second.ID {
		t.Fatalf("startup snapshot lost a waiting job: %+v", snapshot.Jobs)
	}
	d.requestAttention(awaitPrompt(first.ID))
	d.requestAttention(awaitPrompt(second.ID))
	if len(host.Calls()) != 1 {
		t.Fatalf("opened more than one popup: %+v", host.Calls())
	}
}

func TestAttentionFailureToastsOncePerQuestion(t *testing.T) {
	host := plugintest.NewServer(t).
		Fail(herdr.MethodPluginPaneOpen, "ui_busy", "another popup is open").
		Reply(herdr.MethodNotificationShow, herdr.NotificationShowResponse{})
	server, _ := attentionServer(t, nil)
	d := &Daemon{env: host.Env(), server: server}
	job := awaitPrompt("waiting")
	d.requestAttention(job)
	toast := waitAttention(t, host, 1)
	if toast.Method != herdr.MethodNotificationShow || !strings.Contains(string(toast.Params), "Open the manager") {
		t.Fatalf("missing fallback toast: %+v", toast)
	}
	d.requestAttention(job)
	waitAttentionIdle(t, d)
	d.requestAttention(job)
	waitAttentionIdle(t, d)
	if len(host.Calls()) != 2 {
		t.Fatalf("repeated a failed attempt for the same prompt: %+v", host.Calls())
	}
	d.clearAttention(job.ID)
	d.requestAttention(job)
	waitAttention(t, host, 3)
}

func TestAttentionUsesExistingSubscription(t *testing.T) {
	host := plugintest.NewServer(t).Reply(herdr.MethodPluginPaneOpen, herdr.PluginPaneOpenedResponse{})
	server, path := attentionServer(t, nil)
	d := &Daemon{env: host.Env(), server: server}
	stream, err := herdr.New(path).OpenStream(context.Background(), ipc.MethodSubscribe, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	d.requestAttention(awaitPrompt("visible"))
	waitAttentionIdle(t, d)
	if len(host.Calls()) != 0 {
		t.Fatal("opened another popup while one was subscribed")
	}
	_ = stream.Close()
	deadline := time.Now().Add(3 * time.Second)
	for server.HasSubscribers() {
		if time.Now().After(deadline) {
			t.Fatal("closed popup still appeared subscribed")
		}
		time.Sleep(time.Millisecond)
	}
	d.requestAttention(awaitPrompt("visible"))
	waitAttentionIdle(t, d)
	if len(host.Calls()) != 0 {
		t.Fatal("reopened a dismissed question")
	}
	d.requestAttention(awaitPrompt("new"))
	waitAttention(t, host, 0)
}

func waitAttentionIdle(t *testing.T, d *Daemon) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.attentionMu.Lock()
		opening := d.attentionOpening
		d.attentionMu.Unlock()
		if !opening {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("popup attempt did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
