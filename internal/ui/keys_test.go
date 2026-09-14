package ui

import (
	"context"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

// pressKey inspects the model and the returned command without running the
// command: actions would otherwise contact the daemon.
func pressKey(t *testing.T, m model, key tea.KeyMsg) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.key(key)
	got, ok := updated.(model)
	if !ok {
		t.Fatalf("key returned %T, want ui.model", updated)
	}
	return got, cmd
}

func TestListNavigationUsesVisiblePageAndKeepsEndpointsInBounds(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.height = 10
	for i := range 12 {
		m.conns = append(m.conns, conn(fmt.Sprintf("c%d", i), fmt.Sprintf("Machine %d", i), "host", false))
	}
	page := m.rows()

	for _, step := range []struct {
		key            tea.KeyType
		cursor, offset int
	}{
		{tea.KeyPgDown, page, 1},
		{tea.KeyPgDown, min(2*page, 11), min(page+1, 12-page)},
		{tea.KeyPgDown, 11, 12 - page},
		{tea.KeyEnd, 11, 12 - page},
		{tea.KeyPgUp, 11 - page, 11 - page},
		{tea.KeyHome, 0, 0},
		{tea.KeyPgUp, 0, 0},
		{tea.KeyUp, 0, 0},
		{tea.KeyCtrlN, 1, 0},
		{tea.KeyCtrlP, 0, 0},
	} {
		m, _ = pressKey(t, m, tea.KeyMsg{Type: step.key})
		if m.cursor != step.cursor || m.offset != step.offset {
			t.Errorf("after %s: cursor/offset = %d/%d, want %d/%d", tea.KeyMsg{Type: step.key}, m.cursor, m.offset, step.cursor, step.offset)
		}
	}

	m.conns = nil
	m.cursor, m.offset = 0, 0
	for _, key := range []tea.KeyType{tea.KeyPgDown, tea.KeyPgUp, tea.KeyHome, tea.KeyEnd, tea.KeyDown, tea.KeyUp, tea.KeyCtrlN, tea.KeyCtrlP} {
		m, _ = pressKey(t, m, tea.KeyMsg{Type: key})
		if m.cursor != 0 || m.offset != 0 {
			t.Errorf("empty list after %s: cursor/offset = %d/%d", tea.KeyMsg{Type: key}, m.cursor, m.offset)
		}
	}
}

func TestListActionKeysOpenTheExpectedScreenWithoutRunningCommands(t *testing.T) {
	base := newModel(context.Background(), nil)
	base.conns = []daemon.Connection{conn("c1", "One", "host", false)}

	for _, key := range []tea.KeyType{tea.KeyInsert, tea.KeyCtrlA} {
		got, cmd := pressKey(t, base, tea.KeyMsg{Type: key})
		if got.screen != screenAliases || cmd == nil {
			t.Errorf("%s: screen = %v, command present = %v; want alias picker with load command", tea.KeyMsg{Type: key}, got.screen, cmd != nil)
		}
	}

	for _, key := range []tea.KeyType{tea.KeyCtrlE, tea.KeyF2} {
		got, cmd := pressKey(t, base, tea.KeyMsg{Type: key})
		if got.screen != screenForm || got.editing != "c1" || cmd != nil {
			t.Errorf("%s: screen = %v, editing = %q, command present = %v; want edit form", tea.KeyMsg{Type: key}, got.screen, got.editing, cmd != nil)
		}
	}

	got, cmd := pressKey(t, base, tea.KeyMsg{Type: tea.KeyDelete})
	if got.screen != screenConfirm || got.confirm == nil || cmd != nil {
		t.Fatalf("Delete: screen = %v, confirm present = %v, command present = %v; want confirmation only", got.screen, got.confirm != nil, cmd != nil)
	}
	got, cmd = pressKey(t, got, tea.KeyMsg{Type: tea.KeyEnter})
	if got.screen != screenList || got.confirm != nil || cmd == nil {
		t.Errorf("Enter confirmation: screen = %v, confirm present = %v, command present = %v", got.screen, got.confirm != nil, cmd != nil)
	}

	for _, key := range []tea.KeyType{tea.KeyF5, tea.KeyCtrlR} {
		got, cmd = pressKey(t, base, tea.KeyMsg{Type: key})
		if got.screen != screenList || cmd == nil {
			t.Errorf("%s: screen = %v, command present = %v; want refresh", tea.KeyMsg{Type: key}, got.screen, cmd != nil)
		}
	}
}

func TestAliasNavigationLeavesHomeAndEndForFilterEditing(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen, m.height = screenAliases, 10
	m.aliases = make([]sshconfig.Alias, 12)
	for i := range m.aliases {
		m.aliases[i] = sshconfig.Alias{Name: fmt.Sprintf("host-%02d", i)}
	}
	m.aliasFilter.Focus()

	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.aliasCursor != m.aliasRows() {
		t.Errorf("PageDown: alias cursor = %d, want one visible page (%d)", m.aliasCursor, m.aliasRows())
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if m.aliasCursor != len(m.aliases)-1 {
		t.Errorf("Ctrl+End: alias cursor = %d, want final alias", m.aliasCursor)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlHome})
	if m.aliasCursor != 0 || m.aliasOffset != 0 {
		t.Errorf("Ctrl+Home: alias cursor/offset = %d/%d, want 0/0", m.aliasCursor, m.aliasOffset)
	}

	m.aliasFilter.SetValue("ost")
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyHome})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if got := m.aliasFilter.Value(); got != "host" {
		t.Errorf("Home then type: filter = %q, want insertion at start", got)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	if got := m.aliasFilter.Value(); got != "host-" {
		t.Errorf("End then type: filter = %q, want insertion at end", got)
	}
	m.aliasFilter.SetValue("ost")
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if got := m.aliasFilter.Value(); got != "host" || m.screen != screenAliases {
		t.Errorf("Ctrl+A then type: filter = %q, screen = %v; want insertion at start", got, m.screen)
	}
}

