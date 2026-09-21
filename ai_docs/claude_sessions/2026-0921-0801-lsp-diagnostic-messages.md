# Session: LSP diagnostic messages

Session ID: `a8e736d5-4537-439e-81db-34a63af31322`
Date: 2026-09-21
Commit: `adf9b60` — Show LSP diagnostic messages: gutter/underline tooltip, Esc-i, step flash

## Ask

> If there is an LSP error I do see a red dot in the gutter, but I also need
> to somehow see the error message.

Before this session the only place a diagnostic's text was visible was the
Problems panel (status-bar `✗ N` click / ≡). The gutter dot and underline
said *that* a line was broken, never *what*. The LSP hover-dwell tooltip
(hoverdwell.go) exists but is Tier-1 (cats) only and only asks for symbol
docs.

## What was built

Three doors onto one text (`diagTipLines`):

1. **Pointer tooltip** (new `internal/app/diagtip.go`). Rest the pointer on
   a diagnosed line's gutter (any gutter column of the line whose diagnostic
   STARTS there — the dot's line) or on an underlined rune → after 250ms a
   passive tooltip lists the messages: worst severity first, `✗ ⚠ ℹ` glyphs
   (the status bar / Problems vocabulary), `(source)` appended, word-wrapped
   via `wrapChatText` to the tooltip width, capped at 12 rows with
   "… (see the Problems panel)".
   - Modelled on the **overflow popup** (overflow.go), not the LSP dwell:
     reads the `a.lsp.diags` cache, so no round trip, runs on every host,
     and `armDiagTip` schedules a tick only if the cell actually has a
     diagnostic.
   - `diagsAtCell` refuses cells not really ON a rune (PosScreenCell
     round-trip, hoverdwell's rule), so air past EOL doesn't answer.
     Gutter = `lx < AnnotationCols().end + 1`.
   - `diagsCovering` gives zero-width ranges the underline's one-cell
     stretch.
   - Lifecycle: `noteDiagPointer` in handleMouse beside notePointer /
     noteOverflowPointer; `diagTipEvent` case in handleEvent; `drawDiagTip`
     on the passive layer after drawOverflowTip; `closeDiagTip` beside every
     `closeHoverDwell` (resize, keystroke, closeAllModals).
   - When open, the Tier-1 hover dwell stands down (`handleHoverDwellTick`
     returns early; `handleDiagTipTick` closes the dwell).
2. **Esc-i** (`handleLSPHover` in lsp.go) now prepends `diagLinesAtCaret()`
   — diagnostics covering the caret, else any starting on the caret's line
   (so column 0 after a Problems jump works) — above the hover text, and
   shows them alone when the server has no hover text. Keyboard path for
   terminals with no motion reporting.
3. **Next/previous problem** (`stepProblem`) flashes `diagFlashText` of the
   row landed on.

## Files

- `internal/app/diagtip.go` (new), `internal/app/diagtip_test.go` (new)
- `internal/app/app.go` — `diagTip` field, event case, mouse hook, draw,
  dismissals
- `internal/app/hoverdwell.go`, `lsp.go`, `problems.go`, `modals.go`
- `CLAUDE.md` — architecture-map row + "Diagnostic messages" house-rules
  section (before "Go to symbol in file")

## Verification

- `go test -race ./...` green.
- New tests: `TestDiagTipLines`, `TestDiagsAtCell`, `TestDiagTip_Lifecycle`,
  `TestHoverInfo_LeadsWithDiagnostics`, `TestStepProblem_FlashesMessage`.
- **Not verified in a real terminal** — the mouse-rest tooltip was only
  exercised through the simulation screen.
- Pushed to `main`; CI does not run on this fork (Actions not enabled).

## Next

- Try the pointer tooltip in a real terminal (plain tmux, cats, macOS
  Terminal.app) to confirm motion events reach it and the 250ms delay feels
  right; `run-ced` skill can drive it.
- Consider a click on the gutter dot opening the tooltip immediately (today a
  gutter click just moves the caret to column 0) — useful where motion
  reporting is unavailable.
- Consider showing the caret line's diagnostic in the status bar as the
  caret moves (vim/ALE "echo cursor" style) — an ambient, zero-gesture door;
  not done because the status bar is width-budgeted.
