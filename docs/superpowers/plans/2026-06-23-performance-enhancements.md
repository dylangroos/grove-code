# Performance Enhancements + Arrow-Key Column Navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate TUI lag caused by an agent-output-driven re-render storm and redundant diff work, and add left/right arrow navigation between columns in grove mode.

**Architecture:** Targeted hot-path fixes to the existing Bubble Tea TUI, no pipeline rewrite. Cap the terminal pane's dirty-coalescing rate, skip re-rendering diffs whose raw bytes are unchanged (content-hash compare), hoist chroma style setup out of a per-line loop, gate the poll tick on having a selected session, and add a `moveColumn` helper driven by left/right arrows.

**Tech Stack:** Go 1.25, charm.land/bubbletea v2, charm.land/bubbles v2 (viewport), alecthomas/chroma v2, bluekeyes/go-gitdiff, hash/fnv (stdlib).

## Global Constraints

- Go 1.25+ (per `go.mod` `go 1.25` directive — no `.0` patch suffix).
- Shell out to the `git` binary via `internal/gitx`; do not add a git library.
- All model state mutation happens on the Bubble Tea Update goroutine; `tea.Cmd`
  closures (e.g. `diffpane.Refresh`) capture values, they do not mutate the model.
- Run `go build ./...` and `go test ./...` from the repo root.

---

### Task 1: Cap terminal dirty-coalescing rate

**Files:**
- Modify: `internal/ui/termpane/termpane.go` (notifier goroutine, ~line 82-89)

**Interfaces:**
- Consumes: nothing.
- Produces: package const `dirtyCoalesce time.Duration`.

The notifier goroutine currently sleeps `2 * time.Millisecond` between dirty
signals, so a streaming agent triggers hundreds of full-app re-renders per
second. Raise it to 16ms (~60fps) via a named constant. This is the largest
felt improvement; it has no unit-testable assertion, so verify by build + manual
observation.

- [ ] **Step 1: Add the constant**

Near the top of `internal/ui/termpane/termpane.go`, after the imports, add:

```go
// dirtyCoalesce caps how often pty output triggers a re-render. ~60fps: a
// streaming agent can no longer flood the Update loop with RefreshMsg.
const dirtyCoalesce = 16 * time.Millisecond
```

- [ ] **Step 2: Use the constant in the notifier goroutine**

In `Start`, change the notifier goroutine's sleep from:

```go
		for range h.dirty {
			if spec.OnDirty != nil {
				spec.OnDirty()
			}
			time.Sleep(2 * time.Millisecond)
		}
```

to:

```go
		for range h.dirty {
			if spec.OnDirty != nil {
				spec.OnDirty()
			}
			time.Sleep(dirtyCoalesce)
		}
```

- [ ] **Step 3: Build and run existing tests**

Run: `go build ./... && go test ./internal/ui/termpane/...`
Expected: build succeeds, existing termpane tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/termpane/termpane.go
git commit -m "perf(termpane): cap dirty-coalescing at 16ms (~60fps)"
```

---

### Task 2: Hoist chroma style out of the per-line highlight loop

**Files:**
- Modify: `internal/ui/diffpane/diffpane.go` (`highlight`, ~line 173-188; var block ~line 22-27)

**Interfaces:**
- Consumes: nothing.
- Produces: package var `diffStyle *chroma.Style`.

`highlight()` calls `styles.Get("monokai")` on every line of every diff render.
Move it to a package-level var computed once. Behavior is identical (same style),
so this is verified by build + existing render output.

- [ ] **Step 1: Add the package-level style var**

In `internal/ui/diffpane/diffpane.go`, add to the existing `var (...)` block (the
one starting at ~line 22 with `styleAdd`):

```go
	// diffStyle is resolved once; styles.Get is a map lookup we don't want per line.
	diffStyle = styles.Get("monokai")
```

- [ ] **Step 2: Use it in highlight()**

In `highlight`, change:

```go
	var buf bytes.Buffer
	style := styles.Get("monokai")
	if err := formatters.TTY256.Format(&buf, style, it); err != nil {
		return line + "\n"
	}
```

to:

```go
	var buf bytes.Buffer
	if err := formatters.TTY256.Format(&buf, diffStyle, it); err != nil {
		return line + "\n"
	}
