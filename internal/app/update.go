package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dylangroos/grove-code/internal/session"
	"github.com/dylangroos/grove-code/internal/ui/diffpane"
	"github.com/dylangroos/grove-code/internal/ui/logpane"
	"github.com/dylangroos/grove-code/internal/ui/termpane"
)

// pollMsg: periodic refresh of diff/log while user is looking.
type pollMsg struct{}

type statusMsg struct{ text string }

type sessionCreatedMsg struct {
	s   *session.Session
	m   *termpane.Model
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
