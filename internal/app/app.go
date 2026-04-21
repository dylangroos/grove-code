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

	"github.com/dylangroos/grove-code/internal/agent"
	"github.com/dylangroos/grove-code/internal/gh"
	"github.com/dylangroos/grove-code/internal/gitx"
	"github.com/dylangroos/grove-code/internal/session"
	"github.com/dylangroos/grove-code/internal/ui/diffpane"
	"github.com/dylangroos/grove-code/internal/ui/logpane"
	"github.com/dylangroos/grove-code/internal/ui/sessionlist"
	"github.com/dylangroos/grove-code/internal/ui/termpane"
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
	cfg       *agent.File
	repoRoot  string
	launchCwd string // directory grove was launched from; used as the default attach target

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
// launchCwd is the directory grove was launched from — used as the default
// attach target when the user empty-submits with no session selected.
func New(cfg *agent.File, repoRoot string, reg *session.Registry, launchCwd string) *App {
	ti := textinput.New()
	ti.Prompt = "branch> "
	ti.Placeholder = "feature-xyz  (empty = attach agent to current worktree)"
	a := &App{
		cfg:       cfg,
		repoRoot:  repoRoot,
		launchCwd: launchCwd,
		reg:       reg,
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
	// Surface persisted sessions in the left pane immediately; they start as
	// exited (no PTY) and the user resumes them via enter/click.
	a.list.SetItems(a.reg.All())
	if s := a.list.Selected(); s != nil {
		a.active = s.ID
		a.diff.SetRepoRoot(s.WorktreePath)
		a.log.SetRepoRoot(s.WorktreePath)
	}
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
	case diffpane.LoadedMsg:
		var cmd tea.Cmd
		a.diff, cmd = a.diff.Update(msg)
		return a, cmd
	case logpane.LoadedMsg:
		var cmd tea.Cmd
		a.log, cmd = a.log.Update(msg)
		return a, cmd
	case logpane.DiffLoadedMsg:
		var cmd tea.Cmd
		a.log, cmd = a.log.Update(msg)
		return a, cmd
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

	// Mouse clicks and wheel events route by cursor position — not focus —
	// so scrolling and clicking always reach the pane under the cursor.
	if cmd, handled := a.routeMouse(msg); handled {
		return a, cmd
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
			if s := a.list.Selected(); s != nil && (s.Status != session.StatusRunning || a.terms[s.ID] == nil) {
				return a.resumeSession(s)
			}
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

// sessionsW returns the width of the left sessions column.
func (a *App) sessionsW() int {
	if a.w < 90 {
		return a.w / 3
	}
	return 28
}

// focusPrefix returns the selection-arrow prefix for focused panes, or
// matched-width spacing otherwise.
func focusPrefix(focused bool) string {
	if focused {
		return "▸ "
	}
	return "  "
}

// hitPane identifies which pane a screen coordinate falls on.
type hitPane int

const (
	hitNone hitPane = iota
	hitList
	hitTerm
	hitDiff
	hitLog
)

// hitTest maps a global (x,y) to a pane and that pane's origin in global
// coords. The caller subtracts the origin to get pane-local coordinates.
func (a *App) hitTest(x, y int) (hitPane, int, int) {
	if a.w == 0 || a.h == 0 {
		return hitNone, 0, 0
	}
	if y < 1 || y >= a.h-1 || x < 0 || x >= a.w {
		return hitNone, 0, 0
	}
	listW := a.sessionsW()
	// Left column: sessions list starts at y=1 (row 0 of main).
	if x < listW {
		return hitList, 0, 1
	}
	// Right column: body starts at y=2 (skipping the tab/split-header row).
	if y < 2 {
		return hitNone, 0, 0
	}
	if a.layout == layoutSplit && a.tab != tabLog {
		termEndX := listW + a.termW
		if x < termEndX {
			return hitTerm, listW, 2
		}
		if x == termEndX {
			return hitNone, 0, 0 // gap column
		}
		return hitDiff, termEndX + 1, 2
	}
	switch a.tab {
	case tabTerm:
		return hitTerm, listW, 2
	case tabDiff:
		return hitDiff, listW, 2
	case tabLog:
		return hitLog, listW, 2
	}
	return hitNone, 0, 0
}

// translateMouse returns a copy of msg with its coordinates shifted so that
// (originX, originY) becomes the pane-local origin.
func translateMouse(msg tea.Msg, originX, originY int) tea.Msg {
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		m.X -= originX
		m.Y -= originY
		return m
	case tea.MouseReleaseMsg:
		m.X -= originX
		m.Y -= originY
		return m
	case tea.MouseWheelMsg:
		m.X -= originX
		m.Y -= originY
		return m
	case tea.MouseMotionMsg:
		m.X -= originX
		m.Y -= originY
		return m
	}
	return msg
}

// routeMouse dispatches mouse click and wheel events by cursor position.
// Returns handled=true when the event was consumed, so the caller should not
// fall through to focus-based routing.
func (a *App) routeMouse(msg tea.Msg) (tea.Cmd, bool) {
	var mouse tea.Mouse
	isClick := false
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		mouse = m.Mouse()
		isClick = true
	case tea.MouseWheelMsg:
		mouse = m.Mouse()
	default:
		return nil, false
	}

	target, ox, oy := a.hitTest(mouse.X, mouse.Y)
	local := translateMouse(msg, ox, oy)
	switch target {
	case hitList:
		if isClick {
			a.focus = focusSessions
		}
		var changed bool
		a.list, _, changed = a.list.Update(local)
		if changed {
			a.syncActive()
		}
		return nil, true
	case hitDiff:
		if isClick {
			a.focus = focusActive
			a.tab = tabDiff
		}
		var cmd tea.Cmd
		a.diff, cmd = a.diff.Update(local)
		return cmd, true
	case hitTerm:
		// First click focuses the terminal; don't forward the phantom click
		// to the PTY. Subsequent clicks (while focused) go through.
		if isClick && (a.focus != focusActive || a.tab != tabTerm) {
			a.focus = focusActive
			a.tab = tabTerm
			return nil, true
		}
		return a.forwardToTerm(local), true
	case hitLog:
		if isClick {
			a.focus = focusActive
			a.tab = tabLog
			return nil, true
		}
		var cmd tea.Cmd
		a.log, cmd = a.log.Update(local)
		return cmd, true
	}
	return nil, true
}

func (a *App) toggleFocus() {
	if a.focus == focusSessions {
		a.focus = focusActive
	} else {
		a.focus = focusSessions
	}
}

// forwardToTerm delivers a pane-local mouse event to the active terminal, if
// one is focused and running.
func (a *App) forwardToTerm(local tea.Msg) tea.Cmd {
	if a.focus != focusActive || a.tab != tabTerm {
		return nil
	}
	m := a.activeTerm()
	if m == nil {
		return nil
	}
	var cmd tea.Cmd
	*m, cmd = updateTerm(*m, local)
	return cmd
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
				wt, branch, err := a.attachTarget()
				if err != nil {
					a.status = err.Error()
					return a, nil
				}
				return a, a.attachAgent(wt, branch)
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

// findAgent returns the configured agent whose ID matches id.
func (a *App) findAgent(id string) (agent.Agent, bool) {
	for _, ag := range a.cfg.Agents {
		if ag.ID == id {
			return ag, true
		}
	}
	return agent.Agent{}, false
}

// buildSpec resolves the agent to a termpane Spec and injects claude's
// --session-id (for a fresh conversation) or --resume (for a resume) when
// agentSessionID is provided. Other agents get their command untouched.
func (a *App) buildSpec(ag agent.Agent, wtPath, branch, agentSessionID string, resume bool) (termpane.Spec, error) {
	spec, err := agent.Resolve(ag, agent.TemplateVars{WorktreePath: wtPath, Branch: branch, RepoRoot: a.repoRoot})
	if err != nil {
		return termpane.Spec{}, err
	}
	cmd := append([]string(nil), spec.Command...)
	if ag.ID == "claude" && agentSessionID != "" {
		if resume {
			cmd = append(cmd, "--resume", agentSessionID)
		} else {
			cmd = append(cmd, "--session-id", agentSessionID)
		}
	}
	return termpane.Spec{
		Command: cmd, Env: spec.Env, Cwd: spec.Cwd,
		Cols: 80, Rows: 24,
	}, nil
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
		agentSessionID := ""
		if ag.ID == "claude" {
			agentSessionID = session.NewAgentSessionID()
		}
		tspec, err := a.buildSpec(ag, wtPath, branch, agentSessionID, false)
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		id := session.NewID()
		prog := a.prog
		tspec.OnDirty = func() {
			if prog != nil {
				prog.Send(termpane.RefreshMsg{ID: id})
			}
		}
		h, err := termpane.Start(ctx, tspec)
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		m := termpane.NewModel(id, h)
		s := &session.Session{
			ID: id, AgentID: ag.ID, AgentSessionID: agentSessionID,
			RepoRoot: repo, WorktreePath: wtPath,
			Branch: branch, PID: h.PID(), Status: session.StatusRunning,
			StartedAt: time.Now(), LastActivity: time.Now(),
		}
		return sessionCreatedMsg{s: s, m: m}
	}
}

// attachTarget resolves which (worktree, branch) an empty-submit should attach
// to: the currently-highlighted session if one is selected, else the worktree
// grove was launched from.
func (a *App) attachTarget() (wtPath, branch string, err error) {
	if s := a.currentSession(); s != nil {
		return s.WorktreePath, s.Branch, nil
	}
	// No session selected — fall back to the launch cwd's worktree.
	if a.launchCwd == "" {
		return "", "", fmt.Errorf("no worktree to attach to — enter a branch name to create one")
	}
	ctx := context.Background()
	g := gitx.New(a.launchCwd)
	wt, err := g.RepoRoot(ctx)
	if err != nil {
		return "", "", fmt.Errorf("can't resolve current worktree: %w", err)
	}
	br, err := g.CurrentBranch(ctx)
	if err != nil {
		return "", "", fmt.Errorf("can't read current branch: %w", err)
	}
	return wt, br, nil
}

// attachAgent spawns a new agent session inside an *existing* worktree at
// wtPath (current branch = branch). No `git worktree add` is performed.
// Multiple sessions sharing one worktree see the same files; race protection
// is the user's problem.
func (a *App) attachAgent(wtPath, branch string) tea.Cmd {
	if len(a.cfg.Agents) == 0 {
		return func() tea.Msg { return sessionCreatedMsg{err: fmt.Errorf("no agents configured")} }
	}
	ag := a.cfg.Agents[0]
	repo := a.repoRoot
	return func() tea.Msg {
		ctx := context.Background()
		agentSessionID := ""
		if ag.ID == "claude" {
			agentSessionID = session.NewAgentSessionID()
		}
		tspec, err := a.buildSpec(ag, wtPath, branch, agentSessionID, false)
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		id := session.NewID()
		prog := a.prog
		tspec.OnDirty = func() {
			if prog != nil {
				prog.Send(termpane.RefreshMsg{ID: id})
			}
		}
		h, err := termpane.Start(ctx, tspec)
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		m := termpane.NewModel(id, h)
		s := &session.Session{
			ID: id, AgentID: ag.ID, AgentSessionID: agentSessionID,
			RepoRoot: repo, WorktreePath: wtPath,
			Branch: branch, PID: h.PID(), Status: session.StatusRunning,
			StartedAt: time.Now(), LastActivity: time.Now(),
		}
		return sessionCreatedMsg{s: s, m: m}
	}
}

// resumeSession re-spawns an exited session's agent. For claude with a
// stored AgentSessionID, this runs `claude --resume <uuid>` so the prior
// conversation continues. The refreshed session re-uses the original grove
// ID so it replaces the existing entry (via Add's map overwrite) instead
// of adding a duplicate row to the list.
func (a *App) resumeSession(s *session.Session) tea.Cmd {
	if s == nil {
		return nil
	}
	if s.Status == session.StatusRunning && a.terms[s.ID] != nil {
		a.tab = tabTerm
		a.focus = focusActive
		return nil
	}
	ag, ok := a.findAgent(s.AgentID)
	if !ok {
		a.status = "unknown agent: " + s.AgentID
		return nil
	}
	snap := *s
	return func() tea.Msg {
		ctx := context.Background()
		agentSessionID := snap.AgentSessionID
		resume := true
		// Legacy sessions from before AgentSessionID existed — start a fresh
		// claude conversation rather than fail.
		if ag.ID == "claude" && agentSessionID == "" {
			agentSessionID = session.NewAgentSessionID()
			resume = false
		}
		tspec, err := a.buildSpec(ag, snap.WorktreePath, snap.Branch, agentSessionID, resume)
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		id := snap.ID
		prog := a.prog
		tspec.OnDirty = func() {
			if prog != nil {
				prog.Send(termpane.RefreshMsg{ID: id})
			}
		}
		h, err := termpane.Start(ctx, tspec)
		if err != nil {
			return sessionCreatedMsg{err: err}
		}
		m := termpane.NewModel(id, h)
		newS := snap
		newS.AgentSessionID = agentSessionID
		newS.PID = h.PID()
		newS.Status = session.StatusRunning
		newS.LastActivity = time.Now()
		return sessionCreatedMsg{s: &newS, m: m}
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
	listW := a.sessionsW()
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
	header := styleHeader.Render("grove") + "  " + styleHint.Render(filepath.Base(a.repoRoot))
	a.list.SetFocused(a.focus == focusSessions)
	list := a.list.View()
	body := a.renderBody()
	hint := styleHint.Render(a.hintText())
	status := styleStatus.Render(a.status)

	listW := a.sessionsW()
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
	prefix := focusPrefix(a.focus == focusActive)
	var parts []string
	for _, l := range labels {
		if l.t == a.tab {
			parts = append(parts, styleTabOn.Render(prefix+l.text))
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
	labelActive := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	labelDim := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	termFocused := a.focus == focusActive && a.tab == tabTerm
	diffFocused := a.focus == focusActive && a.tab == tabDiff
	pick := func(focused bool) lipgloss.Style {
		if focused {
			return labelActive
		}
		return labelDim
	}
	left := lipgloss.NewStyle().Width(a.termW).Render(pick(termFocused).Render(focusPrefix(termFocused) + "Terminal"))
	right := lipgloss.NewStyle().Width(a.diffW).Render(pick(diffFocused).Render(focusPrefix(diffFocused) + "Diff"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
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