```

- [ ] **Step 3: Build and run existing tests**

Run: `go build ./... && go test ./internal/ui/diffpane/...`
Expected: build succeeds; tests PASS (or "no test files" if none exist yet).

- [ ] **Step 4: Commit**

```bash
git add internal/ui/diffpane/diffpane.go
git commit -m "perf(diffpane): hoist chroma style lookup out of per-line loop"
```

---

### Task 3: Skip re-rendering unchanged diffs

**Files:**
- Modify: `internal/ui/diffpane/diffpane.go` (`Model`, `LoadedMsg`, `SetRepoRoot`, `Refresh`, `Update`; add `decideLoad` + `fnvHash`)
- Test: `internal/ui/diffpane/diffpane_test.go` (create)

**Interfaces:**
- Consumes: `gitx.New(root).DiffWorktree(ctx) ([]byte, error)`.
- Produces:
  - `LoadedMsg` gains `hash uint64` and `unchanged bool` fields (unexported, same-package).
  - `func decideLoad(raw []byte, prevHash uint64) LoadedMsg`
  - `func fnvHash(b []byte) uint64`
  - `Model` gains `lastHash uint64`.

The 2s poll re-parses and re-highlights the diff even when it has not changed.
Compute an FNV-64a hash of the raw `git diff` bytes; if it matches the last
rendered hash, return a no-op message and skip parse/highlight/`vp.SetContent`.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/diffpane/diffpane_test.go`:

```go
package diffpane

import "testing"

func TestDecideLoad_UnchangedRawIsSkipped(t *testing.T) {
	// First load of an empty (clean) diff renders the placeholder and reports a hash.
	first := decideLoad([]byte{}, 0)
	if first.unchanged {
		t.Fatal("first load must not be marked unchanged")
	}
	if first.hash == 0 {
		t.Fatal("first load must report a non-zero hash")
	}
	if first.content != "(no uncommitted changes)" {
		t.Fatalf("unexpected content: %q", first.content)
	}

	// Re-loading identical raw bytes against the prior hash is a no-op.
	second := decideLoad([]byte{}, first.hash)
	if !second.unchanged {
		t.Fatal("identical raw bytes must be marked unchanged")
	}

	// Different raw bytes against the prior hash are NOT unchanged.
	third := decideLoad([]byte("something different"), first.hash)
	if third.unchanged {
		t.Fatal("different raw bytes must not be marked unchanged")
	}
}

func TestUpdate_UnchangedPreservesContent(t *testing.T) {
	m := New()
	m, _ = m.Update(LoadedMsg{content: "hello", hash: 42})
	if m.content != "hello" {
		t.Fatalf("expected content set, got %q", m.content)
	}
	m, _ = m.Update(LoadedMsg{unchanged: true})
	if m.content != "hello" {
		t.Fatalf("unchanged msg must preserve content, got %q", m.content)
	}
	m, _ = m.Update(LoadedMsg{content: "world", hash: 43})
	if m.content != "world" {
		t.Fatalf("new content must replace, got %q", m.content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/diffpane/ -run 'TestDecideLoad|TestUpdate_Unchanged' -v`
Expected: FAIL to compile — `decideLoad` undefined, `LoadedMsg` has no `hash`/`unchanged` fields.

- [ ] **Step 3: Extend `LoadedMsg` and `Model`**

In `internal/ui/diffpane/diffpane.go`, change `LoadedMsg`:

```go
type LoadedMsg struct {
	content   string
	err       error
	hash      uint64
	unchanged bool
}
```

Add `lastHash uint64` to the `Model` struct (alongside `content string`):

```go
type Model struct {
	vp       viewport.Model
	repoRoot string
	content  string
	lastHash uint64
	err      error
	w, h     int
}
```

- [ ] **Step 4: Add `fnvHash` and `decideLoad`, and reset hash on repo change**

Add `"hash/fnv"` to the import block. Add these functions (place near `Refresh`):

