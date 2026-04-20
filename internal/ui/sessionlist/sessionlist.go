// Package sessionlist renders the left-hand session picker.
package sessionlist

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dylangroos/grove-code/internal/session"
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
	// Group by worktree path (siblings cluster); chronological within a group.
	// `Registry.All()` ranges over a map so input order is non-deterministic;
	// sorting here gives a stable, grouped view.
	sorted := make([]*session.Session, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].WorktreePath != sorted[j].WorktreePath {
			return sorted[i].WorktreePath < sorted[j].WorktreePath
		}
		return sorted[i].StartedAt.Before(sorted[j].StartedAt)
	})
	m.items = sorted
	if m.sel >= len(sorted) {
		m.sel = len(sorted) - 1
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
		// If this session shares a worktree with the one above, render it as
		// a sibling under the same branch (tree-char prefix, dim branch label).
		sibling := i > 0 && m.items[i-1].WorktreePath == s.WorktreePath
		var line string
		if sibling {
			line = fmt.Sprintf(" %s%s  %s",
				styleDim.Render("└─ "),
				status(s),
				styleDim.Render(truncate(s.AgentID, 8)))
		} else {
			line = fmt.Sprintf(" %s %s  %s",
				status(s),
				styleAgent.Render(truncate(s.AgentID, 8)),
				styleBranch.Render(truncate(s.Branch, m.w-14)))
		}
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
