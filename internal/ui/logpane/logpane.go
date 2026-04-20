// Package logpane shows the commit log for a worktree and the diff of the
// currently selected commit.
package logpane

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/dylangroos/grove-code/internal/gitx"
	"github.com/dylangroos/grove-code/internal/ui/diffpane"
)

var (
	styleSel   = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15"))
	styleSha   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleDate  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleAuthor = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

type Model struct {
	repoRoot string
	commits  []gitx.Commit
	sel      int
	diffVp   viewport.Model
	diff     string
	err      error
	w, h     int
}

func New() Model {
	return Model{diffVp: viewport.New()}
}

func (m *Model) SetRepoRoot(p string) { m.repoRoot = p }

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	// Left 28 cols for commit list, rest for diff.
	listW := 32
	if w < 70 {
		listW = w / 3
	}
	m.diffVp.SetWidth(w - listW - 2)
	m.diffVp.SetHeight(h)
}

type LoadedMsg struct {
	commits []gitx.Commit
	err     error
}

type DiffLoadedMsg struct {
	content string
	err     error
}

// Refresh reloads the commit log.
func (m *Model) Refresh() tea.Cmd {
	root := m.repoRoot
	return func() tea.Msg {
		if root == "" {
			return LoadedMsg{}
		}
		g := gitx.New(root)
		cs, err := g.Log(context.Background(), "HEAD", 200)
		return LoadedMsg{commits: cs, err: err}
	}
}

func (m *Model) loadDiff(sha string) tea.Cmd {
	root := m.repoRoot
	return func() tea.Msg {
		g := gitx.New(root)
		raw, err := g.DiffCommit(context.Background(), sha)
		if err != nil {
			return DiffLoadedMsg{err: err}
		}
		files, _, err := gitdiff.Parse(bytes.NewReader(raw))
		if err != nil {
			return DiffLoadedMsg{err: err}
		}
		return DiffLoadedMsg{content: diffpane.Render(files)}
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadedMsg:
		m.commits = msg.commits
		m.err = msg.err
		if len(m.commits) > 0 {
			m.sel = 0
			return m, m.loadDiff(m.commits[0].SHA)
		}
	case DiffLoadedMsg:
		if msg.err != nil {
			m.diffVp.SetContent("error: " + msg.err.Error())
		} else {
			m.diffVp.SetContent(msg.content)
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "j", "down":
			if m.sel < len(m.commits)-1 {
				m.sel++
				return m, m.loadDiff(m.commits[m.sel].SHA)
			}
		case "k", "up":
			if m.sel > 0 {
				m.sel--
				return m, m.loadDiff(m.commits[m.sel].SHA)
			}
		}
	}
	var cmd tea.Cmd
	m.diffVp, cmd = m.diffVp.Update(msg)
	return m, cmd
}

// SelectedSHA returns the SHA of the currently-highlighted commit, or "".
func (m Model) SelectedSHA() string {
	if m.sel >= 0 && m.sel < len(m.commits) {
		return m.commits[m.sel].SHA
	}
	return ""
}

func (m Model) View() string {
	if m.err != nil {
		return "log error: " + m.err.Error()
	}
	listW := 32
	if m.w < 70 {
		listW = m.w / 3
	}
	var list strings.Builder
	maxLines := m.h
	for i, c := range m.commits {
		if i >= maxLines {
			break
		}
		line := fmt.Sprintf("%s %s %s",
			styleSha.Render(c.Short),
			styleAuthor.Render(truncate(c.Author, 10)),
			truncate(c.Subject, listW-22))
		if i == m.sel {
			line = styleSel.Render(padTo(ansiStrip(line), listW-1))
		}
		list.WriteString(line)
		list.WriteString("\n")
	}
	for i := len(m.commits); i < maxLines; i++ {
		list.WriteString("\n")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(listW).Render(list.String()),
		lipgloss.NewStyle().Width(m.w-listW-1).Render(m.diffVp.View()),
	)
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

func padTo(s string, n int) string {
	for lipgloss.Width(s) < n {
		s += " "
	}
	return s
}

// ansiStrip removes ANSI color codes so selection-highlight background
// isn't interrupted by resets. Naive but sufficient for our output.
func ansiStrip(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				c := s[j]
				j++
				if (c >= '@' && c <= '~') {
					break
				}
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	_ = styleDate // keep reference even if unused in View
	return b.String()
}