```go
func fnvHash(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

// decideLoad turns raw `git diff` bytes into a LoadedMsg, skipping the parse +
// highlight work when the bytes are unchanged from the last render (prevHash).
func decideLoad(raw []byte, prevHash uint64) LoadedMsg {
	h := fnvHash(raw)
	if h == prevHash {
		return LoadedMsg{unchanged: true}
	}
	if len(raw) == 0 {
		return LoadedMsg{content: "(no uncommitted changes)", hash: h}
	}
	files, _, err := gitdiff.Parse(bytes.NewReader(raw))
	if err != nil {
		return LoadedMsg{err: err}
	}
	return LoadedMsg{content: Render(files), hash: h}
}
```

Reset the cached hash when the session (repo root) changes so a new session
always renders fresh. Change `SetRepoRoot`:

```go
func (m *Model) SetRepoRoot(p string) {
	m.repoRoot = p
	m.lastHash = 0
}
```

- [ ] **Step 5: Rewire `Refresh` and `Update` to use them**

Change `Refresh` to capture the prior hash and delegate to `decideLoad`:

```go
func (m *Model) Refresh() tea.Cmd {
	root := m.repoRoot
	prevHash := m.lastHash
	return func() tea.Msg {
		if root == "" {
			return LoadedMsg{content: "(no session selected)"}
		}
		g := gitx.New(root)
		raw, err := g.DiffWorktree(context.Background())
		if err != nil {
			return LoadedMsg{err: err}
		}
		return decideLoad(raw, prevHash)
	}
}
```

Change the `LoadedMsg` case in `Update` to honor `unchanged` and store the hash:

```go
	case LoadedMsg:
		if msg.unchanged {
			return m, nil
		}
		m.err = msg.err
		m.content = msg.content
		m.lastHash = msg.hash
		m.vp.SetContent(m.content)
		return m, nil
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/ui/diffpane/ -run 'TestDecideLoad|TestUpdate_Unchanged' -v`
Expected: PASS.

- [ ] **Step 7: Build everything**

Run: `go build ./... && go test ./internal/ui/diffpane/...`
Expected: build succeeds, all diffpane tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/diffpane/diffpane.go internal/ui/diffpane/diffpane_test.go
git commit -m "perf(diffpane): skip re-render when raw diff is unchanged"
```

---

### Task 4: Gate the poll tick on a selected session

**Files:**
- Modify: `internal/app/app.go` (`pollMsg` case ~line 144; add `pollTarget` + `tabNone`)
- Test: `internal/app/app_test.go` (create)

**Interfaces:**
- Consumes: `a.currentSession() *session.Session`, `a.layout`, `a.focus`, `a.tab`.
- Produces: `const tabNone tab = -1`; `func (a *App) pollTarget() tab`.

When no session is selected there is nothing to diff, yet the poll still shells
out `git diff` every 2s. Factor the poll decision into a testable `pollTarget`
helper that returns which pane to refresh (or `tabNone`), guarded by
`currentSession() != nil`.

- [ ] **Step 1: Write the failing test**

Create `internal/app/app_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestPollTarget -v`
Expected: FAIL to compile — `pollTarget` and `tabNone` undefined.

- [ ] **Step 3: Add `tabNone` and `pollTarget`**

In `internal/app/app.go`, add `tabNone` to the `tab` const block:

```go
const (
	tabTerm tab = iota
	tabDiff
	tabLog
)

const tabNone tab = -1
```

Add the helper (place near the `pollMsg` handling or with the other `App` methods):

```go
// pollTarget reports which pane the 2s poll tick should refresh, or tabNone.
// No selected session means nothing to diff, so we skip the git shell-out.
func (a *App) pollTarget() tab {
	if a.currentSession() == nil {
		return tabNone
	}
	if a.layout == layoutSplit {
		return tabDiff
	}
	if a.focus == focusActive && (a.tab == tabDiff || a.tab == tabLog) {
		return a.tab
	}
	return tabNone
}
```

- [ ] **Step 4: Use `pollTarget` in the `pollMsg` case**

Replace the `case pollMsg:` body (currently lines ~144-157):

```go
	case pollMsg:
		cmds := []tea.Cmd{tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollMsg{} })}
		switch a.pollTarget() {
		case tabDiff:
			cmds = append(cmds, a.diff.Refresh())
		case tabLog:
			cmds = append(cmds, a.log.Refresh())
		}
		return a, tea.Batch(cmds...)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/ -run TestPollTarget -v`
Expected: PASS (all four).

- [ ] **Step 6: Build everything**

Run: `go build ./... && go test ./internal/app/...`
Expected: build succeeds, tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "perf(app): skip poll-driven diff refresh when no session is selected"
```

