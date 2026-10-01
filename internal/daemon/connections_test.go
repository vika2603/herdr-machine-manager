package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Daemon{store: s}
}

func find(conns []Connection, label string) (Connection, bool) {
	for _, c := range conns {
		if c.Label == label {
			return c, true
		}
	}
	return Connection{}, false
}

func TestReconcileMarksStoredConnectionActive(t *testing.T) {
	d := newTestDaemon(t)
	saved, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy"})
	if err != nil {
		t.Fatal(err)
	}

	conns := d.reconcile([]machines.Machine{{ID: "abc", Label: "Deploy", Target: "deploy"}})
	got, ok := find(conns, "Deploy")
	if !ok || !got.Active {
		t.Fatalf("connection = %+v, want it active", got)
	}
	if got.ID != saved.ID {
		t.Errorf("id = %q, want the stored connection %q reused", got.ID, saved.ID)
	}
	// The endpoint id is recorded, so a later disconnect knows what to remove.
	if got.ProfileID != "abc" {
		t.Errorf("ProfileID = %q, want abc", got.ProfileID)
	}
	if stored, _ := d.store.Get(saved.ID); stored.ProfileID != "abc" {
		t.Errorf("the endpoint id was not persisted: %+v", stored)
	}
}

func TestReconcileClearsTheEndpointWhenHerdrDroppedIt(t *testing.T) {
	d := newTestDaemon(t)
	saved, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "abc"})

	conns := d.reconcile(nil)
	got, _ := find(conns, "Deploy")
	if got.Active {
		t.Error("connection reported active while herdr holds nothing")
	}
	if got.ProfileID != "" {
		t.Errorf("ProfileID = %q, want it cleared", got.ProfileID)
	}
	if stored, _ := d.store.Get(saved.ID); stored.ProfileID != "" {
		t.Errorf("the stale endpoint id survived in the store: %+v", stored)
	}
}

func TestReconcileAdoptsMachinesAddedOutsideThePlugin(t *testing.T) {
	d := newTestDaemon(t)

	conns := d.reconcile([]machines.Machine{{ID: "abc", Label: "Build", Target: "build", Session: "work"}})
	got, ok := find(conns, "Build")
	if !ok {
		t.Fatalf("connections = %+v, want the machine adopted", conns)
	}
	if !got.Active || got.ProfileID != "abc" || got.Target != "build" || got.Session != "work" {
		t.Errorf("adopted = %+v, want it active with the machine's own fields", got)
	}
	if len(d.store.List()) != 1 {
		t.Errorf("store = %+v, want the adopted connection persisted", d.store.List())
	}
}

func TestReconcileNeverDropsAStoredConnection(t *testing.T) {
	d := newTestDaemon(t)
	_, _ = d.store.Put(store.Connection{Label: "Offline", Target: "offline"})
	_, _ = d.store.Put(store.Connection{Label: "Deploy", Target: "deploy"})

	conns := d.reconcile([]machines.Machine{{ID: "abc", Label: "Deploy", Target: "deploy"}})
	if len(conns) != 2 || len(d.store.List()) != 2 {
		t.Fatalf("reconcile returned %d connections and stored %d, want both kept", len(conns), len(d.store.List()))
	}
	if offline, _ := find(conns, "Offline"); offline.Active {
		t.Error("a connection herdr does not hold was reported active")
	}
}

func TestReconcilePrefersTheRecordedEndpointID(t *testing.T) {
	d := newTestDaemon(t)
	// Two connections that differ only by endpoint id, which is what an
	// adopted duplicate of the same target looks like.
	first, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "id-1"})
	second, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "id-2"})

	conns := d.reconcile([]machines.Machine{
		{ID: "id-2", Label: "Deploy", Target: "deploy"},
		{ID: "id-1", Label: "Deploy", Target: "deploy"},
	})
	if len(conns) != 2 {
		t.Fatalf("connections = %+v, want exactly the two stored", conns)
	}
	for _, conn := range conns {
		switch conn.ID {
		case first.ID:
			if conn.ProfileID != "id-1" {
				t.Errorf("first connection took %q, want id-1", conn.ProfileID)
			}
		case second.ID:
			if conn.ProfileID != "id-2" {
				t.Errorf("second connection took %q, want id-2", conn.ProfileID)
			}
		}
	}
}

func TestReconcileReservesExistingIDBeforeMatchingDuplicate(t *testing.T) {
	d := newTestDaemon(t)
	removed, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "id-1"})
	remaining, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "id-2"})

	conns := d.reconcile([]machines.Machine{{ID: "id-2", Label: "Deploy", Target: "deploy"}})
	if len(conns) != 2 {
		t.Fatalf("connections = %+v, want two", conns)
	}
	for _, conn := range conns {
		switch conn.ID {
		case removed.ID:
			if conn.Active || conn.ProfileID != "" {
				t.Errorf("removed connection claimed the remaining endpoint: %+v", conn)
			}
		case remaining.ID:
			if !conn.Active || conn.ProfileID != "id-2" {
				t.Errorf("remaining connection lost its endpoint: %+v", conn)
			}
		}
	}
}

