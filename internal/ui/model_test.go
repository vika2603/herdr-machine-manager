package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

func conn(id, label, target string, active bool) daemon.Connection {
	return daemon.Connection{
		Connection: store.Connection{ID: id, Label: label, Target: target},
		Active:     active,
	}
}

func TestActiveJobsKeepsOnlyTheUnfinished(t *testing.T) {
	got := activeJobs([]jobs.Job{
		{ID: "1", State: jobs.StateSucceeded},
		{ID: "2", State: jobs.StateRunning},
		{ID: "3", State: jobs.StateAwaitingInput},
		{ID: "4", State: jobs.StateFailed},
	})
	if len(got) != 2 || got[0].ID != "2" || got[1].ID != "3" {
		t.Errorf("activeJobs = %+v, want the running and waiting jobs", got)
	}
}

func TestMergeJobReplacesAndDerivesActive(t *testing.T) {
	m := model{outputs: map[string][]string{}}
	m.mergeJob(jobs.Job{ID: "job-1", ConnID: "c1", State: jobs.StateRunning})
	m.mergeJob(jobs.Job{ID: "job-2", ConnID: "c2", State: jobs.StateRunning})
	if len(m.active) != 2 {
		t.Fatalf("active = %d, want 2", len(m.active))
	}

	m.mergeJob(jobs.Job{ID: "job-1", ConnID: "c1", State: jobs.StateSucceeded, Title: "connect one"})
	if len(m.allJobs) != 2 {
		t.Errorf("allJobs = %d, want the job replaced rather than appended", len(m.allJobs))
	}
	if len(m.active) != 1 || m.active[0].ID != "job-2" {
		t.Errorf("active = %+v, want only job-2", m.active)
	}
	if m.status != "connect one finished" {
		t.Errorf("status = %q, want the finished job reported", m.status)
	}
}

func TestMergeJobReportsAFailure(t *testing.T) {
	m := model{outputs: map[string][]string{}}
	m.mergeJob(jobs.Job{ID: "job-1", ConnID: "c1", State: jobs.StateFailed, Title: "connect", Err: "exit status 2"})
	if !strings.Contains(m.failure, "exit status 2") {
		t.Errorf("failure = %q, want the error carried through", m.failure)
	}
}

func TestMergeJobMasksASecretPrompt(t *testing.T) {
	m := model{outputs: map[string][]string{}}
	m.mergeJob(jobs.Job{ID: "job-1", ConnID: "c1", State: jobs.StateAwaitingInput, Prompt: "alice@host password:"})
	if m.dialog == nil || m.dialog.input.EchoMode != textinput.EchoPassword {
		t.Error("a password prompt must not echo what is typed")
	}
	m.mergeJob(jobs.Job{ID: "job-1", ConnID: "c1", State: jobs.StateAwaitingInput, Prompt: "continue? [y/N]"})
	if m.dialog == nil || m.dialog.input.EchoMode != textinput.EchoNormal {
		t.Error("an ordinary prompt should echo")
	}
}

func TestMergeJobsSeedsOutputsFromTheDaemon(t *testing.T) {
	m := model{outputs: map[string][]string{"job-1": {"stale"}}}
	m.mergeJobs([]jobs.Job{{ID: "job-1", ConnID: "c1", State: jobs.StateRunning, Tail: []string{"one", "two"}}})

	// After a reconnect the daemon's copy is the complete one.
	if got := m.outputs["job-1"]; len(got) != 2 || got[0] != "one" {
		t.Errorf("outputs = %q, want the daemon's tail", got)
	}
}

func TestForgetOutputsDropsUnknownJobs(t *testing.T) {
	m := model{outputs: map[string][]string{"job-1": {"a"}, "gone": {"b"}}}
	m.allJobs = []jobs.Job{{ID: "job-1"}}
	m.forgetOutputs()
	if _, ok := m.outputs["gone"]; ok {
		t.Error("output of a job the daemon dropped was kept")
	}
	if _, ok := m.outputs["job-1"]; !ok {
		t.Error("output of a live job was dropped")
	}
}

func TestJobForMatchesTheConnection(t *testing.T) {
	m := model{active: []jobs.Job{{ID: "job-1", ConnID: "c1"}, {ID: "job-2", ConnID: "c2"}}}
	if job, ok := m.jobFor("c2"); !ok || job.ID != "job-2" {
		t.Errorf("jobFor(c2) = %+v, %v", job, ok)
	}
	if _, ok := m.jobFor("c3"); ok {
		t.Error("jobFor reported a job for a connection that has none")
	}
}

func TestLastJobForReturnsTheMostRecent(t *testing.T) {
	m := model{allJobs: []jobs.Job{
		{ID: "job-1", ConnID: "c1", Title: "old"},
		{ID: "job-2", ConnID: "c2"},
		{ID: "job-3", ConnID: "c1", Title: "new"},
	}}
	job, ok := m.lastJobFor("c1")
	if !ok || job.Title != "new" {
		t.Errorf("lastJobFor(c1) = %+v, want the newest", job)
	}
}

func TestFilteredMatchesNameAndHost(t *testing.T) {
	m := model{aliases: []sshconfig.Alias{
		{Name: "deploy", Host: "203.0.113.10"},
		{Name: "build", Host: "10.0.0.8"},
	}, aliasFilter: textinput.New()}

	m.aliasFilter.SetValue("dep")
	if got := m.filtered(); len(got) != 1 || got[0].Name != "deploy" {
		t.Errorf("filter by name = %+v", got)
	}
	m.aliasFilter.SetValue("10.0")
	if got := m.filtered(); len(got) != 1 || got[0].Name != "build" {
		t.Errorf("filter by host = %+v", got)
	}
	m.aliasFilter.SetValue("")
	if got := m.filtered(); len(got) != 2 {
		t.Errorf("empty filter = %+v, want everything", got)
	}
}

func TestScrollKeepsTheCursorVisible(t *testing.T) {
	tests := []struct {
		offset, cursor, rows, want int
	}{
		{offset: 0, cursor: 0, rows: 5, want: 0},
		{offset: 0, cursor: 4, rows: 5, want: 0},
		{offset: 0, cursor: 5, rows: 5, want: 1},
		{offset: 3, cursor: 2, rows: 5, want: 2},
		{offset: 3, cursor: 9, rows: 5, want: 5},
	}
	for _, tt := range tests {
		if got := scroll(tt.offset, tt.cursor, tt.rows); got != tt.want {
			t.Errorf("scroll(%d, %d, %d) = %d, want %d", tt.offset, tt.cursor, tt.rows, got, tt.want)
		}
	}
}

func TestCurrentFollowsTheCursor(t *testing.T) {
	m := model{conns: []daemon.Connection{conn("c1", "One", "one", true), conn("c2", "Two", "two", false)}, cursor: 1}
	if got, ok := m.current(); !ok || got.ID != "c2" {
		t.Errorf("current = %+v, %v", got, ok)
	}
	m.cursor = 5
	if _, ok := m.current(); ok {
		t.Error("current returned a connection for an out-of-range cursor")
	}
}
