package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

func TestInstallationPolicy(t *testing.T) {
	for _, tc := range []struct {
		prompt  string
		decline bool
	}{
		{"install the remote herdr binary?", true},
		{`Install the 0.9.0 stable asset for linux-x86_64 to "$HOME/.local/bin/herdr"? [Y/n]`, true},
		{`Install the 0.9.0 stable asset for linux-x86_64 to "$HOME/.local/bin/other"? [Y/n]`, false},
		{"stop incompatible remote server?", false}, {"password:", false},
	} {
		for _, install := range []bool{false, true} {
			decline := false
			for _, answer := range addAnswers(install) {
				if answer.Match.MatchString(tc.prompt) {
					decline = true
					if answer.Reply != "n\n" {
						t.Fatalf("unsafe automatic answer: %q", answer.Reply)
					}
				}
			}
			if decline != (tc.decline && !install) {
				t.Errorf("prompt %q, install=%v, decline=%v", tc.prompt, install, decline)
			}
		}
	}
}

func TestConnectHonorsInstallationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		allow, install, form, ask bool
	}{
		{"list disabled by config", false, true, false, false},
		{"list allowed", true, true, false, true},
		{"list explicit refusal", true, false, false, false},
		{"form overrides default", false, true, true, true},
		{"form explicit refusal", true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDaemon(t)
			d.cfg.InstallRemote = tc.allow
			d.cli = machines.CLI{Bin: fakeHerdr(t, "if [ \"$2\" = list ]; then echo '[]'; exit 0; fi\nprintf 'install the remote herdr binary? [y/N] '\nread answer\necho answered:$answer\n")}
			conn, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy"})
			if err != nil {
				t.Fatal(err)
			}
			method := ipc.MethodConnect
			var params any = ipc.ConnectionTarget{ID: conn.ID, Install: tc.install}
			if tc.form {
				method, params = ipc.MethodSave, ipc.SaveParams{Label: "New", Target: "new", Install: tc.install}
			}
			raw, _ := json.Marshal(params)
			result, err := d.handle(t.Context(), method, raw)
			if err != nil {
				t.Fatal(err)
			}
			id := result.(ipc.SaveResult).Jobs[0]
			if tc.ask {
				waitForJobState(t, d.queue, id, jobs.StateAwaitingInput)
				if err := d.queue.Input(id, "n\n"); err != nil {
					t.Fatal(err)
				}
			}
			waitForJobState(t, d.queue, id, jobs.StateSucceeded)
			if !strings.Contains(strings.Join(d.queue.List()[0].Tail, "\n"), "answered:n") {
				t.Fatal("installation answer did not reach command")
			}
		})
	}
}

func TestReconnectRemovesCurrentEndpointBeforeAdd(t *testing.T) {
	for _, tc := range []struct {
		name, currentID, removeResult, wantCalls string
		wantState                                jobs.State
	}{
		{"changed endpoint", "current", "", "remove:current\nadd\n", jobs.StateSucceeded},
		{"endpoint gone", "", "", "add\n", jobs.StateSucceeded},
		{"removal failed", "current", "exit 7", "remove:current\n", jobs.StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDaemon(t)
			conn, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "stale"})
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(t.TempDir(), "calls")
			t.Setenv("MM_TEST_TRACE", trace)
			t.Setenv("MM_TEST_REMOVE_RESULT", tc.removeResult)
			d.cli = machines.CLI{Bin: fakeHerdr(t, `case "$2" in
 remove) echo "remove:$3" >> "$MM_TEST_TRACE"; eval "$MM_TEST_REMOVE_RESULT";;
 add) echo add >> "$MM_TEST_TRACE";;
 esac`)}
			release := blockConnectionJob(t, d, conn.ID)
			id := d.enqueue(conn, jobs.KindConnect, true, true)
			conn.ProfileID = tc.currentID
			if _, err := d.store.Put(conn); err != nil {
				t.Fatal(err)
			}
			release()
			waitForJobState(t, d.queue, id, tc.wantState)
			calls, err := os.ReadFile(trace)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if string(calls) != tc.wantCalls {
				t.Fatalf("calls = %q, want %q", calls, tc.wantCalls)
			}
		})
	}
}

func TestQueuedEndpointJobUsesCurrentProfileID(t *testing.T) {
	for _, tc := range []struct {
		name, currentID, wantCalls string
		kind                       jobs.Kind
	}{
		{"disconnect changed endpoint", "current", "remove:current\n", jobs.KindDisconnect},
		{"disconnect endpoint gone", "", "", jobs.KindDisconnect},
		{"rename changed endpoint", "current", "rename:current:Deploy\n", jobs.KindRename},
		{"rename endpoint gone", "", "", jobs.KindRename},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDaemon(t)
			conn, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "stale"})
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(t.TempDir(), "calls")
			t.Setenv("MM_TEST_TRACE", trace)
			d.cli = machines.CLI{Bin: fakeHerdr(t, `case "$2" in
 remove) echo "remove:$3" >> "$MM_TEST_TRACE";;
 rename) echo "rename:$3:$5" >> "$MM_TEST_TRACE";;
 esac`)}
			release := blockConnectionJob(t, d, conn.ID)
			id := d.enqueue(conn, tc.kind, false, false)
			conn.ProfileID = tc.currentID
			if _, err := d.store.Put(conn); err != nil {
				t.Fatal(err)
			}
			release()
			waitForJobState(t, d.queue, id, jobs.StateSucceeded)
			calls, err := os.ReadFile(trace)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if string(calls) != tc.wantCalls {
				t.Fatalf("calls = %q, want %q", calls, tc.wantCalls)
			}
		})
	}
}

func blockConnectionJob(t *testing.T, d *Daemon, connID string) func() {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	d.queue.Submit(connID, jobs.Spec{Run: func(_ context.Context, _ jobs.Sink) error {
		close(started)
		<-release
		return nil
	}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("blocking job did not start")
	}
	var once sync.Once
	done := func() { once.Do(func() { close(release) }) }
	t.Cleanup(done)
	return done
}

func waitForJobState(t *testing.T, q *jobs.Queue, id string, state jobs.State) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range q.List() {
			if job.ID == id && job.State == state {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %q did not reach %s: %+v", id, state, q.List())
}
