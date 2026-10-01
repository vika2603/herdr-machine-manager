package ui

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

// sampleModel holds a connection in every state the list can show.
func sampleModel() model {
	m := newModel(context.Background(), nil)
	m.installDefault = true
	m.aliases = []sshconfig.Alias{
		{Name: "prod", User: "deploy", Host: "10.0.4.12"},
		{Name: "staging", User: "deploy", Host: "staging.internal"},
		{Name: "builder", User: "ci", Host: "build.example.com", Port: "2222"},
		{Name: "replica", Host: "db2.internal"},
		{Name: "tokyo", User: "ops", Host: "edge-tyo.example.net"},
		{Name: "gpu", User: "vika", Host: "gpu.lab.internal"},
		{Name: "bastion", User: "admin", Host: "203.0.113.7"},
		{Name: "archive", Host: "archive.internal"},
		{Name: "sandbox", User: "dev", Host: "sandbox.local"},
	}
	prod := conn("c1", "prod-api", "prod", true)
	prod.Session = "main"
	conns := []daemon.Connection{
		prod,
		conn("c2", "staging", "staging", false),
		conn("c3", "build-runner-with-a-really-long-label", "builder", false),
		conn("c4", "db-replica", "replica", false),
		conn("c5", "edge-tokyo", "tokyo", false),
		conn("c6", "lab-gpu", "gpu", false),
		conn("c7", "bastion", "bastion", false),
		conn("c8", "archive", "archive", true),
	}
	list := []jobs.Job{
		{ID: "j1", ConnID: "c1", Kind: jobs.KindConnect, Title: "connect prod-api", State: jobs.StateSucceeded,
			Tail: []string{"checking remote herdr on prod", "remote herdr 0.9.2 is compatible", "saved machine prod-api"}},
		{ID: "j3", ConnID: "c3", Kind: jobs.KindConnect, Title: "connect build-runner-with-a-really-long-label", State: jobs.StateRunning,
			Tail: []string{"resolving builder", "uploading herdr 0.9.2 (linux-amd64)", "  12.4 MiB / 18.0 MiB"}},
		{ID: "j4", ConnID: "c4", Kind: jobs.KindConnect, Title: "connect db-replica", State: jobs.StateQueued},
		{ID: "j5", ConnID: "c5", Kind: jobs.KindConnect, Title: "connect edge-tokyo", State: jobs.StateAwaitingInput,
			Prompt: "remote herdr is missing on edge-tyo.example.net. continue installing the remote herdr binary? [y/N]",
			Tail:   []string{"checking remote herdr on tokyo", "herdr not found in PATH"}},
		{ID: "j6", ConnID: "c6", Kind: jobs.KindConnect, Title: "connect lab-gpu", State: jobs.StateFailed,
			Err:  "ssh: connect to host gpu.lab.internal port 22: Operation timed out; herdr machine add exited with status 255",
			Tail: []string{"resolving gpu", "ssh: connect to host gpu.lab.internal port 22: Operation timed out"}},
		{ID: "j7", ConnID: "c7", Kind: jobs.KindConnect, Title: "connect bastion", State: jobs.StateAwaitingInput, Prompt: "admin@203.0.113.7's password: "},
		{ID: "j8", ConnID: "c8", Kind: jobs.KindDisconnect, Title: "disconnect archive", State: jobs.StateRunning},
	}
	next, _ := m.Update(listMsg{Connections: conns, Jobs: list})
	m = next.(model)
	// Both questions are set aside so that each screen shows what it is
	// named after; the prompt screens reopen one.
	m.dismissPrompt()
	m.dismissPrompt()
	return m
}

