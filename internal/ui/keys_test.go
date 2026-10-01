package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// press applies keys without running the commands they return: those would
// call the daemon. It reports the command of the last key.
func press(t *testing.T, m model, names ...string) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range keys(names...) {
		m, cmd = promptUpdate(t, m, msg)
	}
	return m, cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestBrowseMovesWithArrowsAndJK(t *testing.T) {
	m := sized(sampleModel(), 100, 26)
	m, _ = press(t, m, "j", "j", "down")
	if m.cursor != 3 {
		t.Fatalf("cursor = %d after three moves down, want 3", m.cursor)
	}
	m, _ = press(t, m, "k")
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("cursor = %d after two moves up, want 1", m.cursor)
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	m, _ = press(t, m, "j")
	if m.cursor != len(m.conns)-1 {
		t.Errorf("cursor = %d, want it to stop at the last connection", m.cursor)
	}
}

func TestBrowseActionKeys(t *testing.T) {
	base := sized(sampleModel(), 100, 26) // c1, active, last job finished

	m, cmd := press(t, base, "a")
	if m.mode != modePick || cmd == nil || !m.filter.Focused() {
		t.Errorf("a: mode = %v, command = %v; want the alias picker loading aliases", m.mode, cmd != nil)
	}
	m, cmd = press(t, base, "e")
	if m.mode != modeForm || m.editing != "c1" || m.fields[0].Value() != "prod-api" || m.fields[2].Value() != "main" || cmd != nil {
		t.Errorf("e: mode = %v, editing = %q, label = %q; want the edit form filled in", m.mode, m.editing, m.fields[0].Value())
	}
	if _, cmd = press(t, base, "space"); cmd == nil {
		t.Error("space did not request a disconnect")
	}
	if _, cmd = press(t, base, "r"); cmd == nil {
		t.Error("r did not request a refresh")
	}
	if _, cmd = press(t, base, "x"); cmd != nil {
		t.Error("x issued a cancel for a connection with nothing to cancel")
	}
	if _, cmd = press(t, base, "down", "down", "x"); cmd == nil {
		t.Error("x did not cancel the running job")
	}
	if _, cmd = press(t, base, "down", "down", "space"); cmd != nil {
		t.Error("space queued another job behind the one in flight")
	}
	if m, cmd = press(t, base, "enter"); m.mode != modeList || cmd != nil {
		t.Errorf("enter in a wide pane: mode = %v; the panel is already shown", m.mode)
	}
	if _, cmd = press(t, base, "esc"); !isQuit(cmd) {
		t.Error("esc did not close the popup")
	}
	if _, cmd = press(t, base, "q"); cmd != nil {
		t.Error("q is not a binding and must do nothing")
	}
}

func TestNarrowPaneOpensDetailsOnDemand(t *testing.T) {
	m := sized(sampleModel(), 60, 14)
	m, _ = press(t, m, "enter")
	if m.mode != modeDetail {
		t.Fatalf("enter: mode = %v, want details", m.mode)
	}
	m, _ = press(t, m, "j")
	if m.mode != modeDetail || m.cursor != 1 {
		t.Errorf("moving in details: mode = %v, cursor = %d; the panel follows the selection", m.mode, m.cursor)
	}
	m, _ = press(t, m, "e", "esc")
	if m.mode != modeDetail {
		t.Errorf("esc from the form: mode = %v, want back to details", m.mode)
	}
	m, cmd := press(t, m, "esc")
	if m.mode != modeList || isQuit(cmd) {
		t.Errorf("esc from details: mode = %v; want the list, not closing", m.mode)
	}
	m = sized(send(sized(m, 60, 14), keys("enter", "e")...), 100, 26)
	if m, _ = press(t, m, "esc"); m.mode != modeList {
		t.Errorf("widening the pane under a form opened from details: esc leads to mode %v, want the list", m.mode)
	}
}

func TestNarrowPaneOpensDetailsUnderAWaitingQuestion(t *testing.T) {
	m := sized(sampleModel(), 60, 14)
	m, _ = press(t, m, "down", "down", "down", "down", "enter")
	if m.dialog == nil || m.mode != modeDetail {
		t.Fatalf("enter on a waiting connection: dialog = %v, mode = %v", m.dialog != nil, m.mode)
	}
	m, _ = press(t, m, "esc")
	if m.dialog != nil || m.mode != modeDetail {
		t.Errorf("setting the question aside: dialog = %v, mode = %v; want its details", m.dialog != nil, m.mode)
	}
}

