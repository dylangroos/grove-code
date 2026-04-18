// Package app wires the panes and session registry into a Bubble Tea root model.
package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/dgroos/grove-code/internal/agent"
	"github.com/dgroos/grove-code/internal/gh"
	"github.com/dgroos/grove-code/internal/gitx"
	"github.com/dgroos/grove-code/internal/session"
	"github.com/dgroos/grove-code/internal/ui/diffpane"
	"github.com/dgroos/grove-code/internal/ui/logpane"
	"github.com/dgroos/grove-code/internal/ui/sessionlist"
	"github.com/dgroos/grove-code/internal/ui/termpane"
)

type tab int

const (
	tabTerm tab = iota
	tabDiff
	tabLog
)

type focus int

const (
	focusSessions focus = iota
	focusActive
)

type mode int

const (
	modeNormal mode = iota
	modeNewBranch
	modeConfirmQuit
)

type layout int

const (
	layoutTabbed layout = iota
	layoutSplit
)

type App struct {
	cfg      *agent.File
	repoRoot string

	prog *tea.Program

	reg      *session.Registry
	terms    map[string]*termpane.Model // sessionID -> pane
	active   string                     // sessionID

	list   sessionlist.Model
	diff   diffpane.Model
	log    logpane.Model
	input  textinput.Model

	tab    tab
	focus  focus
	mode   mode
	layout layout

	w, h         int
	termW, diffW int // derived by layout() in split mode
	status       string
}

// New creates the root model. repoRoot must be a git repository.
func New(cfg *agent.File, repoRoot string, reg *session.Registry) *App {
	ti := textinput.New()
	ti.Prompt = "branch name> "
	ti.Placeholder = "feature-xyz"
	a := &App{
		cfg:      cfg,
		repoRoot: repoRoot,
		reg:      reg,
		terms:    map[string]*termpane.Model{},
		list:     sessionlist.New(),
		diff:     diffpane.New(),
		log:      logpane.New(),
		input:    ti,
		tab:      tabTerm,
		focus:    focusSessions,
		mode:     modeNormal,
		layout:   layoutFromConfig(cfg),
	}
	a.diff.SetRepoRoot(repoRoot)
	a.log.SetRepoRoot(repoRoot)
	return a
}

// SetProgram wires the Bubble Tea program so goroutines (e.g. pty readers)
// can push messages into the Update loop.
func (a *App) SetProgram(p *tea.Program) { a.prog = p }

func layoutFromConfig(cfg *agent.File) layout {
	if cfg != nil && cfg.Defaults.Layout == "split" {
		return layoutSplit
	}
	return layoutTabbed
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.diff.Refresh(),
		a.log.Refresh(),
		tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollMsg{} }),
	)
}

// pollMsg: periodic refresh of diff/log while user is looking.
type pollMsg struct{}

type statusMsg struct{ text string }

