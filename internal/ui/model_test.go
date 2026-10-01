package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

func TestConnLookDerivesOneStatePerConnection(t *testing.T) {
	m := sampleModel()
	for id, want := range map[string]string{
		"c1": "connected",
		"c2": "disconnected",
		"c3": "connecting…",
		"c4": "connect queued",
		"c5": "needs answer",
		"c6": "connect failed",
		"c8": "disconnecting…",
	} {
		c, _ := m.connByID(id)
		if got, _ := m.connLook(c); got.word != want {
			t.Errorf("%s: state = %q, want %q", id, got.word, want)
		}
	}
	c, _ := m.connByID("c6")
	if _, err := m.connLook(c); err == "" {
		t.Error("a failed connection did not carry its error")
	}
}

func TestConnLookPrefersTheJobInFlightOverAnOlderFailure(t *testing.T) {
	m := send(newModel(context.Background(), nil), listMsg{Jobs: []jobs.Job{
		{ID: "j1", ConnID: "c1", Kind: jobs.KindConnect, State: jobs.StateFailed, Err: "boom"},
		{ID: "j2", ConnID: "c1", Kind: jobs.KindDisconnect, State: jobs.StateRunning},
		{ID: "j3", ConnID: "c1", Kind: jobs.KindConnect, State: jobs.StateQueued},
	}})
	l, err := m.connLook(conn("c1", "One", "one", true))
	if l.word != "disconnecting…" || err != "" {
		t.Errorf("state = %q, error = %q; want the running job, not the one queued after it or the failure", l.word, err)
	}
}

func TestMergeJobReplacesAndReportsTheOutcome(t *testing.T) {
	m := model{outputs: map[string][]string{}}
	m.mergeJob(jobs.Job{ID: "j1", ConnID: "c1", State: jobs.StateRunning})
	m.mergeJob(jobs.Job{ID: "j1", ConnID: "c1", State: jobs.StateSucceeded, Title: "connect one"})
	if len(m.jobs) != 1 || m.status != "connect one finished" {
		t.Errorf("jobs = %d, status = %q; want the job replaced and its outcome reported", len(m.jobs), m.status)
	}
	if _, busy := m.pendingJob("c1"); busy {
		t.Error("a finished job still counts as pending")
	}
}

func TestListTailsReplaceBufferedOutputAndDroppedJobsAreForgotten(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.outputs = map[string][]string{"j1": {"stale"}, "gone": {"x"}}
	m = send(m, listMsg{Jobs: []jobs.Job{{ID: "j1", State: jobs.StateRunning, Tail: []string{"one", "two"}}}})
	if got := m.outputs["j1"]; len(got) != 2 || got[0] != "one" {
		t.Errorf("outputs = %q, want the daemon's tail", got)
	}
	if _, ok := m.outputs["gone"]; ok {
		t.Error("output of a job the daemon dropped was kept")
	}
}

func TestReplyNeverUndoesWhatArrivedAfterItWasRequested(t *testing.T) {
	queued := jobs.Job{ID: "j1", ConnID: "c1", Kind: jobs.KindConnect, State: jobs.StateQueued, Tail: []string{"old"}}
	asking := waitingJob("j1", "c1", "Enter code:")
	conns := []daemon.Connection{conn("c1", "One", "one", false)}
	m := send(newModel(context.Background(), nil), listMsg{Connections: conns, Revision: 3, Jobs: []jobs.Job{queued}})

	// Output that streamed after the request outlasts the reply's tail.
	asked := m.received
	m = send(m, outputMsg{jobID: "j1", lines: []string{"streamed"}})
	m = send(m, replyMsg{daemon.ListResult{Connections: conns, Revision: 3, Jobs: []jobs.Job{queued}}, asked})
	if out := m.outputs["j1"]; out[len(out)-1] != "streamed" {
		t.Errorf("the reply's tail replaced output that streamed after it was requested: %q", out)
	}

	// A list request goes out; a question and output arrive on the
	// subscription; then the reply, with a snapshot taken before them.
	asked = m.received
	m = send(m, jobMsg(asking), outputMsg{jobID: "j1", lines: []string{"new"}})
	m = send(m, replyMsg{daemon.ListResult{Connections: conns, Revision: 3, Jobs: []jobs.Job{queued}}, asked})
	if m.dialog == nil || m.jobs[0].State != jobs.StateAwaitingInput || m.outputs["j1"][len(m.outputs["j1"])-1] != "new" {
		t.Errorf("an older reply replaced newer state: dialog %v, jobs %+v, output %q", m.dialog != nil, m.jobs, m.outputs["j1"])
	}

	// A reply requested before a list the subscription delivered is dropped.
	m = send(m, listMsg{Connections: conns, Revision: 4, Jobs: []jobs.Job{asking}})
	m = send(m, replyMsg{daemon.ListResult{Revision: 4}, asked})
	if len(m.conns) != 1 || len(m.jobs) != 1 {
		t.Errorf("a reply older than the last list was applied: %d connections, %d jobs", len(m.conns), len(m.jobs))
	}

	// Of two replies, one with a lower revision than the list applied is dropped.
	asked = m.received
	m = send(m, replyMsg{daemon.ListResult{Connections: append(conns, conn("c2", "Two", "two", false)), Revision: 6, Jobs: []jobs.Job{asking}}, asked})
	m = send(m, replyMsg{daemon.ListResult{Connections: conns, Revision: 5, Jobs: []jobs.Job{asking}}, asked})
	if len(m.conns) != 2 {
		t.Errorf("a list with an older revision replaced a newer one: %d connections", len(m.conns))
	}

	// A new subscription may reach a restarted daemon with lower revisions.
	m = send(m, reconnectMsg{})
	m = send(m, replyMsg{daemon.ListResult{Connections: conns, Revision: 1}, m.received})
	if len(m.conns) != 1 || len(m.jobs) != 0 {
		t.Errorf("the first list after reconnecting was not applied: %d connections, %d jobs", len(m.conns), len(m.jobs))
	}
}

