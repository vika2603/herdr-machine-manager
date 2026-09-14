package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// promptModel shares question handling with the manager but never renders or
// navigates to its list. After the final answer or dismissal its pane closes.
type promptModel struct {
	model
	loaded bool
}

func (m promptModel) Init() tea.Cmd { return m.loadList() }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && m.dialog == nil {
		if key.String() == "esc" || key.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	next, cmd := m.model.Update(msg)
	m.model = next.(model)
	if _, ok := msg.(listMsg); ok {
		m.loaded = true
	}
	if m.loaded && m.dialog == nil {
		if cmd != nil {
			return m, tea.Sequence(cmd, tea.Quit)
		}
		return m, tea.Quit
	}
	return m, cmd
}

func (m promptModel) View() string {
	content := dimStyle.Render("Loading question…")
	if m.dialog != nil {
		content = m.promptContent(max(1, m.cols()-4))
	} else if m.failure != "" {
		content = errStyle.Render(m.failure) + "\n\n" + dimStyle.Render("Esc close")
	}
	style := lipgloss.NewStyle().Padding(1, 2).Width(m.cols()).MaxWidth(m.cols())
	if m.height > 0 {
		style = style.MaxHeight(m.height)
	}
	return style.Render(content)
}