func TestReconcileMatchesANewConnectionByTargetAndLabel(t *testing.T) {
	d := newTestDaemon(t)
	// A connect job has just run: the store has no endpoint id yet, because
	// `herdr machine add` does not report one.
	saved, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy"})

	conns := d.reconcile([]machines.Machine{
		{ID: "other", Label: "Build", Target: "build"},
		{ID: "fresh", Label: "Deploy", Target: "deploy"},
	})
	got, _ := find(conns, "Deploy")
	if got.ID != saved.ID {
		t.Errorf("the fresh machine was adopted as a new connection instead of matching the stored one")
	}
	if got.ProfileID != "fresh" {
		t.Errorf("ProfileID = %q, want fresh", got.ProfileID)
	}
	if len(d.store.List()) != 2 {
		t.Errorf("store holds %d connections, want the matched one plus the adopted Build", len(d.store.List()))
	}
}

func TestConnectionLooksUpTheMergedView(t *testing.T) {
	d := newTestDaemon(t)
	saved, _ := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy"})
	d.conns = d.reconcile([]machines.Machine{{ID: "abc", Label: "Deploy", Target: "deploy"}})

	got, ok := d.connection(saved.ID)
	if !ok || !got.Active {
		t.Errorf("connection(%q) = %+v, %v; want the active merged view", saved.ID, got, ok)
	}
	if _, ok := d.connection("missing"); ok {
		t.Error("connection reported an unknown id as found")
	}
}

// fakeHerdr writes a stand-in for the herdr binary that reports one machine.
func fakeHerdr(t *testing.T, listJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	body := "#!/bin/sh\ncase \"$2\" in\n  list) cat <<'JSON'\n" + listJSON + "\nJSON\n  ;;\nesac\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSaveReportsQueueFailureWithoutPartialReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "connections.json")
	connections, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{store: connections}
	original, err := d.store.Put(store.Connection{Label: "Deploy", Target: "deploy", ProfileID: "ep-1"})
	if err != nil {
		t.Fatal(err)
	}
	d.cli = machines.CLI{Bin: fakeHerdr(t, `[{"id":"ep-1","label":"Deploy","target":"deploy"}]`)}
	d.queue = jobs.NewQueue(ctx, jobs.Hooks{})
	d.conns = []Connection{{Connection: original, Active: true}}
	release := make(chan struct{})
	started := make(chan struct{})
	_, err = d.queue.Submit(jobs.Spec{ConnID: original.ID, Kind: jobs.KindConnect,
		Run: func(context.Context, jobs.Sink) error { close(started); <-release; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer close(release)
	<-started
	for range 7 { // One free lane slot is insufficient for both reconnect jobs.
		if _, err := d.queue.Submit(jobs.Spec{ConnID: original.ID, Kind: jobs.KindRename,
			Run: func(context.Context, jobs.Sink) error { return nil }}); err != nil {
			t.Fatal(err)
		}
	}
	before := len(d.queue.List())
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.save(ctx, ipc.SaveParams{ID: original.ID, Label: "Deploy", Target: "new-target"})
	if err == nil || !strings.Contains(err.Error(), "job not queued") {
		t.Fatalf("save error = %v, want a visible queue failure", err)
	}
	if got := len(d.queue.List()); got != before {
		t.Errorf("save queued only part of reconnect: %d jobs, want %d", got, before)
	}
	if saved, _ := d.store.Get(original.ID); saved != original {
		t.Errorf("failed save changed the record: got %+v, want %+v", saved, original)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, persisted) {
		t.Errorf("failed save changed connections.json:\n%s", after)
	}
}

func TestConcurrentRefreshAdoptsAMachineOnce(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{
		store: s,
		cli:   machines.CLI{Bin: fakeHerdr(t, `[{"id":"ep-1","label":"Build","target":"build"}]`)},
		queue: jobs.NewQueue(ctx, jobs.Hooks{}),
	}

	// The job hook, the poller and an explicit refresh can all fire at once.
	// Each one that decided independently that the machine is unclaimed would
	// adopt it again, and the user would see duplicate rows.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.refresh(ctx)
		}()
	}
	wg.Wait()

	if got := len(s.List()); got != 1 {
		t.Fatalf("store holds %d connections, want 1: %+v", got, s.List())
	}
	if got := len(d.snapshot().Connections); got != 1 {
		t.Errorf("snapshot has %d connections, want 1", got)
	}
}
