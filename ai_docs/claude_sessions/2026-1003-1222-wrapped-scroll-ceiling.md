# Session: wrapped-scroll-ceiling

Session ID: 3bb58a00-2321-491f-8764-59ec4fff65bd
Date: 2026-10-03

Follows `2026-1003-1204-jump-margin` (same session, second ask), then
cuts patch release 0.4.2.

## 1. The ask

"fix N-045" — raised in the jump-margin doc:

> `Tab.MaxScroll` is LINE-indexed even when the tab soft-wraps. A
> wrapped file with few but long lines gets MaxScroll 0, so clampScroll
> pulls ScrollY back to 0 after EnsureVisible scrolled — the caret sits
> off screen and the last rows are unreachable.

## 2. Root cause

`MaxScroll(viewH) = LineCount - viewH + overscroll` (overscroll =
max(viewH/2, 3)). Soft wrap keeps ScrollY a LINE index but costs several
rows per line. Ten 50-rune lines = 30 rows in a 20-row pane:
`ensureVisibleWrapped` correctly sets ScrollY 2 for a caret on line 8,
then `clampScroll` caps at 10-20+10 = 0 and the caret (row 24) is drawn
nowhere.

## 3. What was done

- `internal/editor/tab.go` `MaxScroll`: when `t.wrapW > 0` (Render sets
  it before it clamps) → `maxScrollWrapped(t.wrapW, viewH-overscroll)`.
  Doc comment explains why.
- `internal/editor/softwrap.go` `maxScrollWrapped(width, keep)`: walk up
  from the last line summing `lineRows` until `keep` rows are counted;
  that line is the ceiling; fewer rows than `keep` → 0. `keep` floored at
  1. Cost is one screenful of layouts. With one row per line it equals
  the unwrapped formula exactly (same overscroll feel).
  - Only behavioural difference from unwrapped: a 1–2 row pane can't
    scroll the last line off the top in wrap mode (unwrapped formula
    allows it; nothing relies on it).
- Tests (all three FAIL on the old code, verified by stashing the fix):
  - `softwrap_test.go` `TestSoftWrap_MaxScrollCountsRows` (6 for the
    10×3-row file; 190 for 200 one-row lines; 0 for a short file).
  - `softwrap_test.go` `TestSoftWrap_CaretBelowLineCeilingStaysOnScreen`
    (the bug).
  - `jumpmargin_test.go` `TestJumpMargin_WrappedShortFile` (margin on the
    small wrapped file → ScrollY 4).
- CLAUDE.md soft-wrap section: the ceiling counts rows when wrapped.
- `make test` (race) green.

## 4. Release

Patch release **0.4.2** (past latest tag v0.4.1): `version.go` and
`cats-plugin.toml` bumped together, `Release ced 0.4.2` on main, tag
`v0.4.2` pushed. Ships the jump margin (`2026-1003-1204-jump-margin`)
and this fix.

## Next

Closed: N-045. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