func TestStreamedOutputIsBounded(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.jobs = []jobs.Job{{ID: "j1", State: jobs.StateRunning}}
	for range outputLimit + 5 {
		m, _ = promptUpdate(t, m, outputMsg{jobID: "j1", lines: []string{"line"}})
	}
	if got := len(m.outputs["j1"]); got != outputLimit {
		t.Errorf("buffered %d lines, want %d", got, outputLimit)
	}
}

func TestSelectionFollowsTheConnectionAcrossListUpdates(t *testing.T) {
	m := newModel(context.Background(), nil)
	a, b, c := conn("a", "A", "a", false), conn("b", "B", "b", false), conn("c", "C", "c", false)
	m, _ = promptUpdate(t, m, listMsg{Connections: []daemon.Connection{a, b}})
	m.cursor = 1
	m, _ = promptUpdate(t, m, listMsg{Connections: []daemon.Connection{c, a, b}})
	if m.conns[m.cursor].ID != "b" {
		t.Errorf("selection = %q after a connection was inserted above it, want b", m.conns[m.cursor].ID)
	}
	m, _ = promptUpdate(t, m, listMsg{Connections: []daemon.Connection{a}})
	if m.conns[m.cursor].ID != "a" {
		t.Errorf("selection = %q after the selected connection was removed, want a valid row", m.conns[m.cursor].ID)
	}
}

func TestReconnectNoticeClearsOnceTheListIsRead(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.config = "config.toml: bad value"
	m, _ = promptUpdate(t, m, failureMsg("job not queued"))
	m, _ = promptUpdate(t, m, linkMsg("lost the daemon, reconnecting…"))
	m, cmd := promptUpdate(t, m, reconnectMsg{})
	if cmd == nil {
		t.Fatal("a reopened stream did not read the list again")
	}
	m, _ = promptUpdate(t, m, listMsg{})
	if m.link != "" {
		t.Errorf("link = %q after the list was read", m.link)
	}
	if m.failure == "" || m.config == "" {
		t.Errorf("failure = %q, config = %q; a list read must not hide a rejected request or the config error", m.failure, m.config)
	}
	m, _ = promptUpdate(t, m, statusMsg("connect queued"))
	if m.failure != "" || m.config == "" || !strings.Contains(m.View(), "config.toml: bad value") {
		t.Errorf("after an accepted request: failure = %q, config = %q", m.failure, m.config)
	}
}

func TestDetailFollowsTheJobInFlightNotTheOneQueuedBehindIt(t *testing.T) {
	m := sized(newModel(context.Background(), nil), 100, 26)
	asking := waitingJob("j1", "c1", "Enter the one-time code:")
	asking.Tail = []string{"LIVE OUTPUT LINE"}
	m, _ = promptUpdate(t, m, listMsg{
		Connections: []daemon.Connection{conn("c1", "One", "one", true)},
		Jobs:        []jobs.Job{asking, {ID: "j2", ConnID: "c1", Kind: jobs.KindForget, State: jobs.StateQueued}},
	})
	m, _ = press(t, m, "esc")
	view := m.View()
	if !strings.Contains(view, "one-time code") || !strings.Contains(view, "LIVE OUTPUT LINE") {
		t.Errorf("the panel lost the waiting job's question or output:\n%s", view)
	}
}

func TestFilteredMatchesNameAndHost(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.aliases = []sshconfig.Alias{{Name: "deploy", Host: "203.0.113.10"}, {Name: "build", Host: "10.0.0.8"}}
	for filter, want := range map[string]int{"dep": 1, "10.0": 1, "": 2, "zzz": 0} {
		m.filter.SetValue(filter)
		if got := len(m.filtered()); got != want {
			t.Errorf("filter %q matched %d aliases, want %d", filter, got, want)
		}
	}
}

func TestEndpointFormatting(t *testing.T) {
	for _, tt := range []struct{ user, host, port, want string }{
		{host: "10.0.0.1", want: "10.0.0.1"},
		{user: "root", host: "10.0.0.1", port: "22", want: "root@10.0.0.1"},
		{user: "root", host: "10.0.0.1", port: "2222", want: "root@10.0.0.1:2222"},
		{user: "root", want: ""},
	} {
		if got := endpoint(tt.user, tt.host, tt.port); got != tt.want {
			t.Errorf("endpoint(%q, %q, %q) = %q, want %q", tt.user, tt.host, tt.port, got, tt.want)
		}
	}
}