---

### Task 5: Left/right arrow column navigation

**Files:**
- Modify: `internal/app/app.go` (`handleNormalKey` ~line 273; add `moveColumn`)
- Test: `internal/app/app_test.go` (extend the file created in Task 4)

**Interfaces:**
- Consumes: `a.layout`, `a.focus`, `a.tab`, `focusSessions`, `focusActive`,
  `layoutSplit`, `layoutTabbed`, `tabTerm`, `tabDiff`, `a.diff.Refresh()`.
- Produces: `func (a *App) moveColumn(delta int) tea.Cmd`.

In grove mode (terminal not focused, so `handleNormalKey` runs), `left`/`right`
move focus horizontally. Columns are `[list, active-pane]` in tabbed layout and
`[list, terminal, diff]` in split layout. `moveColumn` computes the current
column index, clamps `index+delta`, and applies the resulting `(focus, tab)`.
Landing on the diff column triggers a refresh.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/app_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestMoveColumn -v`
Expected: FAIL to compile — `moveColumn` undefined.

- [ ] **Step 3: Implement `moveColumn`**

In `internal/app/app.go`, add:

```go
// moveColumn moves horizontal focus by delta (-1 left, +1 right). Columns are
// [list, active-pane] in tabbed layout and [list, terminal, diff] in split.
// Landing on the diff column refreshes it.
func (a *App) moveColumn(delta int) tea.Cmd {
	maxIdx := 1
	if a.layout == layoutSplit {
		maxIdx = 2
	}

	// Current column index.
	idx := 0 // list
	if a.focus == focusActive {
		idx = 1 // terminal / active pane
		if a.layout == layoutSplit && a.tab == tabDiff {
			idx = 2 // diff
		}
	}

	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx > maxIdx {
		idx = maxIdx
	}

	switch idx {
	case 0:
		a.focus = focusSessions
		return nil
	case 1:
		a.focus = focusActive
		if a.layout == layoutSplit {
			a.tab = tabTerm
		}
		return nil
	default: // 2, split only
		a.focus = focusActive
		a.tab = tabDiff
		return a.diff.Refresh()
	}
}
```

- [ ] **Step 4: Wire arrow keys into `handleNormalKey`**

In `handleNormalKey`, add these cases (e.g. after the `"enter"` case):

```go
	case "left":
		return a.moveColumn(-1)
	case "right":
		return a.moveColumn(+1)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/ -run TestMoveColumn -v`
Expected: PASS (both).

- [ ] **Step 6: Build and run the full test suite**

Run: `go build ./... && go test ./...`
Expected: build succeeds, all packages PASS.

- [ ] **Step 7: Update the hint text (discoverability)**

In `hintText()` (`internal/app/app.go` ~line 662), append `←/→ cols` to the
two non-terminal-focused hint strings so users can discover the binding. Change:

```go
	case a.focus == focusActive:
		return "j/k scroll  1/2/3 tab  ctrl+g → sessions"
```

to:

```go
	case a.focus == focusActive:
		return "j/k scroll  ←/→ cols  1/2/3 tab  ctrl+g → sessions"
```

and in the `default:` branch append `  ←/→ cols` to both the split and tabbed
hint strings (before `ctrl+g → terminal`).

- [ ] **Step 8: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go
git commit -m "feat(app): left/right arrow navigation between columns in grove mode"
```

---

## Self-Review Notes

- **Spec coverage:** Task 1 ↔ coalesce; Task 2 ↔ chroma hoist; Task 3 ↔ unchanged-diff skip; Task 4 ↔ poll gating; Task 5 ↔ arrow navigation. All five spec items mapped.
- **Type consistency:** `LoadedMsg` fields (`content`, `err`, `hash`, `unchanged`) are used identically in Tasks 2-3; `decideLoad`/`fnvHash`/`lastHash` names match across steps; `pollTarget`/`tabNone` and `moveColumn` signatures match between definition and tests.
- **Ordering:** Tasks are independently shippable; recommended order is 1→5 as numbered.
