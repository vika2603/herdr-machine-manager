package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

func TestRepeatedConnectionActionsCoalesce(t *testing.T) {
	for _, method := range []string{ipc.MethodConnect, ipc.MethodDisconnect} {
		t.Run(method, func(t *testing.T) {
			d := newTestDaemon(t)
			conn, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy"})
			if err != nil {
				t.Fatal(err)
			}
			list := "[]"
			operation := "add"
			if method == ipc.MethodDisconnect {
				list = `[{"id":"current","label":"Deploy","target":"deploy"}]`
				operation = "remove"
			}
			dir := t.TempDir()
			release, trace := filepath.Join(dir, "release"), filepath.Join(dir, "calls")
			defer func() { _ = os.WriteFile(release, nil, 0o600) }()
			d.cli = machines.CLI{Bin: fakeHerdr(t, fmt.Sprintf(`case "$2" in
 list) echo '%s';;
 *) echo "$2" >> %q; while [ ! -e %q ]; do sleep 0.01; done;;
 esac`, list, trace, release))}
			d.refresh(t.Context(), false)
			var wg sync.WaitGroup
			replies := make(chan ipc.SaveResult, 50)
			for range 50 {
				wg.Go(func() {
					result, err := d.act(t.Context(), method, ipc.ConnectionTarget{ID: conn.ID})
					if err != nil {
						t.Error(err)
					}
					replies <- result
				})
			}
			wg.Wait()
			close(replies)
			accepted := 0
			for reply := range replies {
				if reply.ID != conn.ID {
					t.Errorf("reply lost connection identity: %+v", reply)
				}
				accepted += len(reply.Jobs)
			}
			if accepted != 1 {
				t.Errorf("queued %d jobs for 50 concurrent requests, want 1", accepted)
			}
			// An explicit form save remains admissible while the connection is busy.
			if method == ipc.MethodDisconnect {
				result, err := d.save(t.Context(), ipc.SaveParams{ID: conn.ID, Label: "Renamed", Target: "deploy"})
				if err != nil || len(result.Jobs) != 1 {
					t.Errorf("busy save = %+v, %v", result, err)
				}
				operation += "\nrename"
			}
			if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			pending := d.queue.List()
			waitForJobState(t, d.queue, pending[len(pending)-1].ID, jobs.StateSucceeded)
			calls, err := os.ReadFile(trace)
			if err != nil || string(calls) != operation+"\n" {
				t.Errorf("commands = %q, %v; want %q", calls, err, operation+"\n")
			}
		})
	}
}

func TestRefreshPublishesOnlyChangedConnectionsOrExplicitRequest(t *testing.T) {
	active := `[{"id":"current","label":"Deploy","target":"deploy"}]`
	for _, tc := range []struct {
		name, initial, list string
		explicit, publish   bool
	}{
		{"adopt", "[]", active, false, true},
		{"unchanged poll", active, active, false, false},
		{"explicit unchanged refresh", active, active, true, true},
		{"endpoint removed", active, "[]", false, true},
		{"another unchanged poll", "[]", "[]", false, false},
		{"failed poll", "[]", "invalid", false, false},
		{"explicit failed refresh", "[]", "invalid", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDaemon(t)
			list := filepath.Join(t.TempDir(), "list.json")
			d.cli = machines.CLI{Bin: fakeHerdr(t, fmt.Sprintf("cat %q", list))}
			if err := os.WriteFile(list, []byte(tc.initial), 0o600); err != nil {
				t.Fatal(err)
			}
			d.refresh(t.Context(), false)
			server, path := attentionServer(t, d.handle)
			d.server = server
			client := herdr.New(path)
			stream, err := client.OpenStream(t.Context(), ipc.MethodSubscribe, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()

			if err := os.WriteFile(list, []byte(tc.list), 0o600); err != nil {
				t.Fatal(err)
			}
			before := d.snapshot().Revision
			if tc.explicit {
				if _, err := d.handle(t.Context(), ipc.MethodRefresh, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				d.refresh(t.Context(), false)
			}
			want := before
			if tc.publish {
				want++
			}
			if got := d.snapshot().Revision; got != want {
				t.Errorf("revision = %d, want %d", got, want)
			}
			// The barrier follows every synchronous refresh broadcast in FIFO order.
			// Reading through it proves absence without a timeout-based negative check.
			server.Broadcast("test.barrier", nil)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			changed := 0
			for {
				event, err := stream.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Event == "test.barrier" {
					break
				}
				if event.Event != ipc.EventConnectionsChanged {
					t.Fatalf("unexpected event %q", event.Event)
				}
				var snapshot ListResult
				if err := json.Unmarshal(event.Data, &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot.Revision != want {
					t.Errorf("broadcast revision = %d, want %d", snapshot.Revision, want)
				}
				changed++
			}
			expected := 0
			if tc.publish {
				expected = 1
			}
			if changed != expected {
				t.Errorf("broadcasts = %d, want %d", changed, expected)
			}
		})
	}
}

func TestSecondDaemonExplainsInstanceConflict(t *testing.T) {
	d := newTestDaemon(t)
	d.env.BinPath = fakeHerdr(t, "echo '[]'")
	lock, err := os.OpenFile(filepath.Join(d.env.StateDir, "manager.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = Run(ctx, d.env)
	if !errors.Is(err, syscall.EWOULDBLOCK) || !strings.Contains(err.Error(), "another instance is running") {
		t.Fatalf("second daemon error = %v", err)
	}
}
