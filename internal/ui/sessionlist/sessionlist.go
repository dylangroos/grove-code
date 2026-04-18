// Package sessionlist renders the left-hand session picker.
package sessionlist

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dgroos/grove-code/internal/session"
)

var (
	styleSel  = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15"))
	styleDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleAgent = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleBranch = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleHead = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
)

type Model struct {
	items []*session.Session
	sel   int
	w, h  int
}

func New() Model { return Model{} }

func (m *Model) SetItems(items []*session.Session) {
	m.items = items
	if m.sel >= len(items) {
		m.sel = len(items) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

func (m *Model) SetSize(w, h int) { m.w, m.h = w, h }

func (m *Model) MoveUp() {
	if m.sel > 0 {
		m.sel--
	}
}
func (m *Model) MoveDown() {
	if m.sel < len(m.items)-1 {
		m.sel++
	}
}
func (m *Model) Selected() *session.Session {
	if m.sel >= 0 && m.sel < len(m.items) {
		return m.items[m.sel]
	}
	return nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(styleHead.Render("Sessions"))
	b.WriteString("\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", max(0, m.w-1))))
	b.WriteString("\n")
	if len(m.items) == 0 {
		b.WriteString(styleDim.Render("  (none — press n)"))
		b.WriteString("\n")
	}
	for i, s := range m.items {
		line := fmt.Sprintf(" %s %s  %s",
			status(s),
			styleAgent.Render(truncate(s.AgentID, 8)),
			styleBranch.Render(truncate(s.Branch, m.w-14)))
		if i == m.sel {
			line = styleSel.Render(pad(line, m.w-1))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func status(s *session.Session) string {
	switch s.Status {
	case session.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("●")
	case session.StatusExited:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("○")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗")
	}
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func pad(s string, n int) string {
	for lipgloss.Width(s) < n {
		s += " "
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
