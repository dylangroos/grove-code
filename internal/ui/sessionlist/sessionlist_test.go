package sessionlist

import (
	"strings"
	"testing"
	"time"

	"github.com/dylangroos/grove-code/internal/session"
)

// stripANSI removes ANSI escape sequences so assertions can focus on glyphs.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b { // ESC
			// skip "\x1b[...letter" CSI sequences
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !((s[j] >= '@' && s[j] <= '~')) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j - 1
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestSetItemsGroupsByWorktreePath(t *testing.T) {
	// Intentionally interleaved input order to exercise the sort.
	wtA := "/tmp/wt/a"
	wtB := "/tmp/wt/b"
	t0 := time.Now()
	items := []*session.Session{
		{ID: "b1", WorktreePath: wtB, Branch: "feat-b", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0.Add(2 * time.Second)},
		{ID: "a2", WorktreePath: wtA, Branch: "feat-a", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0.Add(1 * time.Second)},
		{ID: "a1", WorktreePath: wtA, Branch: "feat-a", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0},
		{ID: "b2", WorktreePath: wtB, Branch: "feat-b", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0.Add(3 * time.Second)},
	}
	m := New()
	m.SetSize(40, 20)
	m.SetItems(items)

	// After sort: a1, a2 (both wtA, chronological), then b1, b2 (both wtB).
	want := []string{"a1", "a2", "b1", "b2"}
	for i, id := range want {
		if m.items[i].ID != id {
			t.Fatalf("sorted[%d].ID = %q, want %q; full order: %v", i, m.items[i].ID, id, idsOf(m.items))
		}
	}
}

func TestViewRendersSiblingTreeChar(t *testing.T) {
	wt := "/tmp/wt/shared"
	t0 := time.Now()
	m := New()
	m.SetSize(40, 20)
	m.SetItems([]*session.Session{
		{ID: "first", WorktreePath: wt, Branch: "feat-a", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0},
		{ID: "second", WorktreePath: wt, Branch: "feat-a", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0.Add(time.Second)},
	})
	plain := stripANSI(m.View())
	lines := strings.Split(plain, "\n")

	// First session line should NOT have the tree-char prefix; second should.
	// Find the two session rows (after header + divider).
	var first, second string
	n := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" || strings.HasPrefix(strings.TrimSpace(ln), "Sessions") || strings.HasPrefix(strings.TrimSpace(ln), "─") {
			continue
		}
		if n == 0 {
			first = ln
		} else if n == 1 {
			second = ln
		}
		n++
	}
	if strings.Contains(first, "└─") {
		t.Fatalf("first line should not have sibling prefix, got: %q", first)
	}
	if !strings.Contains(second, "└─") {
		t.Fatalf("second line (sibling) should have └─ prefix, got: %q", second)
	}
}

func TestViewNoGroupingForDistinctPaths(t *testing.T) {
	t0 := time.Now()
	m := New()
	m.SetSize(40, 20)
	m.SetItems([]*session.Session{
		{ID: "a", WorktreePath: "/tmp/a", Branch: "feat-a", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0},
		{ID: "b", WorktreePath: "/tmp/b", Branch: "feat-b", AgentID: "grove", Status: session.StatusRunning, StartedAt: t0.Add(time.Second)},
	})
	plain := stripANSI(m.View())
	if strings.Contains(plain, "└─") {
		t.Fatalf("no sibling grouping expected, got view:\n%s", plain)
	}
}

func idsOf(ss []*session.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}
