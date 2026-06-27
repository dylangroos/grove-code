// Package diffpane renders a unified git diff with chroma syntax highlighting.
package diffpane

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
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
	styleAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDel    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleHunk   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleGutter = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	// diffStyle is resolved once; styles.Get is a map lookup we don't want per line.
	diffStyle = styles.Get("monokai")
)

type Model struct {
	vp       viewport.Model
	repoRoot string
	content  string
	lastHash uint64
	err      error
	w, h     int
}

func New() Model {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	return Model{vp: vp}
}

func (m *Model) SetRepoRoot(p string) {
	m.repoRoot = p
	m.lastHash = 0
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.vp.SetWidth(w)
	m.vp.SetHeight(h)
}

type LoadedMsg struct {
	content   string
	err       error
	hash      uint64
	unchanged bool
}

func fnvHash(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

// decideLoad turns raw `git diff` bytes into a LoadedMsg, skipping the parse +
// highlight work when the bytes are unchanged from the last render (prevHash).
func decideLoad(raw []byte, prevHash uint64) LoadedMsg {
	h := fnvHash(raw)
	if h == prevHash {
		return LoadedMsg{unchanged: true}
	}
	if len(raw) == 0 {
		return LoadedMsg{content: "(no uncommitted changes)", hash: h}
	}
	files, _, err := gitdiff.Parse(bytes.NewReader(raw))
	if err != nil {
		return LoadedMsg{err: err}
	}
	return LoadedMsg{content: Render(files), hash: h}
}

// Refresh runs `git diff HEAD` and updates the viewport.
func (m *Model) Refresh() tea.Cmd {
	root := m.repoRoot
	prevHash := m.lastHash
	return func() tea.Msg {
		if root == "" {
			return LoadedMsg{content: "(no session selected)"}
		}
		g := gitx.New(root)
		raw, err := g.DiffWorktree(context.Background())
		if err != nil {
			return LoadedMsg{err: err}
		}
		return decideLoad(raw, prevHash)
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadedMsg:
		if msg.unchanged {
			return m, nil
		}
		m.err = msg.err
		m.content = msg.content
		m.lastHash = msg.hash
		m.vp.SetContent(m.content)
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if m.err != nil {
		return styleDel.Render("diff error: " + m.err.Error())
	}
	return m.vp.View()
}

// Render produces a colored unified-diff string from parsed files.
func Render(files []*gitdiff.File) string {
	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(styleHeader.Render(fileHeader(f)))
		b.WriteString("\n")
		if f.IsBinary {
			b.WriteString(styleGutter.Render("  (binary file)\n"))
			continue
		}
		lexer := lexerFor(bestName(f))
		for _, frag := range f.TextFragments {
			b.WriteString(styleHunk.Render(fmt.Sprintf("@@ -%d,%d +%d,%d @@",
				frag.OldPosition, frag.OldLines, frag.NewPosition, frag.NewLines)))
			b.WriteString("\n")
			for _, line := range frag.Lines {
				switch line.Op {
				case gitdiff.OpAdd:
					b.WriteString(styleAdd.Render("+"))
					b.WriteString(highlight(line.Line, lexer))
				case gitdiff.OpDelete:
					b.WriteString(styleDel.Render("-"))
					b.WriteString(styleDel.Render(strings.TrimRight(line.Line, "\n")))
					b.WriteString("\n")
				case gitdiff.OpContext:
					b.WriteString(styleGutter.Render(" "))
					b.WriteString(highlight(line.Line, lexer))
				}
			}
		}
	}
	return b.String()
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
	if err := formatters.TTY256.Format(&buf, diffStyle, it); err != nil {
		return line + "\n"
	}
	return buf.String() + "\n"
}
