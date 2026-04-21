// Package diffpane renders a unified git diff with chroma syntax highlighting.
package diffpane

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/dylangroos/grove-code/internal/gitx"
)

var (
	styleAdd       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDel       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleHeader    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleHeaderSel = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15")).Bold(true)
	styleHunk      = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleGutter    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// FileAnchor marks the rendered-content line number where a file's header
// begins, so clicks and j/k nav can jump the viewport to that file.
type FileAnchor struct {
	Name   string
	Line   int    // 0-indexed line offset in the rendered diff content
	Header string // raw pre-styled header text, for applying selection highlight
}

type Model struct {
	vp         viewport.Model
	repoRoot   string
	files      []*gitdiff.File
	rawContent string
	anchors    []FileAnchor
	selected   int
	err        error
	w, h       int
}

func New() Model {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	return Model{vp: vp}
}

func (m *Model) SetRepoRoot(p string) { m.repoRoot = p }

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.vp.SetWidth(w)
	m.vp.SetHeight(h)
}

type LoadedMsg struct {
	files []*gitdiff.File
	empty string
	err   error
}

// Refresh runs `git diff HEAD` and updates the viewport.
func (m *Model) Refresh() tea.Cmd {
	root := m.repoRoot
	return func() tea.Msg {
		if root == "" {
			return LoadedMsg{empty: "(no session selected)"}
		}
		g := gitx.New(root)
		raw, err := g.DiffWorktree(context.Background())
		if err != nil {
			return LoadedMsg{err: err}
		}
		if len(raw) == 0 {
			return LoadedMsg{empty: "(no uncommitted changes)"}
		}
		files, _, err := gitdiff.Parse(bytes.NewReader(raw))
		if err != nil {
			return LoadedMsg{err: err}
		}
		return LoadedMsg{files: files}
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadedMsg:
		m.err = msg.err
		if msg.empty != "" || len(msg.files) == 0 {
			m.files = nil
			m.anchors = nil
			m.selected = 0
			m.rawContent = msg.empty
			m.vp.SetContent(m.rawContent)
			return m, nil
		}
		// Preserve the caller's selection across periodic refreshes — poll
		// would otherwise yank the user out of whatever file they're reading.
		prevName := ""
		if m.selected >= 0 && m.selected < len(m.anchors) {
			prevName = m.anchors[m.selected].Name
		}
		m.files = msg.files
		m.selected = 0
		if prevName != "" {
			for i, f := range m.files {
				if bestName(f) == prevName {
					m.selected = i
					break
				}
			}
		}
		m.rawContent, m.anchors = Render(m.files)
		m.vp.SetContent(applySelection(m.rawContent, m.anchors, m.selected))
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "j", "down":
			if len(m.anchors) > 1 && m.selected < len(m.anchors)-1 {
				m.selected++
				m.rerender()
				m.jumpToSelected()
			}
			return m, nil
		case "k", "up":
			if len(m.anchors) > 1 && m.selected > 0 {
				m.selected--
				m.rerender()
				m.jumpToSelected()
			}
			return m, nil
		}
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		contentLine := m.vp.YOffset() + msg.Y
		for i, a := range m.anchors {
			if a.Line == contentLine {
				m.selected = i
				m.rerender()
				m.jumpToSelected()
				return m, nil
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) rerender() {
	if len(m.anchors) == 0 {
		return
	}
	m.vp.SetContent(applySelection(m.rawContent, m.anchors, m.selected))
}

func (m *Model) jumpToSelected() {
	if m.selected < 0 || m.selected >= len(m.anchors) {
		return
	}
	m.vp.SetYOffset(m.anchors[m.selected].Line)
}

func (m Model) View() string {
	if m.err != nil {
		return styleDel.Render("diff error: " + m.err.Error())
	}
	return m.vp.View()
}

// applySelection re-renders a single header line with the selected style by
// swapping it in the already-rendered content — avoids re-running chroma on
// the whole diff just to repaint one row.
func applySelection(content string, anchors []FileAnchor, sel int) string {
	if sel < 0 || sel >= len(anchors) {
		return content
	}
	a := anchors[sel]
	lines := strings.Split(content, "\n")
	if a.Line < 0 || a.Line >= len(lines) {
		return content
	}
	lines[a.Line] = styleHeaderSel.Render(a.Header)
	return strings.Join(lines, "\n")
}

// Render produces a colored unified-diff string plus file anchors marking
// the line offset of each file's header.
func Render(files []*gitdiff.File) (string, []FileAnchor) {
	var b strings.Builder
	var anchors []FileAnchor
	line := 0
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n")
			line++
		}
		hdr := fileHeader(f)
		anchors = append(anchors, FileAnchor{Name: bestName(f), Line: line, Header: hdr})
		b.WriteString(styleHeader.Render(hdr))
		b.WriteString("\n")
		line++
		if f.IsBinary {
			b.WriteString(styleGutter.Render("  (binary file)\n"))
			line++
			continue
		}
		lexer := lexerFor(bestName(f))
		for _, frag := range f.TextFragments {
			b.WriteString(styleHunk.Render(fmt.Sprintf("@@ -%d,%d +%d,%d @@",
				frag.OldPosition, frag.OldLines, frag.NewPosition, frag.NewLines)))
			b.WriteString("\n")
			line++
			for _, l := range frag.Lines {
				switch l.Op {
				case gitdiff.OpAdd:
					b.WriteString(styleAdd.Render("+"))
					b.WriteString(highlight(l.Line, lexer))
				case gitdiff.OpDelete:
					b.WriteString(styleDel.Render("-"))
					b.WriteString(styleDel.Render(strings.TrimRight(l.Line, "\n")))
					b.WriteString("\n")
				case gitdiff.OpContext:
					b.WriteString(styleGutter.Render(" "))
					b.WriteString(highlight(l.Line, lexer))
				}
				line++
			}
		}
	}
	return b.String(), anchors
}

func fileHeader(f *gitdiff.File) string {
	switch {
	case f.IsNew:
		return "+++ " + f.NewName + " (new)"
	case f.IsDelete:
		return "--- " + f.OldName + " (deleted)"
	case f.IsRename:
		return fmt.Sprintf("~ %s -> %s", f.OldName, f.NewName)
	default:
		return "~ " + f.NewName
	}
}

func bestName(f *gitdiff.File) string {
	if f.NewName != "" {
		return f.NewName
	}
	return f.OldName
}

func lexerFor(path string) chroma.Lexer {
	if path == "" {
		return lexers.Fallback
	}
	if l := lexers.Match(path); l != nil {
		return l
	}
	if l := lexers.Get(strings.TrimPrefix(filepath.Ext(path), ".")); l != nil {
		return l
	}
	return lexers.Fallback
}

func highlight(line string, lexer chroma.Lexer) string {
	line = strings.TrimRight(line, "\n")
	if lexer == nil {
		return line + "\n"
	}
	it, err := lexer.Tokenise(nil, line)
	if err != nil {
		return line + "\n"
	}
	var buf bytes.Buffer
	style := styles.Get("monokai")
	if err := formatters.TTY256.Format(&buf, style, it); err != nil {
		return line + "\n"
	}
	return buf.String() + "\n"
}