type sessionCreatedMsg struct {
	s *session.Session
	m *termpane.Model
	err error
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		a.relayout()
		return a, nil
	case pollMsg:
		cmds := []tea.Cmd{tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollMsg{} })}
		// Split view keeps the diff live regardless of focus.
		if a.layout == layoutSplit {
			cmds = append(cmds, a.diff.Refresh())
		} else if a.focus == focusActive {
			switch a.tab {
			case tabDiff:
				cmds = append(cmds, a.diff.Refresh())
			case tabLog:
				cmds = append(cmds, a.log.Refresh())
			}
		}
		return a, tea.Batch(cmds...)
	case statusMsg:
		a.status = msg.text
		return a, nil
	case sessionCreatedMsg:
		if msg.err != nil {
			a.status = "create failed: " + msg.err.Error()
			return a, nil
		}
		a.reg.Add(msg.s)
		a.terms[msg.s.ID] = msg.m
		_ = a.reg.Save()
		a.active = msg.s.ID
		a.list.SetItems(a.reg.All())
		a.diff.SetRepoRoot(msg.s.WorktreePath)
		a.log.SetRepoRoot(msg.s.WorktreePath)
		a.tab = tabTerm
		a.focus = focusActive
		a.relayout()
		a.status = "session " + msg.s.ID + " started"
		return a, tea.Batch(msg.m.Init(), a.diff.Refresh())
	case termpane.ExitedMsg:
		if s := a.reg.Get(msg.ID); s != nil {
			s.Status = session.StatusExited
			_ = a.reg.Save()
		}
		a.list.SetItems(a.reg.All())
		if msg.Err != nil {
			a.status = "session " + msg.ID + " exited: " + msg.Err.Error()
		} else {
			a.status = "session " + msg.ID + " exited"
		}
		return a, nil
	}

	switch a.mode {
	case modeNewBranch:
		return a.updateNewBranch(msg)
	case modeConfirmQuit:
		return a.updateConfirmQuit(msg)
	}

	// Key routing. The rule: when the terminal pane is focused, every key
	// except ctrl+g (leader) and ctrl+c (emergency) flows to the agent PTY.
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch km.String() {
		case "ctrl+g":
			a.toggleFocus()
			return a, nil
		case "ctrl+c":
			return a, a.beginQuit()
		}
		terminalFocused := a.focus == focusActive && a.tab == tabTerm
		if !terminalFocused {
			if cmd := a.handleNormalKey(km); cmd != nil || a.mode != modeNormal {
				return a, cmd
			}
		}
	}

	// Route to focused area.
	if a.focus == focusActive {
		switch a.tab {
		case tabTerm:
			if m := a.activeTerm(); m != nil {
				var cmd tea.Cmd
				*m, cmd = updateTerm(*m, msg)
				return a, cmd
			}
		case tabDiff:
			var cmd tea.Cmd
			a.diff, cmd = a.diff.Update(msg)
			return a, cmd
		case tabLog:
			var cmd tea.Cmd
			a.log, cmd = a.log.Update(msg)
			return a, cmd
		}
	}

	// Forward pty refresh and keepalive ticks to all term models.
	switch msg.(type) {
	case termpane.RefreshMsg, termpane.KeepAliveMsg:
		var cmds []tea.Cmd
		for _, t := range a.terms {
			var cmd tea.Cmd
			*t, cmd = updateTerm(*t, msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return a, tea.Batch(cmds...)
	}
	return a, nil
}

func updateTerm(t termpane.Model, msg tea.Msg) (termpane.Model, tea.Cmd) {
	p := &t
	p2, cmd := p.Update(msg)
	return *p2, cmd
}

// handleNormalKey processes bare-letter grove commands. Called only when the
// terminal pane is NOT focused — otherwise keystrokes go to the agent PTY.
func (a *App) handleNormalKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "q":
		return a.beginQuit()
	case "1":
		a.tab = tabTerm
		a.focus = focusActive
		return nil
	case "2":
		a.tab = tabDiff
		a.focus = focusActive
		return a.diff.Refresh()
	case "3":
		a.tab = tabLog
		a.focus = focusActive
		return a.log.Refresh()
	case "n":
		a.mode = modeNewBranch
		a.input.SetValue("")
		a.input.Focus()
		return nil
	case "x":
		return a.killActive()
	case "j", "down":
		if a.focus == focusSessions {
			a.list.MoveDown()
			a.syncActive()
			return nil
		}
	case "k", "up":
		if a.focus == focusSessions {
			a.list.MoveUp()
			a.syncActive()
			return nil
		}
	case "enter":
		if a.focus == focusSessions {
			a.focus = focusActive
			return nil
		}
	case "P":
		return a.createPR()
	case "s":
		a.toggleLayout()
		return nil
	}
	return nil
}

func (a *App) toggleFocus() {
	if a.focus == focusSessions {
		a.focus = focusActive
	} else {
		a.focus = focusSessions
	}
}

func (a *App) toggleLayout() {
	if a.layout == layoutSplit {
		a.layout = layoutTabbed
	} else {
		a.layout = layoutSplit
	}
	a.relayout()
}

func (a *App) beginQuit() tea.Cmd {
	if len(a.reg.All()) == 0 {
		return tea.Quit
	}
	a.mode = modeConfirmQuit
	a.status = "quit? y/n  (will kill running agents)"
	return nil
}

func (a *App) createPR() tea.Cmd {
	s := a.currentSession()
	if s == nil {
		a.status = "no active session"
		return nil
	}
	if !gh.Available() {
		a.status = "gh CLI not installed"
		return nil
	}
	dir := s.WorktreePath
	return func() tea.Msg {
		out, err := gh.CreatePRWeb(context.Background(), dir, "")
		if err != nil {
			return statusMsg{text: "gh pr create: " + err.Error() + " " + strings.TrimSpace(string(out))}
		}
		return statusMsg{text: "PR draft opened"}
	}
}

