package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/vika2603/herdr-client/plugin"
	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	env := &plugin.Env{StateDir: t.TempDir()}
	s, err := store.Open(filepath.Join(env.StateDir, "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Daemon{env: env, store: s, queue: jobs.NewQueue(t.Context(), jobs.Hooks{})}
}

func fakeHerdr(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReconcile(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stored   []store.Connection
		actual   []machines.Machine
		profiles []string
	}{
		{"reserve known IDs", []store.Connection{
			{ID: "removed", Label: "Deploy", Target: "deploy", ProfileID: "id-1"},
			{ID: "owner", Label: "Deploy", Target: "deploy", ProfileID: "id-2"},
		}, []machines.Machine{{ID: "id-2", Label: "Deploy", Target: "deploy"}}, []string{"", "id-2"}},
		{"match by ID despite changed labels", []store.Connection{
			{ID: "first", Label: "Deploy", Target: "deploy", ProfileID: "id-1"},
			{ID: "second", Label: "Deploy", Target: "deploy", ProfileID: "id-2"},
		}, []machines.Machine{{ID: "id-2"}, {ID: "id-1"}}, []string{"id-1", "id-2"}},
		{"keep offline, match new, clear stale, adopt external", []store.Connection{
			{ID: "offline", Label: "Offline", Target: "offline"},
			{ID: "new", Label: "Deploy", Target: "deploy"},
			{ID: "stale", Label: "Stale", Target: "stale", ProfileID: "gone"},
		}, []machines.Machine{{ID: "fresh", Label: "Deploy", Target: "deploy"}, {ID: "external", Label: "Build", Target: "build", Session: "work"}}, []string{"", "fresh", "", "external"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDaemon(t)
			for _, c := range tc.stored {
				if _, err := d.store.Put(c); err != nil {
					t.Fatal(err)
				}
			}
			got := d.reconcile(tc.actual)
			if len(got) != len(tc.profiles) {
				t.Fatalf("connections = %+v", got)
			}
			for i, c := range got {
				if c.ProfileID != tc.profiles[i] || c.Active != (tc.profiles[i] != "") {
					t.Errorf("connection %d = %+v", i, c)
				}
				if i < len(tc.stored) && c.ID != tc.stored[i].ID {
					t.Errorf("replaced stored connection %q", c.ID)
				}
				persisted, ok := d.store.Get(c.ID)
				if !ok || persisted != c.Connection {
					t.Errorf("disk candidate differs from view: %+v", persisted)
				}
			}
			if len(got) > len(tc.stored) {
				last := got[len(got)-1]
				if last.ID == "" || last.Label != "Build" || last.Target != "build" || last.Session != "work" {
					t.Errorf("adopted = %+v", last)
				}
			}
		})
	}
}

func TestSaveAdmitsWholeReconnectAndRejectsFailedWrite(t *testing.T) {
	d := newTestDaemon(t)
	original, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "ep-1"})
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "calls")
	t.Setenv("MM_TEST_TRACE", trace)
	endpoint := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(endpoint, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MM_TEST_ENDPOINT", endpoint)
	d.cli = machines.CLI{Bin: fakeHerdr(t, `case "$2" in
 list) if [ -e "$MM_TEST_ENDPOINT" ]; then echo '[{"id":"ep-1","label":"Deploy","target":"deploy"}]'; else echo '[]'; fi;;
 remove) if [ ! -e "$MM_TEST_ENDPOINT" ]; then echo 'endpoint absent' >&2; exit 3; fi
         rm "$MM_TEST_ENDPOINT"; echo remove >> "$MM_TEST_TRACE";;
 add) echo add >> "$MM_TEST_TRACE";;
 esac`)}
	release := make(chan struct{})
	d.queue.Submit(original.ID, jobs.Spec{Kind: jobs.KindDisconnect, Run: func(ctx context.Context, _ jobs.Sink) error {
		<-release
		if err := d.cli.Remove(ctx, original.ProfileID); err != nil {
			return err
		}
		d.refresh(ctx)
		return nil
	}})
	for range 12 {
		d.queue.Submit(original.ID, jobs.Spec{Run: func(context.Context, jobs.Sink) error { return nil }})
	}
	saved, err := d.save(t.Context(), ipc.SaveParams{ID: original.ID, Label: "Deploy", Target: "new-target"})
	close(release)
	if err != nil || len(saved.Jobs) != 1 {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	waitForJobState(t, d.queue, saved.Jobs[0], jobs.StateSucceeded)
	calls, err := os.ReadFile(trace)
	if err != nil || string(calls) != "remove\nadd\n" {
		t.Fatalf("reconnect commands = %q, %v", calls, err)
	}
	before := d.store.List()
	path := filepath.Join(d.env.StateDir, "connections.json")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	jobsBefore := d.queue.List()
	if _, err := d.save(t.Context(), ipc.SaveParams{ID: original.ID, Label: "Changed", Target: "other"}); err == nil {
		t.Fatal("save accepted a failed write")
	}
	if !slices.Equal(before, d.store.List()) || len(d.queue.List()) != len(jobsBefore) {
		t.Fatal("failed save partially applied")
	}
	reopened, err := store.Open(path + ".saved")
	if err != nil || !slices.Equal(before, reopened.List()) {
		t.Fatalf("failed save changed persisted records: %v", err)
	}
}

func TestConcurrentRefreshAdoptsOnce(t *testing.T) {
	d := newTestDaemon(t)
	d.cli = machines.CLI{Bin: fakeHerdr(t, `echo '[{"id":"ep-1","label":"Build","target":"build"}]'`)}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { d.refresh(t.Context()) })
	}
	wg.Wait()
	if len(d.store.List()) != 1 || len(d.snapshot().Connections) != 1 {
		t.Fatal("concurrent refresh duplicated the machine")
	}
}
