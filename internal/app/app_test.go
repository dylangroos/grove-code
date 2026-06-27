package app

import (
	"testing"
	"time"

	"github.com/dylangroos/grove-code/internal/session"
)

func newTestApp() *App {
	return New(nil, "/repo", session.NewRegistry(), "")
}

func addSelectedSession(a *App) {
	s := &session.Session{
		ID: "s1", WorktreePath: "/repo", Branch: "b",
		Status: session.StatusRunning, StartedAt: time.Now(),
	}
	a.reg.Add(s)
	a.active = "s1"
}

func TestPollTarget_NoSession(t *testing.T) {
	a := newTestApp()
	a.layout = layoutSplit
	if got := a.pollTarget(); got != tabNone {
		t.Fatalf("no session must poll nothing, got %d", got)
	}
}

func TestPollTarget_SplitRefreshesDiff(t *testing.T) {
	a := newTestApp()
	addSelectedSession(a)
	a.layout = layoutSplit
	if got := a.pollTarget(); got != tabDiff {
		t.Fatalf("split layout must refresh diff, got %d", got)
	}
}

func TestPollTarget_TabbedDiffFocused(t *testing.T) {
	a := newTestApp()
	addSelectedSession(a)
	a.layout = layoutTabbed
	a.focus = focusActive
	a.tab = tabDiff
	if got := a.pollTarget(); got != tabDiff {
		t.Fatalf("focused diff tab must refresh diff, got %d", got)
	}
}

func TestPollTarget_TabbedTerminalFocusedSkips(t *testing.T) {
	a := newTestApp()
	addSelectedSession(a)
	a.layout = layoutTabbed
	a.focus = focusActive
	a.tab = tabTerm
	if got := a.pollTarget(); got != tabNone {
		t.Fatalf("terminal tab must poll nothing, got %d", got)
	}
}

func TestPollTarget_TabbedLogFocused(t *testing.T) {
	a := newTestApp()
	addSelectedSession(a)
	a.layout = layoutTabbed
	a.focus = focusActive
	a.tab = tabLog
	if got := a.pollTarget(); got != tabLog {
		t.Fatalf("focused log tab must refresh log, got %d", got)
	}
}

func TestMoveColumn_TabbedRightFocusesActiveLeftFocusesList(t *testing.T) {
	a := newTestApp()
	a.layout = layoutTabbed
	a.focus = focusSessions

	a.moveColumn(+1)
	if a.focus != focusActive {
		t.Fatal("right from list must focus the active pane")
	}
	a.moveColumn(+1) // clamp: no third column in tabbed
	if a.focus != focusActive {
		t.Fatal("right past the last column must stay on the active pane")
	}
	a.moveColumn(-1)
	if a.focus != focusSessions {
		t.Fatal("left must return focus to the list")
	}
	a.moveColumn(-1) // clamp: already leftmost
	if a.focus != focusSessions {
		t.Fatal("left past the first column must stay on the list")
	}
}

func TestMoveColumn_SplitCyclesListTerminalDiff(t *testing.T) {
	a := newTestApp()
	a.layout = layoutSplit
	a.focus = focusSessions
	a.tab = tabTerm

	a.moveColumn(+1) // -> terminal
	if a.focus != focusActive || a.tab != tabTerm {
		t.Fatalf("right from list must focus terminal, got focus=%d tab=%d", a.focus, a.tab)
	}
	a.moveColumn(+1) // -> diff
	if a.focus != focusActive || a.tab != tabDiff {
		t.Fatalf("right from terminal must focus diff, got focus=%d tab=%d", a.focus, a.tab)
	}
	a.moveColumn(+1) // clamp at diff
	if a.focus != focusActive || a.tab != tabDiff {
		t.Fatal("right past diff must stay on diff")
	}
	a.moveColumn(-1) // -> terminal
	if a.focus != focusActive || a.tab != tabTerm {
		t.Fatalf("left from diff must focus terminal, got focus=%d tab=%d", a.focus, a.tab)
	}
	a.moveColumn(-1) // -> list
	if a.focus != focusSessions {
		t.Fatal("left from terminal must focus the list")
	}
}