func send(m model, msgs ...tea.Msg) model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func keys(s ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range s {
		switch k {
		case "enter":
			out = append(out, tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			out = append(out, tea.KeyMsg{Type: tea.KeyEsc})
		case "down":
			out = append(out, tea.KeyMsg{Type: tea.KeyDown})
		case "right":
			out = append(out, tea.KeyMsg{Type: tea.KeyRight})
		case "tab":
			out = append(out, tea.KeyMsg{Type: tea.KeyTab})
		case "space":
			out = append(out, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		case "ctrl+o":
			out = append(out, tea.KeyMsg{Type: tea.KeyCtrlO})
		default:
			out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		}
	}
	return out
}

func sized(m model, w, h int) model {
	return send(m, tea.WindowSizeMsg{Width: w, Height: h})
}

type sampleScreen struct {
	name  string
	sizes [][2]int
	build func(w, h int) tea.Model
}

var managerSizes = [][2]int{{100, 26}, {60, 14}}

func sampleScreens() []sampleScreen {
	manager := func(name string, setup func(m model) model, sizes ...[2]int) sampleScreen {
		if sizes == nil {
			sizes = managerSizes
		}
		return sampleScreen{name, sizes, func(w, h int) tea.Model { return setup(sized(sampleModel(), w, h)) }}
	}
	pane := func(name string, sizes [][2]int, list tea.Msg, input ...tea.Msg) sampleScreen {
		return sampleScreen{name, sizes, func(w, h int) tea.Model {
			var p tea.Model = promptModel{model: sized(newModel(context.Background(), nil), w, h)}
			if list != nil {
				for _, msg := range append([]tea.Msg{list}, input...) {
					p, _ = p.Update(msg)
				}
			}
			return p
		}}
	}
	s := sampleModel()
	two := listMsg{Connections: s.conns, Jobs: s.jobs}
	secretOnly := listMsg{Connections: s.conns, Jobs: []jobs.Job{s.jobs[5]}}
	promptSizes := [][2]int{{64, 13}, {40, 10}}
	return []sampleScreen{
		manager("list, connected connection with a finished job selected", func(m model) model { return m }),
		manager("list, running connection selected", func(m model) model { return send(m, keys("down", "down")...) }),
		manager("list, failed connection selected", func(m model) model { return send(m, keys("down", "down", "down", "down", "down")...) }),
		manager("details opened with enter (narrow panes only)", func(m model) model {
			return send(m, keys("down", "down", "down", "down", "down", "enter")...)
		}),
		manager("details of a connection waiting for an answer", func(m model) model {
			return send(m, keys("down", "down", "down", "down", "enter", "esc")...)
		}),
		manager("no connections", func(m model) model { m.conns, m.jobs = nil, nil; return m }),
		manager("add: alias picker filtered by \"a\"", func(m model) model { return send(m, keys("a", "a")...) }),
		manager("add: no alias matches the typed target", func(m model) model { return send(m, keys("a", "deploy@10.0.9.1")...) }),
		manager("add: form for a picked alias", func(m model) model { return send(m, keys("a", "s", "a", "n", "enter")...) }),
		manager("edit: active connection with a changed target", func(m model) model {
			m = send(m, keys("e", "tab")...)
			m.fields[1].SetValue("prod-new")
			return m
		}),
		manager("edit: a question arrives while typing", func(m model) model {
			return send(m, append(keys("e", "tab"), jobMsg(waitingJob("j2", "c2", "Enter the verification code:")))...)
		}),
		manager("forget confirmation, connected", func(m model) model { return send(m, keys("d")...) }),
		manager("forget confirmation, not connected", func(m model) model { return send(m, keys("down", "d")...) }),
		manager("confirmation question in the manager, Yes chosen", func(m model) model {
			return send(m, keys("down", "down", "down", "down", "enter", "right")...)
		}),
		manager("password question in the manager, draft typed", func(m model) model {
			return send(m, keys("down", "down", "down", "down", "down", "down", "enter", "hunter2")...)
		}),
		manager("question whose answer could not be sent", func(m model) model {
			m = send(m, keys("down", "down", "down", "down", "enter", "enter")...)
			return send(m, promptReplyMsg{jobID: "j5", prompt: m.dialog.prompt, err: errors.New("daemon: connection reset by peer")})
		}),
		manager("daemon unreachable", func(m model) model {
			return send(m, failureMsg("dial unix /Users/vika/.local/state/herdr/plugins/herdr.machine-manager/manager.sock: connect: no such file or directory"))
		}),
		manager("smallest supported pane, list", func(m model) model { return m }, [2]int{40, 10}),
		manager("smallest supported pane, details", func(m model) model { return send(m, keys("down", "down", "enter")...) }, [2]int{40, 10}),
		manager("smallest supported pane, edit form", func(m model) model { return send(m, keys("e")...) }, [2]int{40, 10}),
		pane("standalone prompt pane: confirmation, two questions waiting", promptSizes, two),
		pane("standalone prompt pane: password, draft typed", promptSizes, secretOnly, keys("hunter2")...),
		pane("standalone prompt pane: loading", [][2]int{{64, 13}}, nil),
	}
}

// TestWriteScreens renders every screen into the file MM_UI_SCREENS names,
// for reviewing the design as plain text.
func TestWriteScreens(t *testing.T) {
	path := os.Getenv("MM_UI_SCREENS")
	if path == "" {
		t.Skip("MM_UI_SCREENS is not set")
	}
	var b strings.Builder
	for _, s := range sampleScreens() {
		for _, size := range s.sizes {
			w, h := size[0], size[1]
			fmt.Fprintf(&b, "=== %s (%dx%d) %s\n", s.name, w, h, strings.Repeat("=", max(0, 70-len(s.name))))
			for _, line := range strings.Split(ansi.Strip(s.build(w, h).View()), "\n") {
				b.WriteString(strings.TrimRight(line, " ") + "\n")
			}
			b.WriteString("\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWriteScreensHTML renders every screen in true colour, on a dark and on a
// light background, into the standalone HTML page MM_UI_SCREENS_HTML names.
// Each screen is shown at its first size: 100x26 for the manager, 64x13 for
// the prompt pane, 40x10 for the smallest-pane screens.
func TestWriteScreensHTML(t *testing.T) {
	path := os.Getenv("MM_UI_SCREENS_HTML")
	if path == "" {
		t.Skip("MM_UI_SCREENS_HTML is not set")
	}
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		lipgloss.SetHasDarkBackground(dark)
	})
	lipgloss.SetColorProfile(termenv.TrueColor)
	themes := []struct {
		name, bg, fg string
		dark         bool
	}{{"dark", "#1e1e2e", "#cdd6f4", true}, {"light", "#fafafa", "#24242e", false}}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>machine-manager screens</title>
<style>
body { margin: 24px; background: #8a8a96; font-family: -apple-system, system-ui, sans-serif; }
h2 { font-size: 14px; font-weight: 600; margin: 28px 0 8px; color: #111; }
.pair { display: flex; flex-wrap: wrap; gap: 16px; align-items: flex-start; }
pre { margin: 0; padding: 10px 12px; border-radius: 8px; font: 13px/1.15 ui-monospace, "SF Mono", Menlo, Consolas, monospace; }
</style></head><body>
<h1 style="font-size:18px">machine-manager popup, rendered from View() in true colour</h1>
`)
	for _, s := range sampleScreens() {
		w, h := s.sizes[0][0], s.sizes[0][1]
		fmt.Fprintf(&b, "<h2>%s (%dx%d)</h2>\n<div class=\"pair\">\n", html.EscapeString(s.name), w, h)
		for _, theme := range themes {
			lipgloss.SetHasDarkBackground(theme.dark)
			view := s.build(w, h).View()
			for i, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > w {
					t.Errorf("%s, %s: line %d is %d columns", s.name, theme.name, i, lipgloss.Width(line))
				}
			}
			fmt.Fprintf(&b, "<pre style=\"width:%dch;background:%s;color:%s\">%s</pre>\n", w, theme.bg, theme.fg, ansiHTML(view, theme.fg, theme.bg))
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("</body></html>\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

var sgrPattern = regexp.MustCompile("\x1b\\[([0-9;?]*)([A-Za-z])")

// ansiHTML converts the SGR sequences Lip Gloss writes in true colour (bold,
// faint, underline, reverse, 24-bit foreground and background) to styled
// spans, and drops every other control sequence.
func ansiHTML(s, fg, bg string) string {
	var b strings.Builder
	var cur struct {
		fg, bg                         string
		bold, faint, underline, invert bool
	}
	emit := func(text string) {
		if text == "" {
			return
		}
		f, g := cur.fg, cur.bg
		if cur.invert {
			f, g = or(cur.bg, bg), or(cur.fg, fg)
		}
		var css []string
		if f != "" {
			css = append(css, "color:"+f)
		}
		if g != "" {
			css = append(css, "background:"+g)
		}
		if cur.bold {
			css = append(css, "font-weight:bold")
		}
		if cur.faint {
			css = append(css, "opacity:0.6")
		}
		if cur.underline {
			css = append(css, "text-decoration:underline")
		}
		if css == nil {
			b.WriteString(html.EscapeString(text))
			return
		}
		fmt.Fprintf(&b, "<span style=\"%s\">%s</span>", strings.Join(css, ";"), html.EscapeString(text))
	}
	rest := s
	for _, loc := range sgrPattern.FindAllStringSubmatchIndex(s, -1) {
		emit(s[len(s)-len(rest) : loc[0]])
		rest = s[loc[1]:]
		if s[loc[4]:loc[5]] != "m" {
			continue
		}
		params := strings.Split(s[loc[2]:loc[3]], ";")
		for i := 0; i < len(params); i++ {
			switch n, _ := strconv.Atoi(params[i]); {
			case n == 0:
				cur.fg, cur.bg, cur.bold, cur.faint, cur.underline, cur.invert = "", "", false, false, false, false
			case n == 1:
				cur.bold = true
			case n == 2:
				cur.faint = true
			case n == 22:
				cur.bold, cur.faint = false, false
			case n == 4:
				cur.underline = true
			case n == 24:
				cur.underline = false
			case n == 7:
				cur.invert = true
			case n == 27:
				cur.invert = false
			case n == 39:
				cur.fg = ""
			case n == 49:
				cur.bg = ""
			case (n == 38 || n == 48) && i+4 < len(params) && params[i+1] == "2":
				r, _ := strconv.Atoi(params[i+2])
				g, _ := strconv.Atoi(params[i+3])
				bl, _ := strconv.Atoi(params[i+4])
				colour := fmt.Sprintf("#%02x%02x%02x", r, g, bl)
				if n == 38 {
					cur.fg = colour
				} else {
					cur.bg = colour
				}
				i += 4
			}
		}
	}
	emit(rest)
	return b.String()
}
