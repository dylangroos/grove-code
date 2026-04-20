package session

import (
	"testing"
	"time"
)

// Multiple sessions can share a single WorktreePath (multi-agent-on-same-worktree).
// Registry must keep them as distinct entries keyed by ID.
func TestRegistryAcceptsDuplicateWorktreePath(t *testing.T) {
	r := NewRegistry()
	wt := "/tmp/some/worktree"
	s1 := &Session{ID: "aaaa", WorktreePath: wt, Branch: "feat-a", StartedAt: time.Now()}
	s2 := &Session{ID: "bbbb", WorktreePath: wt, Branch: "feat-a", StartedAt: time.Now().Add(time.Second)}

	r.Add(s1)
	r.Add(s2)

	if got := r.Get("aaaa"); got == nil || got.ID != "aaaa" {
		t.Fatalf("Get(aaaa) = %+v, want session aaaa", got)
	}
	if got := r.Get("bbbb"); got == nil || got.ID != "bbbb" {
		t.Fatalf("Get(bbbb) = %+v, want session bbbb", got)
	}
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	for _, s := range all {
		if s.WorktreePath != wt {
			t.Fatalf("All() returned session with WorktreePath %q, want %q", s.WorktreePath, wt)
		}
	}

	// Removing one must leave the other.
	r.Remove("aaaa")
	if got := r.Get("aaaa"); got != nil {
		t.Fatalf("after Remove(aaaa), Get(aaaa) = %+v, want nil", got)
	}
	if got := r.Get("bbbb"); got == nil {
		t.Fatal("after Remove(aaaa), Get(bbbb) returned nil — siblings should not be affected")
	}
}