func (a *App) updateNewBranch(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			a.mode = modeNormal
			a.input.Blur()
			return a, nil
		case "enter":
			v := strings.TrimSpace(a.input.Value())
			a.mode = modeNormal
			a.input.Blur()
			if v == "" {
				return a, nil
			}
			return a, a.startSession(v)
		}
	}
	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	return a, cmd
}

func (a *App) updateConfirmQuit(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "y":
			a.killAll()
			return a, tea.Quit
		case "n", "esc":
			a.mode = modeNormal
			a.status = ""
			return a, nil
		}
	}
	return a, nil
}

func (a *App) killActive() tea.Cmd {
	s := a.currentSession()
	if s == nil {
		return nil
	}
	if t := a.terms[s.ID]; t != nil {
		_ = t.Handle().Kill()
	}
	a.reg.Remove(s.ID)
	delete(a.terms, s.ID)
	_ = a.reg.Save()
	a.list.SetItems(a.reg.All())
	a.syncActive()
	a.status = "killed " + s.ID
	return nil
}

func (a *App) killAll() {
	for _, t := range a.terms {
		_ = t.Handle().Kill()
	}
}

func (a *App) startSession(branchName string) tea.Cmd {
	if len(a.cfg.Agents) == 0 {
		return func() tea.Msg { return sessionCreatedMsg{err: fmt.Errorf("no agents configured")} }
	}
	ag := a.cfg.Agents[0]
	prefix := a.cfg.Defaults.BranchPrefix
	branch := prefix + branchName
	repo := a.repoRoot
	return func() tea.Msg {
		wtPath := session.WorktreePathFor(a.cfg.Defaults.WorktreeRoot, repo, branch)
		ctx := context.Background()
		g := gitx.New(repo)
		if err := g.WorktreeAdd(ctx, wtPath, branch, ""); err != nil {
			return sessionCreatedMsg{err: fmt.Errorf("worktree add: %w", err)}
		}
		spec, err := agent.Resolve(ag, agent.TemplateVars{WorktreePath: wtPath, Branch: branch, RepoRoot: repo})
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		id := session.NewID()
		prog := a.prog
		h, err := termpane.Start(ctx, termpane.Spec{
			Command: spec.Command, Env: spec.Env, Cwd: spec.Cwd,
			Cols: 80, Rows: 24,
			OnDirty: func() {
				if prog != nil {
					prog.Send(termpane.RefreshMsg{ID: id})
				}
			},
		})
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		m := termpane.NewModel(id, h)
		s := &session.Session{
			ID: id, AgentID: ag.ID, RepoRoot: repo, WorktreePath: wtPath,
			Branch: branch, PID: h.PID(), Status: session.StatusRunning,
			StartedAt: time.Now(), LastActivity: time.Now(),
		}
		return sessionCreatedMsg{s: s, m: m}
	}
}

func (a *App) currentSession() *session.Session {
	if a.active != "" {
		if s := a.reg.Get(a.active); s != nil {
			return s
		}
	}
	return a.list.Selected()
}

func (a *App) activeTerm() *termpane.Model {
	s := a.currentSession()
	if s == nil {
		return nil
	}
	return a.terms[s.ID]
}

// syncActive updates diff/log repo roots to follow the selected session.
func (a *App) syncActive() {
	s := a.list.Selected()
	if s == nil {
		return
	}
	a.active = s.ID
	a.diff.SetRepoRoot(s.WorktreePath)
	a.log.SetRepoRoot(s.WorktreePath)
}

func (a *App) relayout() {
	listW := 28
	if a.w < 90 {
		listW = a.w / 3
	}
	bodyH := a.h - 4 // top header + tab bar + status bar + dialog area
	if bodyH < 5 {
		bodyH = a.h - 2
	}
	rightW := a.w - listW - 1
	a.list.SetSize(listW, bodyH)
	a.log.SetSize(rightW, bodyH)

	if a.layout == layoutSplit && a.tab != tabLog {
		// Terminal gets 55% of the right area, diff gets 45%, 1col gap.
		a.termW = rightW * 55 / 100
		a.diffW = rightW - a.termW - 1
		if a.diffW < 20 {
			a.diffW = 20
			a.termW = rightW - a.diffW - 1
		}
		a.diff.SetSize(a.diffW, bodyH)
		for _, t := range a.terms {
			t.SetSize(a.termW, bodyH)
		}
	} else {
		a.termW, a.diffW = rightW, rightW
		a.diff.SetSize(rightW, bodyH)
		for _, t := range a.terms {
			t.SetSize(rightW, bodyH)
		}
	}
}

