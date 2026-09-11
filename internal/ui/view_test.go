package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

func TestConnStateShowsTheJobInFlight(t *testing.T) {
	c := conn("c1", "Deploy", "deploy", false)
	m := model{active: []jobs.Job{
		{ID: "job-1", ConnID: "c1", Kind: jobs.KindConnect, State: jobs.StateRunning},
	}}
	_, detail := m.connState(c)
	if !strings.Contains(detail, "connecting") {
		t.Errorf("detail = %q, want the job's own state", detail)
	}
}

func TestConnStateAsksForAnAnswer(t *testing.T) {
	c := conn("c1", "Deploy", "deploy", false)
	m := model{active: []jobs.Job{
		{ID: "job-1", ConnID: "c1", Kind: jobs.KindConnect, State: jobs.StateAwaitingInput},
	}}
	_, detail := m.connState(c)
	if !strings.Contains(detail, "needs an answer") {
		t.Errorf("detail = %q, want the row to ask for input", detail)
	}
}

func TestConnStateKeepsTheLastFailure(t *testing.T) {
	c := conn("c1", "Deploy", "deploy", false)
	m := model{allJobs: []jobs.Job{
		{ID: "job-1", ConnID: "c1", State: jobs.StateFailed, Err: "exit status 2"},
	}}
	_, detail := m.connState(c)
	if !strings.Contains(detail, "exit status 2") {
		t.Errorf("detail = %q, want the failure to stay visible", detail)
	}
}

func TestConnStateOfAQuietConnection(t *testing.T) {
	m := model{}
	if _, detail := m.connState(conn("c1", "Deploy", "deploy", true)); detail != "" {
		t.Errorf("detail = %q, want the row to fall back to its target", detail)
	}
}

func TestPageFitsTheWindow(t *testing.T) {
	m := model{width: 40, height: 8, status: "working"}
	body := make([]string, 20)
	for i := range body {
		body[i] = "row"
	}
	got := strings.Split(m.page("Title", "summary", body, "help"), "\n")
	if len(got) > m.height {
		t.Errorf("rendered %d lines into a %d-row pane", len(got), m.height)
	}
	// The chrome survives the cut: the help line is what tells the user what
	// to press.
	if !strings.Contains(got[len(got)-1], "working") || !strings.Contains(got[len(got)-2], "help") {
		t.Errorf("frame lost its footer: %q", got[len(got)-2:])
	}
}

func TestPageWithoutHeightIsNotCut(t *testing.T) {
	m := model{width: 40}
	body := []string{"a", "b", "c"}
	if got := strings.Split(m.page("Title", "", body, "help"), "\n"); len(got) != 7 {
		t.Errorf("lines = %d, want header, rule, 3 rows, rule and help", len(got))
	}
}

func TestTruncateCountsDisplayWidth(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate = %q, want it untouched", got)
	}
	if got := truncate("abcdefghij", 5); lipgloss.Width(got) > 5 {
		t.Errorf("truncate = %q, wider than 5 columns", got)
	}
	// CJK glyphs are two columns wide; cutting by rune count would overflow.
	if got := truncate("机器管理器面板", 6); lipgloss.Width(got) > 6 {
		t.Errorf("truncate = %q is %d columns, want at most 6", got, lipgloss.Width(got))
	}
}

func TestPadFillsToWidth(t *testing.T) {
	if got := pad("ab", 5); lipgloss.Width(got) != 5 {
		t.Errorf("pad = %q, want 5 columns", got)
	}
	if got := pad("机器", 5); lipgloss.Width(got) != 5 {
		t.Errorf("pad = %q is %d columns, want 5", got, lipgloss.Width(got))
	}
}

func TestEndpointFormatting(t *testing.T) {
	tests := []struct {
		user, host, port, want string
	}{
		{host: "10.0.0.1", want: "10.0.0.1"},
		{user: "root", host: "10.0.0.1", want: "root@10.0.0.1"},
		{user: "root", host: "10.0.0.1", port: "22", want: "root@10.0.0.1"},
		{user: "root", host: "10.0.0.1", port: "2222", want: "root@10.0.0.1:2222"},
		{want: ""},
	}
	for _, tt := range tests {
		if got := endpoint(tt.user, tt.host, tt.port); got != tt.want {
			t.Errorf("endpoint(%q, %q, %q) = %q, want %q", tt.user, tt.host, tt.port, got, tt.want)
		}
	}
}

func TestVisibleWindowsTheList(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	if got := visible(items, 1, 2); len(got) != 2 || got[0] != "b" {
		t.Errorf("visible = %q", got)
	}
	if got := visible(items, 3, 5); len(got) != 1 || got[0] != "d" {
		t.Errorf("visible past the end = %q", got)
	}
	if got := visible(items, 9, 2); got != nil {
		t.Errorf("visible beyond the list = %q, want nothing", got)
	}
}
