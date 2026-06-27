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
