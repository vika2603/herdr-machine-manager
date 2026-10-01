package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestFrameNeverExceedsThePane renders every sample screen at a range of sizes
// down to 40x10: a taller frame scrolls its own top away, a wider one wraps.
func TestFrameNeverExceedsThePane(t *testing.T) {
	for _, s := range sampleScreens() {
		for _, w := range []int{40, 47, 59, 64, 75, 76, 90, 100, 140} {
			for _, h := range []int{5, 7, 10, 11, 13, 14, 20, 26, 40} {
				lines := strings.Split(s.build(w, h).View(), "\n")
				if len(lines) != h {
					t.Errorf("%s at %dx%d: %d lines", s.name, w, h, len(lines))
				}
				for i, line := range lines {
					if lipgloss.Width(line) > w {
						t.Errorf("%s at %dx%d: line %d is %d columns: %q", s.name, w, h, i, lipgloss.Width(line), line)
					}
				}
			}
		}
	}
}

func TestAnswerControlSurvivesTinyPanes(t *testing.T) {
	for _, h := range []int{5, 6, 7, 8, 10} {
		m := sized(sampleModel(), 40, h)
		m, _ = press(t, m, "down", "down", "down", "down", "enter")
		if view := m.View(); !strings.Contains(view, "No") || !strings.Contains(view, "Yes") {
			t.Errorf("40x%d: the confirmation buttons were cut:\n%s", h, view)
		}
		m, _ = press(t, m, "esc", "down", "down", "enter")
		if view := m.View(); !strings.Contains(view, "›") {
			t.Errorf("40x%d: the password input was cut:\n%s", h, view)
		}
	}
}

func TestSelectionStaysVisible(t *testing.T) {
	m := sized(sampleModel(), 100, 26)
	m, _ = promptUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	m = sized(m, 40, 10)
	m.status = "refreshed"
	if view := m.View(); !strings.Contains(view, "▌ ◐ archive") {
		t.Errorf("the selected last connection is not on screen after shrinking:\n%s", view)
	}
}

func TestEveryHintIsABinding(t *testing.T) {
	keyFor := map[string][]tea.KeyMsg{
		"enter":  {{Type: tea.KeyEnter}},
		"esc":    {{Type: tea.KeyEsc}},
		"space":  {{Type: tea.KeySpace, Runes: []rune{' '}}},
		"tab":    {{Type: tea.KeyTab}},
		"ctrl+o": {{Type: tea.KeyCtrlO}},
		// One of the two arrows changes the choice, whichever is made.
		"←→": {{Type: tea.KeyRight}, {Type: tea.KeyLeft}},
	}
	for _, s := range sampleScreens() {
		for _, size := range [][2]int{{100, 26}, {60, 14}} {
			// Each key gets a fresh model: the open question is shared by
			// pointer between a model and its updated copy.
			for _, h := range s.build(size[0], size[1]).(interface{ hints() []hint }).hints() {
				msgs, named := keyFor[h.key]
				if !named {
					msgs = []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune(h.key)}}
				}
				acts := false
				for _, msg := range msgs {
					before := s.build(size[0], size[1]).View()
					next, cmd := s.build(size[0], size[1]).Update(msg)
					acts = acts || cmd != nil || next.View() != before
				}
				if !acts {
					t.Errorf("%s at %v: hint %q %q does nothing", s.name, size, h.key, h.action)
				}
			}
		}
	}
}

func TestWrapKeepsOptionsWhole(t *testing.T) {
	got := wrap("$ herdr machine add prod --label prod-api --remote-session main", 30)
	for _, line := range got {
		if lipgloss.Width(line) > 30 || strings.HasSuffix(line, "-") {
			t.Errorf("wrap produced %q", got)
		}
	}
	if got := wrap(strings.Repeat("x", 25), 10); len(got) != 3 {
		t.Errorf("a long word was not hard-wrapped: %q", got)
	}
}