// --- View ---

var (
	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleTabOn  = lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15")).Padding(0, 1)
	styleTabOff = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Padding(0, 1)
	styleStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

func (a *App) render() string {
	if a.w == 0 {
		return "initializing…"
	}
	header := styleHeader.Render("grove-code") + "  " + styleHint.Render(filepath.Base(a.repoRoot))
	list := a.list.View()
	body := a.renderBody()
	hint := styleHint.Render(a.hintText())
	status := styleStatus.Render(a.status)

	listW := 28
	if a.w < 90 {
		listW = a.w / 3
	}
	rightW := a.w - listW - 1

	// In split mode, show column headers instead of a tab bar (which is
	// misleading when both Terminal and Diff are visible at once). The
	// full-screen Log view still gets the standard tab bar.
	var topRight string
	if a.layout == layoutSplit && a.tab != tabLog {
		topRight = a.renderSplitHeaders()
	} else {
		topRight = a.renderTabs()
	}

	listPane := lipgloss.NewStyle().Width(listW).Render(list)
	rightPane := lipgloss.NewStyle().Width(rightW).Render(topRight + "\n" + body)
	main := lipgloss.JoinHorizontal(lipgloss.Top, listPane, rightPane)

	bottom := hint
	if a.status != "" {
		bottom = status + "  " + hint
	}
	if a.mode == modeNewBranch {
		bottom = a.input.View()
	}
	if a.mode == modeConfirmQuit {
		bottom = styleStatus.Render("quit? y/n")
	}
	return header + "\n" + main + "\n" + bottom
}

func (a *App) hintText() string {
	split := a.layout == layoutSplit && a.tab != tabLog
	switch {
	case a.focus == focusActive && a.tab == tabTerm:
		if split {
			return "[typing → agent]  ctrl+g → grove  (split: agent | diff)"
		}
		return "[typing → agent]  ctrl+g → grove commands"
	case a.focus == focusActive:
		return "j/k scroll  1/2/3 tab  ctrl+g → sessions"
	default:
		if split {
			return "j/k pick  n new  x kill  s tabbed  3 log  P pr  q quit  ctrl+g → terminal"
		}
		return "j/k pick  n new  x kill  s split  1/2/3 tab  P pr  q quit  ctrl+g → terminal"
	}
}

func (a *App) renderTabs() string {
	labels := []struct {
		t    tab
		text string
	}{{tabTerm, "Terminal"}, {tabDiff, "Diff"}, {tabLog, "Log"}}
	var parts []string
	for _, l := range labels {
		if l.t == a.tab {
			parts = append(parts, styleTabOn.Render(l.text))
		} else {
			parts = append(parts, styleTabOff.Render(l.text))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (a *App) renderBody() string {
	if a.layout == layoutSplit && a.tab != tabLog {
		return a.renderSplitBody()
	}
	switch a.tab {
	case tabTerm:
		if m := a.activeTerm(); m != nil {
			return m.View()
		}
		return styleHint.Render("(no session — press n)")
	case tabDiff:
		return a.diff.View()
	case tabLog:
		return a.log.View()
	}
	return ""
}

func (a *App) renderSplitHeaders() string {
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	left := lipgloss.NewStyle().Width(a.termW).Render(label.Render("Terminal"))
	right := lipgloss.NewStyle().Width(a.diffW).Render(label.Render("Diff"))
	gap := " "
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
}

func (a *App) renderSplitBody() string {
	termView := styleHint.Render("(no session — press n)")
	if m := a.activeTerm(); m != nil {
		termView = m.View()
	}
	left := lipgloss.NewStyle().Width(a.termW).Render(termView)
	right := lipgloss.NewStyle().Width(a.diffW).Render(a.diff.View())
	gap := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("│")
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
}
