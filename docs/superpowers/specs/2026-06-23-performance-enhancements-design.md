# Performance Enhancements + Arrow-Key Column Navigation

Date: 2026-06-23
Branch: grove/new/feature

## Problem

Users report lag across the whole TUI: typing/general UI, the diff pane, session
switching, and startup.

### Root cause

Bubble Tea calls `View()` after every `Update`, and `App.render()`
(`internal/app/app.go:619`) rebuilds the entire UI on each call: the session
list, the diff viewport, and the full terminal emulator render
(`termpane.Handle.Render()`).

The terminal pane signals "dirty" with only **2ms** coalescing
(`internal/ui/termpane/termpane.go:87`). A streaming agent therefore fires
`RefreshMsg` hundreds of times per second, and each one triggers a full-app
re-render. This single mechanism explains the lag in typing, session switching,
and the diff pane simultaneously.

Two amplifiers sit on top:

1. The diff is fully re-parsed and re-highlighted on every 2s poll
   (`internal/app/app.go:144`) even when the diff content has not changed.
2. `highlight()` looks up `styles.Get("monokai")` **per line** inside its loop
   (`internal/ui/diffpane/diffpane.go:183`), repeating a map lookup for every
   line of every diff render.

## Goals

- Eliminate the agent-output-driven re-render storm.
- Stop redundant diff re-parsing/re-highlighting.
- Add left/right arrow navigation between columns in grove mode.

## Non-goals

- No architectural change to the render pipeline (no full per-pane dirty-flag
  render cache). Revisit only if lag persists after these changes.
- No cross-session highlighted-diff cache keyed by content hash. The unchanged
  skip (below) covers the common case.

## Approach

Targeted hot-path fixes, no architecture change. Four independently shippable
changes, ranked by leverage, plus the navigation feature.

### 1. Coalesce the re-render storm (biggest win)

In `internal/ui/termpane/termpane.go`, raise the dirty-notifier coalescing sleep
from `2 * time.Millisecond` to `16 * time.Millisecond` (~60fps cap). A busy
agent can no longer trigger hundreds of full-app renders per second; renders are
capped at roughly one per frame.

- The notifier goroutine (lines 82-89) still coalesces via the buffered `dirty`
  channel; only the sleep duration changes.
- Define the interval as a named constant (e.g. `dirtyCoalesce = 16 * time.Millisecond`)
  for clarity.

### 2. Skip unchanged diffs

In `internal/ui/diffpane/diffpane.go`, avoid re-parsing/re-highlighting when the
raw `git diff` output is identical to the last render.

- Store a hash (e.g. FNV-64 or SHA-256) of the last raw diff bytes on the
  `Model` (set when a `LoadedMsg` is applied in `Update`).
- `Refresh` runs `git diff` as today, then compares the new raw bytes' hash to
  the stored one. If equal, it returns a no-op message (`LoadedMsg{unchanged: true}`)
  and `Update` does nothing (no `vp.SetContent`).
- Because `Refresh` is a `tea.Cmd` (runs in a goroutine) and the hash lives on
  the model, the hash to compare against must be captured into the closure at
  `Refresh()` call time (like `root` already is), and the new hash is stored
  when the resulting `LoadedMsg` is applied in `Update`. This keeps all model
  mutation on the Update goroutine.

### 3. Hoist chroma setup

In `internal/ui/diffpane/diffpane.go`, move the monokai style lookup and the
TTY256 formatter reference to package-level vars computed once, instead of
`styles.Get("monokai")` inside the per-line `highlight()` loop.

```go
var diffStyle = styles.Get("monokai") // once, at package init
```

`highlight()` then uses `diffStyle` directly.

### 4. Gate polling

In `internal/app/app.go` `pollMsg` handling (line 144), skip the diff/log
`Refresh()` work when there is no session selected (nothing to diff). The
existing focus/tab/layout gating stays; this adds a "no active session → skip"
guard. Combined with change #2, an idle or unchanged session does no diff work
on the poll tick.

### 5. Feature: left/right arrow column navigation

In grove mode (after ctrl-g — i.e. when the terminal pane is NOT focused, so the
existing `handleNormalKey` path runs), `left`/`right` move focus horizontally.
Added to `handleNormalKey` (`internal/app/app.go:273`) so the agent PTY never
loses keystrokes.

Define an ordered list of "columns" for the current layout and move an index:

- **Tabbed layout:** columns are `[list, active-pane]`.
  - `left` → `focusSessions`.
  - `right` → `focusActive` (keeps current tab).
- **Split layout:** columns are `[list, terminal, diff]`.
  - Stepping right: list → terminal (`focusActive`, `tab=tabTerm`) → diff
    (`focusActive`, `tab=tabDiff`).
  - Stepping left reverses.
  - Routing already works: `focusActive`+`tabDiff` routes keys to the diff pane,
    `focusActive`+`tabTerm` routes to the terminal (`internal/app/app.go:230`).

`up`/`down` keep their current meaning (move session selection). `ctrl+g`
continues to toggle focus as before. Arrows are ignored while the terminal pane
is focused and typing (the `terminalFocused` guard at `internal/app/app.go:221`
already routes those keys to the PTY, so `handleNormalKey` is not reached).

## Components touched

- `internal/ui/termpane/termpane.go` — coalesce interval constant.
- `internal/ui/diffpane/diffpane.go` — unchanged-diff hash skip; chroma hoist.
- `internal/app/app.go` — poll gating; arrow-key column navigation.

## Error handling

- Diff hashing failures are impossible (hashing raw bytes); the unchanged path
  only triggers on a successful, non-empty diff that matches the prior hash.
  Errors and empty/clean states bypass the skip and render as today.
- No new external calls; no new failure modes introduced.

## Testing

- `internal/ui/diffpane`: unit test that applying two `LoadedMsg`s built from
  identical raw diff bytes results in the second `Refresh` reporting unchanged
  (no viewport content reset). Verify a changed diff does re-render.
- `internal/app`: table test for arrow routing — `left`/`right` produce the
  expected `(focus, tab)` in both tabbed and split layouts, and arrows are
  ignored when the terminal is focused/typing.
- Coalesce interval and chroma hoist: covered by existing behavior plus manual
  verification (run an agent that streams output; confirm UI stays responsive).

## Rollout

All changes are local to the TUI and independently revertable. Order of
implementation: (1) coalesce, (2) unchanged-diff skip, (3) chroma hoist,
(4) poll gating, (5) arrow navigation.
