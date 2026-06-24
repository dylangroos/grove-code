package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dylangroos/grove-code/internal/agent"
	"github.com/dylangroos/grove-code/internal/gh"
	"github.com/dylangroos/grove-code/internal/gitx"
	"github.com/dylangroos/grove-code/internal/session"
	"github.com/dylangroos/grove-code/internal/ui/termpane"
)

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
