# 2026-10-08 18:03 — Diagnostic note off switch (N-037) + next-list run

Session: `797e751b-0955-45cf-ac31-53f1db6a6e5c`

## Request

1. `/next-list` — living-list pass over `ai_docs/todo/next-list.md`.
2. From the cats-todo backlog, next-list item **N-037**: the caret-line
   diagnostic note (`diagnote.go`) has no off switch. Related nit: on a
   host that reports motion, a click on the underline shows both the
   note and the pointer tooltip (the release arrives as a motion report
   on the same cell and arms the dwell).

## Next-list run

Living-list mode, 138 session docs. No lapses (N-001…N-058 all present,
IDs unique, Next ID N-059 correct; every Raised/Closed in the last 15
docs' summaries reconciled). Every Open premise re-checked against the
code — all still hold. Text corrected in place:

- **N-039** — tab-menu row counts refreshed (Restore and Copy to… each
  added a row: 14 outside cats … 18 on a grouped markdown tab, 20 with
  the border; falls off below ~22 rows).
- **N-054** — besides "twenty-nine" derived keys (33 now), the
  "thirty-five" totals in palette.go / builtin.go / palette_test.go /
  load_test.go are stale too (41 = 8 core + 33). Counted from
  `derivations()`.
- **N-013** — `~/.cargo/bin/rust-analyzer` is rustup's proxy with the
  component missing ("Unknown binary"); `rustup component add
  rust-analyzer` would make it the cheapest server to check next, and
  ced's look-up finding a dying binary is worth confirming degrades
  silently.

Suggested to the user (not acted on): N-005/N-006 (stale cats-side
decisions) as Non-goals candidates; N-009 to Roadmap beside N-001.

## What changed (N-037)

- `internal/userconfig/userconfig.go` — `DiagNote bool` (default on),
  `"diagnote"` key parsed on/off with a typo reported, `SaveDiagNote`
  (saveKey round-trip). `TestDiagNoteKey` mirrors `TestInlayHintsKey`.
- `internal/app/app.go` — `App.diagNoteOff`, held INVERTED so the zero
  value is the default (tests build App directly and keep the note);
  loaded from `cfg.DiagNote`; ≡ View row after the inlay-hints row
  (`menuToggleDiagNote`, `diagNoteToggleLabel`).
- `internal/app/diagnote.go` — `stampDiagCaretNote` stamps an EMPTY note
  while off (that is what clears the previous frame's note — no per-tab
  sweep); `setDiagNote` single write path (state, flash, config). Header
  gains an "off switch" section.
- `internal/app/diagtip.go` — the nit: while the note is ON, a press in
  `noteDiagPointer` stamps `diagTip.x/y`, so the release's same-cell
  motion report reads as a repeat and arms nothing. While OFF the stamp
  is skipped: the dwell is then the click's only answer.
- Tests: `TestDiagNote_OffSwitch` (paint gone / back, label, persisted
  off), `TestDiagNote_ClickReleaseArmsNoTooltipWhileOn` (on: release arms
  nothing, travel still arms; off: release arms). Menu pins re-pinned:
  174 group actions, 191 rows, height 197, dividers `[2, 5, 194]`
  (`TestMenuLayout_NoCustomActions`, `_WithCustomActions` 200,
  `TestMenuModalRect_Centered` / `_ClampsToWindowHeight` heights 197).
- `CLAUDE.md` — menu pin numbers; Diagnostics caret-line note bullet
  documents the switch and the release stamp.
- `README.md` — Diagnostics bullet names the ≡ View row / config key.

## Verification

`make test` (race) — all packages pass. Not driven in the real binary;
the simulation-screen tests cover the paint.

Pre-existing, untouched: gofmt flags `TestLoadTerminal`'s map alignment
in `internal/userconfig/userconfig_test.go`.

## Next

Closed: N-037. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-013, N-039, N-054. Full list: `ai_docs/todo/next-list.md`.
