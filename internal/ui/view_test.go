package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
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
	for _, height := range []int{1, 4, 8, 24} {
		for _, count := range []int{0, 3, 20} {
			for _, message := range []string{"", "working", "failed"} {
				m := model{width: 40, height: height}
				if message == "failed" {
					m.failure = message
				} else {
					m.status = message
				}
				body := make([]string, count)
				for i := range body {
					body[i] = "row"
				}
				got := strings.Split(m.page("Title", "summary", body, "help"), "\n")
				if len(got) != height {
					t.Fatalf("height=%d body=%d message=%q: rendered %d lines", height, count, message, len(got))
				}
				if !strings.Contains(got[height-1], "help") {
					t.Errorf("last row = %q, want key hints", got[height-1])
				}
				if height >= 4 && message != "" && !strings.Contains(got[height-3], message) {
					t.Errorf("status row = %q, want %q above the footer rule", got[height-3], message)
				}
				if height == 24 && count > 0 && got[2] != "row" {
					t.Errorf("first body row = %q, want content to stay at the top", got[2])
				}
			}
		}
	}
}

func TestPageWithoutHeightIsNotCut(t *testing.T) {
	m := model{width: 40}
	body := []string{"a", "b", "c"}
	if got := strings.Split(m.page("Title", "", body, "help"), "\n"); len(got) != 7 {
		t.Errorf("lines = %d, want header, rule, 3 rows, rule and help", len(got))
	}
}

func TestKeyHintsStayOnOneLine(t *testing.T) {
	for _, width := range []int{24, 40, 72, 100} {
		m := model{width: width, height: 24}
		view := m.viewList()
		lines := strings.Split(view, "\n")
		if len(lines) != m.height || !strings.Contains(lines[len(lines)-1], "Esc close") {
			t.Fatalf("width %d: footer is not at the bottom: %q", width, view)
		}
		footer := lines[len(lines)-1]
		if lipgloss.Width(footer) > width || !strings.Contains(footer, "Ctrl+A add") {
			t.Errorf("width %d: primary action missing or footer overflows: %q", width, footer)
		}
		if width >= 40 && !strings.Contains(footer, "Ctrl+E edit") {
			t.Errorf("width %d: edit hint missing: %q", width, footer)
		}
		if !strings.Contains(lines[len(lines)-2], strings.Repeat("─", width)) {
			t.Errorf("width %d: hints occupy more than one row: %q", width, view)
		}
	}
}

func TestDetailsShowConfigurationWithoutAnyJob(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen, m.width, m.height = screenOutput, 72, 16
	c := conn("c1", "Build machine", "builder", true)
	c.Session = "work"
	m.conns = []daemon.Connection{c}
	m.aliases = []sshconfig.Alias{{Name: "builder", User: "dev", Host: "build.example", Port: "2222"}}
	view := m.View()
	for _, want := range []string{"Label", "Build machine", "SSH target", "builder", "Session", "work", "State", "connected", "Address", "dev@build.example:2222", "Ctrl+E edit"} {
		if !strings.Contains(view, want) {
			t.Errorf("details missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "nothing has run") {
		t.Error("details should show settings without requiring a previous job")
	}
	m.conns[0].Session = ""
	m.conns[0].Active = false
	view = m.View()
	if !strings.Contains(view, "(default)") || !strings.Contains(view, "disconnected") {
		t.Errorf("unset session or disconnected state is not explicit: %q", view)
	}
}

func TestDetailsKeepConfigurationAlongsideTaskOutput(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen, m.width, m.height = screenOutput, 72, 16
	m.conns = []daemon.Connection{conn("c1", "Build machine", "builder", false)}
	m.allJobs = []jobs.Job{{ID: "j1", ConnID: "c1", State: jobs.StateFailed, Err: "connection refused"}}
	m.outputs["j1"] = []string{"resolving host", "could not connect"}
	view := m.View()
	for _, want := range []string{"SSH target", "builder", "connection refused", "could not connect", "Ctrl+E edit"} {
		if !strings.Contains(view, want) {
			t.Errorf("details missing %q: %q", want, view)
		}
	}
}

func TestSelectedConnectionStaysVisibleAfterResizeAndStatus(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.width, m.height = 100, 24
	for i := range 30 {
		m.conns = append(m.conns, conn(fmt.Sprintf("c%d", i), fmt.Sprintf("Machine %02d", i), "host", false))
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updated.(model)
	m.status = "refreshed"
	view := m.View()
	if !strings.Contains(view, "❯") || !strings.Contains(view, "Machine 29") {
		t.Errorf("selected connection was hidden after resizing: %q", view)
	}
	if len(strings.Split(view, "\n")) != m.height {
		t.Errorf("frame does not fit the resized pane: %q", view)
	}
}

func TestWaitingPromptInvitationSurvivesNarrowPaneAndLongOutput(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen, m.width, m.height = screenOutput, 24, 12
	m.conns = []daemon.Connection{conn("c1", "One", "host", false)}
	m.allJobs = []jobs.Job{{ID: "j1", ConnID: "c1", State: jobs.StateAwaitingInput, Prompt: "Continue?"}}
	m.outputs["j1"] = make([]string, 100)
	m.status = "waiting"
	view := m.View()
	if !strings.Contains(view, "Continue?") || !strings.Contains(view, "Press Enter to answer") || !strings.Contains(view, "Enter answer") || !strings.Contains(view, "Esc back") {
		t.Errorf("question or invitation hidden by output: %q", view)
	}
	if strings.Contains(view, "›") {
		t.Errorf("details still expose an inline answer input: %q", view)
	}
	if len(strings.Split(view, "\n")) != m.height {
		t.Errorf("frame does not fit: %q", view)
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
