package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

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
