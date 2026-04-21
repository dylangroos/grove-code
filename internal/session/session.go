// Package session tracks running agent sessions + persistence.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/dylangroos/grove-code/internal/config"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusCrashed Status = "crashed"
)

// Session is the in-memory + persisted representation of one agent run.
type Session struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	AgentSessionID string    `json:"agent_session_id,omitempty"` // agent-owned UUID (e.g. claude --session-id); empty for agents without a resume model
	RepoRoot       string    `json:"repo_root"`
	WorktreePath   string    `json:"worktree_path"`
	Branch         string    `json:"branch"`
	PID            int       `json:"pid"`
	Status         Status    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
	LastActivity   time.Time `json:"last_activity"`
}

type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewRegistry() *Registry {
	return &Registry{sessions: map[string]*Session{}}
}

func (r *Registry) Add(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

func (r *Registry) Get(id string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

func (r *Registry) All() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// NewID returns a short random hex id.
func NewID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewAgentSessionID returns a random RFC 4122 v4 UUID. Claude's --session-id
// flag rejects anything that isn't a valid v4 UUID.
func NewAgentSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type persisted struct {
	Version  int        `json:"version"`
	Sessions []*Session `json:"sessions"`
}

// Save atomically writes the registry to state.json.
func (r *Registry) Save() error {
	if err := config.EnsureDir(config.StateDir()); err != nil {
		return err
	}
	r.mu.Lock()
	p := persisted{Version: 1, Sessions: make([]*Session, 0, len(r.sessions))}
	for _, s := range r.sessions {
		p.Sessions = append(p.Sessions, s)
	}
	r.mu.Unlock()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := config.StateFile()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads state.json and returns a registry. All persisted sessions are
// kept (including exited ones) so the user can resume them from history.
// Any process surviving from a prior grove is terminated — a fresh grove
// can't share a PTY with it, and concurrent claude writers to the same
// JSONL corrupt the transcript.
func Load() (*Registry, error) {
	r := NewRegistry()
	data, err := os.ReadFile(config.StateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}
	for _, s := range p.Sessions {
		if s.Status == StatusRunning && pidAlive(s.PID) {
			_ = killPid(s.PID)
		}
		s.Status = StatusExited
		r.sessions[s.ID] = s
	}
	return r, nil
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(nil) == nil
}

// killPid terminates an orphan process: SIGTERM, brief grace period, SIGKILL
// if it's still alive. Best-effort; failures are swallowed.
func killPid(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	_ = proc.Signal(syscall.SIGTERM)
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		if !pidAlive(pid) {
			return nil
		}
	}
	return proc.Signal(syscall.SIGKILL)
}

// WorktreePathFor returns the filesystem path for a new session's worktree.
func WorktreePathFor(root, repoRoot, branch string) string {
	hash := hashShort(repoRoot)
	safe := safeBranch(branch)
	return filepath.Join(config.ExpandHome(root), hash, safe)
}

func hashShort(s string) string {
	// Non-crypto short ID for dir-naming; collisions only within a user's repos.
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

func safeBranch(b string) string {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '/' || c == '\\' || c == ':' {
			out = append(out, '-')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
