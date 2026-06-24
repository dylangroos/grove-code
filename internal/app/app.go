// Package app wires the panes and session registry into a Bubble Tea root model.
package app

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/dylangroos/grove-code/internal/agent"
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

	reg    *session.Registry
	terms  map[string]*termpane.Model // sessionID -> pane
	active string                     // sessionID

	list  sessionlist.Model
	diff  diffpane.Model
	log   logpane.Model
	input textinput.Model

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
		terms:     map[string]*termpane.Model{},
		list:      sessionlist.New(),
		diff:      diffpane.New(),
		log:       logpane.New(),
		input:     ti,
		tab:       tabTerm,
		focus:     focusSessions,
		mode:      modeNormal,
		layout:    layoutFromConfig(cfg),
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
