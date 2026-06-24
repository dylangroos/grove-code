package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dylangroos/grove-code/internal/agent"
	"github.com/dylangroos/grove-code/internal/session"
)

// newTestApp builds an App with an empty config and an in-memory registry,
// sized to a sane window so relayout math is well-defined. No PTYs or git
// side effects are involved.
func newTestApp(t *testing.T) *App {
	t.Helper()
	a := New(&agent.File{}, "/repo", session.NewRegistry(), "")
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return a
}

// key builds a KeyPressMsg whose String() matches what the Update loop matches
// on. Verified mapping: plain runes carry Text; special/modified keys use Code.
func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+g":
		return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	default:
		r := []rune(s)[0]
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

// isQuitCmd reports whether running cmd yields a tea.QuitMsg.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestWindowSizeMsgSetsDimensions(t *testing.T) {
	a := New(&agent.File{}, "/repo", session.NewRegistry(), "")
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if a.w != 120 || a.h != 40 {
		t.Fatalf("w,h = %d,%d; want 120,40", a.w, a.h)
	}
}

func TestStatusMsgSetsStatus(t *testing.T) {
	a := newTestApp(t)
	a.Update(statusMsg{text: "hello"})
	if a.status != "hello" {
		t.Fatalf("status = %q; want %q", a.status, "hello")
	}
}

func TestNumberKeySwitchesTabAndFocus(t *testing.T) {
	a := newTestApp(t)
	a.Update(key("2"))
	if a.tab != tabDiff {
		t.Fatalf("tab = %d; want tabDiff (%d)", a.tab, tabDiff)
	}
	if a.focus != focusActive {
		t.Fatalf("focus = %d; want focusActive (%d)", a.focus, focusActive)
	}
}

func TestKeyNEntersNewBranchMode(t *testing.T) {
	a := newTestApp(t)
	a.Update(key("n"))
	if a.mode != modeNewBranch {
		t.Fatalf("mode = %d; want modeNewBranch (%d)", a.mode, modeNewBranch)
	}
}

func TestEscCancelsNewBranchMode(t *testing.T) {
	a := newTestApp(t)
	a.Update(key("n"))
	a.Update(key("esc"))
	if a.mode != modeNormal {
		t.Fatalf("mode = %d; want modeNormal (%d)", a.mode, modeNormal)
	}
}

func TestKeySTogglesLayout(t *testing.T) {
	a := newTestApp(t)
	if a.layout != layoutTabbed {
		t.Fatalf("initial layout = %d; want layoutTabbed (%d)", a.layout, layoutTabbed)
	}
	a.Update(key("s"))
	if a.layout != layoutSplit {
		t.Fatalf("after one toggle layout = %d; want layoutSplit (%d)", a.layout, layoutSplit)
	}
	a.Update(key("s"))
	if a.layout != layoutTabbed {
		t.Fatalf("after two toggles layout = %d; want layoutTabbed (%d)", a.layout, layoutTabbed)
	}
}

func TestCtrlGTogglesFocus(t *testing.T) {
	a := newTestApp(t)
	if a.focus != focusSessions {
		t.Fatalf("initial focus = %d; want focusSessions (%d)", a.focus, focusSessions)
	}
	a.Update(key("ctrl+g"))
	if a.focus != focusActive {
		t.Fatalf("focus = %d; want focusActive (%d)", a.focus, focusActive)
	}
}

func TestBeginQuitWithNoSessionsQuitsImmediately(t *testing.T) {
	a := newTestApp(t)
	_, cmd := a.Update(key("q"))
	if !isQuitCmd(cmd) {
		t.Fatal("expected quit command with no sessions")
	}
	if a.mode != modeNormal {
		t.Fatalf("mode = %d; want modeNormal (%d)", a.mode, modeNormal)
	}
}

func TestBeginQuitWithSessionsAsksConfirmation(t *testing.T) {
	a := newTestApp(t)
	a.reg.Add(&session.Session{ID: "s1", Status: session.StatusRunning})
	_, cmd := a.Update(key("q"))
	if isQuitCmd(cmd) {
		t.Fatal("should not quit immediately while a session is running")
	}
	if a.mode != modeConfirmQuit {
		t.Fatalf("mode = %d; want modeConfirmQuit (%d)", a.mode, modeConfirmQuit)
	}
}

func TestConfirmQuitCancelReturnsToNormal(t *testing.T) {
	a := newTestApp(t)
	a.reg.Add(&session.Session{ID: "s1", Status: session.StatusRunning})
	a.Update(key("q")) // enter confirm mode
	a.Update(key("n")) // cancel
	if a.mode != modeNormal {
		t.Fatalf("mode = %d; want modeNormal (%d)", a.mode, modeNormal)
	}
}

func TestConfirmQuitYesQuits(t *testing.T) {
	a := newTestApp(t)
	a.reg.Add(&session.Session{ID: "s1", Status: session.StatusRunning})
	a.Update(key("q")) // enter confirm mode
	_, cmd := a.Update(key("y"))
	if !isQuitCmd(cmd) {
		t.Fatal("expected quit command after confirming")
	}
}
