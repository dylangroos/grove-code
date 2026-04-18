// Package termpane is the embedded PTY + virtual terminal pane.
//
// One instance per session. The emulator implementation is hidden behind the
// Handle interface so a v0.2 supervisor-daemon client can replace it without
// touching the UI.
package termpane

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	uv "github.com/charmbracelet/ultraviolet"
)

// Spec is what the caller needs to know to launch a session.
type Spec struct {
	Command []string
	Env     []string // extra KEY=VALUE entries
	Cwd     string
	Cols    int
	Rows    int
}

// Handle is the transport-agnostic session handle. v0.1 = in-process; v0.2 can
// replace with a daemon client that speaks the same methods.
type Handle interface {
	PID() int
	Resize(cols, rows int) error
	SendKey(k uv.KeyEvent)
	SendMouse(m uv.MouseEvent)
	SendText(s string)
	Render() string
	IsAltScreen() bool
	Wait() error
	Kill() error
	Close() error
}

// Start spawns the command in a PTY and wires a vt emulator.
func Start(ctx context.Context, spec Spec) (Handle, error) {
	if spec.Cols <= 0 {
		spec.Cols = 80
	}
	if spec.Rows <= 0 {
		spec.Rows = 24
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(spec.Cols), Rows: uint16(spec.Rows)})
	if err != nil {
		return nil, fmt.Errorf("pty.Start: %w", err)
	}

	emu := vt.NewSafeEmulator(spec.Cols, spec.Rows)

	h := &handle{
		cmd:  cmd,
		ptmx: ptmx,
		emu:  emu,
		done: make(chan struct{}),
	}
	// Pipe child output → emulator.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = emu.Write(buf[:n])
				h.markDirty()
			}
			if err != nil {
				h.markDone(err)
				return
			}
		}
	}()
	// Pipe encoded user input (from SendKey/SendMouse/SendText) → pty.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return h, nil
}

type handle struct {
	cmd  *exec.Cmd
	ptmx *os.File
	emu  *vt.SafeEmulator

	mu      sync.Mutex
	dirty   bool
	exitErr error
	done    chan struct{}
	closed  bool
}

func (h *handle) markDirty() {
	h.mu.Lock()
	h.dirty = true
	h.mu.Unlock()
}

// TakeDirty returns true at most once per "run of dirtiness" since the last call.
func (h *handle) TakeDirty() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dirty {
		h.dirty = false
		return true
	}
	return false
}

func (h *handle) markDone(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.exitErr = err
	close(h.done)
}

func (h *handle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *handle) Resize(cols, rows int) error {
	if err := pty.Setsize(h.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return err
	}
	h.emu.Resize(cols, rows)
	h.markDirty()
	return nil
}

func (h *handle) SendKey(k uv.KeyEvent)     { h.emu.SendKey(k) }
func (h *handle) SendMouse(m uv.MouseEvent) { h.emu.SendMouse(m) }
func (h *handle) SendText(s string)         { h.emu.SendText(s) }
func (h *handle) Render() string            { return h.emu.Render() }
func (h *handle) IsAltScreen() bool         { return h.emu.IsAltScreen() }

func (h *handle) Wait() error {
	<-h.done
	waitErr := h.cmd.Wait()
	if waitErr != nil {
		return waitErr
	}
	if h.exitErr != nil && h.exitErr != io.EOF {
		return h.exitErr
	}
	return nil
}

func (h *handle) Kill() error {
	if h.cmd.Process != nil {
		return h.cmd.Process.Kill()
	}
	return nil
}

func (h *handle) Close() error {
	_ = h.ptmx.Close()
	return nil
}

// --- Bubble Tea Model ---

// RefreshMsg is emitted by the periodic tick to trigger re-render when the
// underlying terminal has new output. It carries the pane's id so the root
// model can target the right session.
type RefreshMsg struct {
	ID string
}

// ExitedMsg is sent when the child process exits.
type ExitedMsg struct {
	ID  string
	Err error
}

// Model is a Bubble Tea model wrapping a single Handle.
type Model struct {
	ID     string
	h      Handle
	w, h_  int
	focus  bool
}

func NewModel(id string, h Handle) *Model {
	return &Model{ID: id, h: h, focus: true}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.tick(), m.waitExit())
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg {
		return RefreshMsg{ID: m.ID}
	})
}

func (m *Model) waitExit() tea.Cmd {
	return func() tea.Msg {
		err := m.h.Wait()
		return ExitedMsg{ID: m.ID, Err: err}
	}
}

// SetSize resizes the terminal.
func (m *Model) SetSize(w, h int) {
	if w == m.w && h == m.h_ {
		return
	}
	m.w, m.h_ = w, h
	_ = m.h.Resize(w, h)
}

// Focus toggles input forwarding.
func (m *Model) Focus()  { m.focus = true }
func (m *Model) Blur()   { m.focus = false }
func (m *Model) Focused() bool { return m.focus }

// Handle exposes the underlying handle (for kill, etc.).
func (m *Model) Handle() Handle { return m.h }

func (m *Model) Update(msg tea.Msg) (*Model, tea.Cmd) {
	switch msg := msg.(type) {
	case RefreshMsg:
		if msg.ID == m.ID {
			return m, m.tick()
		}
	case tea.KeyPressMsg:
		if !m.focus {
			return m, nil
		}
		m.h.SendKey(uv.KeyPressEvent(uv.Key(msg)))
		return m, nil
	case tea.PasteMsg:
		if !m.focus {
			return m, nil
		}
		m.h.SendText(msg.Content)
		return m, nil
	case tea.MouseClickMsg:
		if !m.focus {
			return m, nil
		}
		m.h.SendMouse(uv.MouseClickEvent(uv.Mouse(msg.Mouse())))
		return m, nil
	case tea.MouseReleaseMsg:
		if !m.focus {
			return m, nil
		}
		m.h.SendMouse(uv.MouseReleaseEvent(uv.Mouse(msg.Mouse())))
		return m, nil
	case tea.MouseWheelMsg:
		if !m.focus {
			return m, nil
		}
		// On primary screen, we could consume the wheel for our own scrollback;
		// on alt-screen, forward it. v0.1: always forward (simplest correct behavior).
		m.h.SendMouse(uv.MouseWheelEvent(uv.Mouse(msg.Mouse())))
		return m, nil
	case tea.MouseMotionMsg:
		if !m.focus {
			return m, nil
		}
		m.h.SendMouse(uv.MouseMotionEvent(uv.Mouse(msg.Mouse())))
		return m, nil
	}
	return m, nil
}

// View returns the rendered terminal contents.
func (m *Model) View() string {
	return m.h.Render()
}