func TestFormNavigationPreservesTextAndSpaceTogglesOnlyCheckbox(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen = screenForm
	m.label.SetValue("my")
	m.target.SetValue("host")
	m.focusField()

	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if m.label.Value() != "my " || m.install {
		t.Errorf("space in label: value = %q, install = %v", m.label.Value(), m.install)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.field != 1 || m.label.Value() != "my " || m.target.Value() != "host" {
		t.Errorf("Tab: field = %d, label = %q, target = %q", m.field, m.label.Value(), m.target.Value())
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.field != 0 || m.label.Value() != "my " || m.target.Value() != "host" {
		t.Errorf("Shift+Tab: field = %d, label = %q, target = %q", m.field, m.label.Value(), m.target.Value())
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.field != 1 || m.label.Value() != "my " || m.target.Value() != "host" {
		t.Errorf("Ctrl+N: field = %d, label = %q, target = %q", m.field, m.label.Value(), m.target.Value())
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.field != 0 || m.label.Value() != "my " || m.target.Value() != "host" {
		t.Errorf("Ctrl+P: field = %d, label = %q, target = %q", m.field, m.label.Value(), m.target.Value())
	}
	m.field = 3
	m.focusField()
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if !m.install || m.label.Value() != "my " {
		t.Errorf("space on checkbox: install = %v, label = %q", m.install, m.label.Value())
	}
	m, cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.screen != screenList || cmd == nil {
		t.Errorf("Ctrl+S: screen = %v, command present = %v; want save", m.screen, cmd != nil)
	}
}

func TestFormCtrlAAndCtrlEEditTheCurrentField(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen = screenForm
	m.label.SetValue("host")
	m.focusField()
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if m.screen != screenForm || m.label.Value() != "ahostz" {
		t.Errorf("Ctrl+A/Ctrl+E in a form: screen = %v, label = %q", m.screen, m.label.Value())
	}
}

func TestOutputCancelShortcutDoesNotConsumeLiteralInput(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen = screenOutput
	m.conns = []daemon.Connection{conn("c1", "One", "host", false)}
	m, _ = promptUpdate(t, m, jobMsg(waitingJob("job-1", "c1", "Enter code:")))
	if m.dialog == nil {
		t.Fatal("waiting job did not open an input dialog")
	}

	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.dialog == nil || m.dialog.input.Value() != "x" || m.screen != screenOutput {
		t.Errorf("typing x in dialog: dialog = %+v, screen = %v", m.dialog, m.screen)
	}
	m, cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.screen != screenOutput || m.dialog != nil || cmd == nil {
		t.Errorf("Ctrl+X: screen = %v, dialog = %+v, command present = %v; want cancel command", m.screen, m.dialog, cmd != nil)
	}
}

func TestDetailsEditReturnsToDetailsAfterDismissingPrompt(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen = screenOutput
	m.conns = []daemon.Connection{conn("c1", "One", "host", false)}
	m, _ = promptUpdate(t, m, jobMsg(waitingJob("job-1", "c1", "Enter code:")))
	for _, letter := range "er" {
		m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{letter}})
	}
	if m.dialog == nil || m.dialog.input.Value() != "er" || m.screen != screenOutput {
		t.Fatalf("typing e/r in dialog: dialog = %+v, screen = %v", m.dialog, m.screen)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.dialog == nil || m.dialog.input.Value() != "xer" {
		t.Fatalf("Ctrl+A while answering: dialog = %+v", m.dialog)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.dialog == nil || m.dialog.input.Value() != "xery" {
		t.Fatalf("Ctrl+E while answering: dialog = %+v; want insertion at end", m.dialog)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil || m.screen != screenOutput {
		t.Fatalf("Esc from dialog: dialog = %+v, screen = %v; want details", m.dialog, m.screen)
	}
	m, cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if m.screen != screenForm || m.formBack != screenOutput || m.editing != "c1" || m.label.Value() != "One" || m.target.Value() != "host" || cmd != nil {
		t.Fatalf("Ctrl+E from details: screen = %v, back = %v, editing = %q, label = %q, target = %q, command present = %v", m.screen, m.formBack, m.editing, m.label.Value(), m.target.Value(), cmd != nil)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenOutput || m.dialog != nil {
		t.Fatalf("Esc from edit: screen = %v, dialog = %+v; want details", m.screen, m.dialog)
	}
	m, _ = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m, cmd = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.screen != screenOutput || cmd == nil {
		t.Errorf("save from details: screen = %v, command present = %v; want return to details", m.screen, cmd != nil)
	}
}

func TestDetailsCtrlEOpensEditWhenNotAnswering(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.screen = screenOutput
	m.conns = []daemon.Connection{conn("c1", "One", "host", false)}
	m, cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if m.screen != screenForm || m.formBack != screenOutput || m.editing != "c1" || cmd != nil {
		t.Errorf("Ctrl+E from details: screen = %v, back = %v, editing = %q, command present = %v", m.screen, m.formBack, m.editing, cmd != nil)
	}
}
