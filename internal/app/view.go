package app

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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
	header := styleHeader.Render("grove") + "  " + styleHint.Render(filepath.Base(a.repoRoot))
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
