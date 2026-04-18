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
	"time"

	"github.com/dgroos/grove-code/internal/config"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusCrashed Status = "crashed"
)

// Session is the in-memory + persisted representation of one agent run.
type Session struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	RepoRoot     string    `json:"repo_root"`
	WorktreePath string    `json:"worktree_path"`
	Branch       string    `json:"branch"`
	PID          int       `json:"pid"`
	Status       Status    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	LastActivity time.Time `json:"last_activity"`
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

// Load reads state.json and returns a registry. Sessions whose PID is no longer
// live are dropped (v0.1 does not reattach — that comes with the v0.2 daemon).
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
		if s.Status == StatusRunning && !pidAlive(s.PID) {
			s.Status = StatusExited
		}
		// Drop exited/crashed sessions on load — v0.1 doesn't show history.
		if s.Status != StatusRunning {
			continue
		}
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
