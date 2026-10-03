# Session: Change-bar popup (hover a git gutter bar → scrollable diff)

Session ID: `80520c18-55f0-485c-acce-11f1881898a8`
Date: 2026-10-03
(Same session as `2026-1003-1519-git-restore-file`.)

## Request

"When I hover over the change indicator bar can you show me a popup with
the changes (scrollable)."

## What was built

Rest the pointer on a git gutter bar (`▎` added/modified, `▁` removed),
or click it, and a popup shows that hunk's diff: removed lines red,
added lines green, title in the top border
(`Changed · lines 7–28  −10 +22`). The pointer can move into the box and
the wheel scrolls it; ▴/▾ on its right border and `a–b of n` in the
bottom border say there is more.

```
  41 ▎│ return parse(src)          ← pointer rests on the ▎
      ┌ Changed · lines 41–43  −1 +3 ──────────────┐
      │ -    return parseAll(src, opts)           ▴│
      │ +    return parse(src)                     │
      │ +    // opts moved to the caller          ▾│
      └───────────────────────────── 1–3 of 9 ┘
```

### Pieces

- **gitdiff.go** — `diffHunk` grew `Body []string` (the hunk's own
  `-`/`+`/`\` lines, verbatim) and `Top bool` (a deletion git reported at
  `+0,0`, which the clamped boundary line 0 can't express).
  `parseUnifiedDiff` collects body lines only after an ACCEPTED header,
  so the file header's `---`/`+++` and a malformed header's body are
  never mistaken for hunk text. The popup therefore costs no extra git
  run and always matches the bar.
- **hunktip.go** (new) — `hunkTipState` on App; `noteHunkTipPointer`
  (mouse hook), `armHunkTip` / `hunkTipEvent` / `handleHunkTipTick`
  (250ms dwell, `diagTipDelay`), `openHunkTipAt`, `hunkGutterPress`
  (click door), `hunkAtCell`, title/rows/style helpers,
  `scrollHunkTip` + `clampHunkTipScroll`, `drawHunkTip`, `clipRunes`.
- **app.go** — field, event dispatch, `noteHunkTipPointer` placed FIRST
  among the pointer hooks (before `notePointer`), `hunkGutterPress` right
  after `diagGutterPress`, close on key / resize, `drawHunkTip` after
  `drawDiagTip`. **modals.go** — `openModal` path closes it.

### Design decisions

- **Enterable passive popup.** Every other tooltip closes when the
  pointer leaves its cell; this one must survive the trip from the bar
  into the box or the wheel can't reach it. `tooltipPlace` hangs the box
  on the row below/above starting at the anchor column, so the trip
  never crosses a foreign cell. Motion inside the box is CLAIMED so the
  LSP dwell tooltip doesn't wake on the code beneath.
- **Wheel inside scrolls the box only** (even when it fits) — the editor
  scrolling under it would slide the bar away from its description.
  Wheel elsewhere closes it and falls through.
- **Target = the mark cell on a line's first screen row** (gutter is
  first-row-only under soft wrap; checked via `PosScreenCell`, not
  `ScrollY + row`). Refused when `diagsAtCell` is non-empty: the mark
  cell shows the dot by precedence, and the diagnostic tooltip owns it.
- **Code clipped with `…`, not wrapped; tabs → 4 cells.** Body capped at
  `hunkTipMaxRows` (16), width at `hunkTipMaxWidth` (100).
- **Not an overflow-marker surface** — it paints its own ▴/▾ (same
  glyphs) rather than joining `overflowMarkers()`, which enumerates
  docked viewports and would pop a tooltip on a tooltip.
- **No keyboard door** for the popup itself: a key-scrollable popup
  would have to own the keyboard. ≡ Git "Show file's uncommitted
  changes" is the keyboard path to the full diff.

## Verification

- `hunktip_test.go`: title per kind, rows (tabs/CR), clip + clamp,
  `hunkAtCell` refusals (code, line number, unchanged line, diagnostic
  dot), hover lifecycle (arm, stale tick, box placement, entering,
  wheel scroll + clamp, wheel outside), motion-off close, click toggle,
  painted output (title, colors, ▾/▴, position note), key + tab-switch
  hiding, real-git `loadFileDiff` bodies.
- `gitdiff_test.go`: new body test; position comparisons go through
  `hunkPos`/`hunkPosT` since `diffHunk` is no longer comparable.
- `make test` (race) passes.
- Real binary via run-ced capture with SGR motion/wheel reports: popup
  opened on hover, two wheel-downs moved `1–16 of 32` → `7–22 of 32`.
  (First attempt used `Printf` with no verbs — gopls diagnostics then
  owned the mark cells, which confirmed the precedence refusal.)

## Docs

- README: new Features bullet "Change bars" (the gutter wasn't
  documented at all before).
- CLAUDE.md: architecture map entry + "Change-bar popup" design-rule
  section.

## Other

- User said Co-Authored-By trailers are fine for personal projects;
  saved as a memory. ced's CLAUDE.md still forbids them — offered to
  reword, unanswered (N-046).

## Next

Closed: None. Declined: None. Raised: N-046.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
