package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
		manager("add: form for a picked alias", func(m model) model { return send(m, keys("a", "s", "a", "n", "enter")...) }),
		manager("edit: active connection with a changed target", func(m model) model {
			m = send(m, keys("e", "tab")...)
			m.fields[1].SetValue("prod-new")
			return m
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
