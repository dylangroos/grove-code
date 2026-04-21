// Package sessionlist renders the left-hand session picker.
package sessionlist

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dylangroos/grove-code/internal/session"
)

var (
	styleSel       = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15"))
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleAgent     = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleBranch    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleHead      = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleHeadFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
)

// headerRows is the number of non-item rows at the top of the pane
// ("Sessions" title + divider). Click Y must be offset past these.
const headerRows = 2

type Model struct {
	items   []*session.Session
	sel     int
	off     int // index of the top visible item
	w, h    int
	focused bool
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
	m.ensureVisible()
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.ensureVisible()
}

// SetFocused toggles the visual focus indicator on the sessions pane.
func (m *Model) SetFocused(b bool) { m.focused = b }

func (m *Model) MoveUp() {
	if m.sel > 0 {
		m.sel--
	}
	m.ensureVisible()
}
func (m *Model) MoveDown() {
	if m.sel < len(m.items)-1 {
		m.sel++
	}
	m.ensureVisible()
}
func (m *Model) Selected() *session.Session {
	if m.sel >= 0 && m.sel < len(m.items) {
		return m.items[m.sel]
	}
	return nil
}

// SetSelected moves the highlight to index i (clamped) and scrolls so it's
// visible. Returns true if the selection actually changed.
func (m *Model) SetSelected(i int) bool {
	if i < 0 || i >= len(m.items) {
		return false
	}
	if i == m.sel {
		return false
	}
	m.sel = i
	m.ensureVisible()
	return true
}

// Update handles mouse messages for the sessions list. The caller is
// responsible for translating coordinates into list-local space (X=0..w,
// Y=0..h where Y=0 is the "Sessions" header row) before calling.
// Returns (model, cmd, selectionChanged).
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		// Wheel scrolls the viewport without moving selection.
		switch msg.Button {
		case tea.MouseWheelUp:
			m.off -= 2
		case tea.MouseWheelDown:
			m.off += 2
		}
		m.clampOffset()
		return m, nil, false
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil, false
		}
		row := msg.Y - headerRows
		if row < 0 {
			return m, nil, false
		}
		idx := m.off + row
		if idx < 0 || idx >= len(m.items) {
			return m, nil, false
		}
		changed := m.SetSelected(idx)
		return m, nil, changed
	}
	return m, nil, false
}

func (m *Model) visibleRows() int {
	n := m.h - headerRows
	if n < 0 {
		return 0
	}
	return n
}

// ensureVisible clamps off so sel is within the visible window.
func (m *Model) ensureVisible() {
	vis := m.visibleRows()
	if vis <= 0 {
		m.off = 0
		return
	}
	if m.sel < m.off {
		m.off = m.sel
	} else if m.sel >= m.off+vis {
		m.off = m.sel - vis + 1
	}
	m.clampOffset()
}

func (m *Model) clampOffset() {
	vis := m.visibleRows()
	maxOff := len(m.items) - vis
	if maxOff < 0 {
		maxOff = 0
	}
	if m.off > maxOff {
		m.off = maxOff
	}
	if m.off < 0 {
		m.off = 0
	}
}

func (m Model) View() string {
	var b strings.Builder
	if m.focused {
		b.WriteString(styleHeadFocus.Render("▸ Sessions"))
	} else {
		b.WriteString(styleHead.Render("  Sessions"))
	}
	b.WriteString("\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", max(0, m.w-1))))
	b.WriteString("\n")
	if len(m.items) == 0 {
		b.WriteString(styleDim.Render("  (none — press n)"))
		b.WriteString("\n")
		return b.String()
	}
	vis := m.visibleRows()
	if vis <= 0 {
		vis = len(m.items)
	}
	end := m.off + vis
	if end > len(m.items) {
		end = len(m.items)
	}
	for i := m.off; i < end; i++ {
		s := m.items[i]
		// Sibling check compares against the previous item in the full list
		// (not the previous rendered row) so scrolling doesn't reshape the tree.
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