func TestPickerTypesIntoTheFilterAndPicksAnAlias(t *testing.T) {
	m := sized(sampleModel(), 100, 26)
	m.installDefault = false
	m, _ = press(t, m, "a", "j", "k")
	if m.filter.Value() != "jk" || m.mode != modePick {
		t.Fatalf("filter = %q, mode = %v; letters belong to the filter", m.filter.Value(), m.mode)
	}
	m, _ = press(t, m, "esc")
	if m.mode != modeList {
		t.Fatalf("esc: mode = %v, want the list", m.mode)
	}
	m, _ = press(t, m, "a", "s", "a", "n", "d")
	if list := m.filtered(); len(list) != 1 || list[0].Name != "sandbox" {
		t.Fatalf("filtered = %+v, want sandbox", list)
	}
	m, _ = press(t, m, "enter")
	if m.mode != modeForm || m.editing != "" || m.fields[0].Value() != "sandbox" || m.fields[1].Value() != "sandbox" || m.install {
		t.Errorf("picked: mode = %v, editing = %q, label = %q, target = %q, install = %v", m.mode, m.editing, m.fields[0].Value(), m.fields[1].Value(), m.install)
	}
	m, _ = press(t, m, "esc")
	if m.mode != modeList {
		t.Errorf("esc from a new form: mode = %v, want the list", m.mode)
	}
}

func TestFormFieldsSwitchAndSave(t *testing.T) {
	m := sized(sampleModel(), 100, 26)
	m.installDefault = true
	m, _ = press(t, m, "e", "space")
	if m.fields[0].Value() != "prod-api " || !m.install {
		t.Errorf("space in the label: label = %q, install = %v; want a typed space", m.fields[0].Value(), m.install)
	}
	m, _ = press(t, m, "tab", "tab", "tab", "space")
	if m.field != 3 || m.install {
		t.Errorf("space on the switch: field = %d, install = %v; want it turned off", m.field, m.install)
	}
	m, _ = press(t, m, "x")
	if m.fields[0].Value() != "prod-api " {
		t.Error("a letter on the switch reached a text field")
	}
	m, _ = press(t, m, "tab")
	if m.field != 0 {
		t.Errorf("tab from the last field: field = %d, want the first", m.field)
	}
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.field != 3 {
		t.Errorf("shift+tab from the first field: field = %d, want the last", m.field)
	}

	m.fields[1].SetValue("prod-new")
	if _, ok := m.reconnects(); !ok {
		t.Error("changing an active connection's target did not warn of a reconnect")
	}
	m.fields[1].SetValue("prod")
	m.fields[0].SetValue("renamed")
	if _, ok := m.reconnects(); ok {
		t.Error("a label change warned of a reconnect; the daemon renames in place")
	}

	m.fields[0].SetValue(" ")
	m, cmd := press(t, m, "enter")
	if cmd != nil || m.mode != modeForm || m.failure == "" {
		t.Errorf("empty label: command = %v, mode = %v, failure = %q; want the form kept with an error", cmd != nil, m.mode, m.failure)
	}
	m.fields[0].SetValue("renamed")
	m, cmd = press(t, m, "enter")
	if cmd == nil || m.mode != modeList {
		t.Errorf("enter: command = %v, mode = %v; want a save and the list", cmd != nil, m.mode)
	}
}

func TestForgetAsksFirst(t *testing.T) {
	m := sized(sampleModel(), 100, 26)
	m, cmd := press(t, m, "d")
	if m.mode != modeForget || m.forget != "c1" || cmd != nil {
		t.Fatalf("d: mode = %v, forget = %q, command = %v; want the confirmation only", m.mode, m.forget, cmd != nil)
	}
	if got, cmd := press(t, m, "esc"); got.mode != modeList || cmd != nil {
		t.Errorf("esc: mode = %v, command = %v; want nothing forgotten", got.mode, cmd != nil)
	}
	if got, cmd := press(t, m, "y"); got.mode != modeForget || cmd != nil {
		t.Error("only enter confirms")
	}
	if got, cmd := press(t, m, "enter"); got.mode != modeList || cmd == nil {
		t.Errorf("enter: mode = %v, command = %v; want the forget requested", got.mode, cmd != nil)
	}
}

func TestCtrlCClosesFromAnyMode(t *testing.T) {
	for _, setup := range [][]string{nil, {"a"}, {"e"}, {"d"}, {"down", "down", "down", "down", "enter"}} {
		m, _ := press(t, sized(sampleModel(), 100, 26), setup...)
		if _, cmd := promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyCtrlC}); !isQuit(cmd) {
			t.Errorf("after %v: ctrl+c did not close", setup)
		}
	}
}
