package daemon

import (
	"encoding/json"
	"strings"
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
