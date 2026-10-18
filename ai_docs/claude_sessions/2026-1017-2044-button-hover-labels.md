# Button hover labels

Session: `95462966-d55d-43e2-9d8b-a270c962a228`

## Ask

From the cats-todo backlog: "Are tooltip popups possible in ced?" Then:
"add hover labels to the buttons — as many as are practical (esp the red
close-the-editor ✕)".

## Answer to the question

Yes — ced already had four passive popups on dwell timers: LSP hover on
dwell (hoverdwell.go, Tier 1 only), the diagnostic tip (diagtip.go), the
change-bar popup (hunktip.go) and the overflow-marker tip (overflow.go).
They share `tooltipLayout` / `tooltipPlace` / `drawTooltipBox`
(hovermodal.go), and ced enables all-motion mouse reporting (mode 1003,
app.go). Nothing labelled the chrome buttons.

## What was built

`internal/app/buttontip.go` (+ `buttontip_test.go`, 15 tests):

- **One enumerator**, `buttonTips()`, lists every labelled button with
  the rect its click router already hit-tests (btnRect methods, the
  stamped `lastTabRects` and `statusSegs`). Arm, tick and draw all ask
  it; draw re-resolves, so a vanished button takes its label with it and
  toggles (`Aa`: on/off) track state under a resting pointer.
- **Key twin** held as the verb (`keyFn`) and resolved lazily by
  `leaderKeysFor` — a code-pointer match on `leaderBindings()`, one
  namespace deep (`Esc a c`). Closures never match. `keys` literal only
  for non-leader doors (Alt chords in the find bar, `Esc Esc ·
  right-click` for ≡).
- **Passive**: never claims an event; closes on press, key, resize,
  `closeAllModals`. **Anchored to the button**: motion inside the same
  button keeps an open label / pending clock. 500ms delay
  (`buttonTipDelay`), every host.
- **Modal rule**: silent under the ≡ menu and every modal except the
  unpinned Find-all list (its ⟳ ◇ ◨ are the most cryptic glyphs). Hence
  `noteButtonTipPointer` runs BEFORE handleMouse's modal dispatch and
  `drawButtonTip` is the last call in `draw`, after the overlay layer.
- `statusSegment` gained a `tip` field; every clickable segment has one
  (⧉'s names the absolute path it copies).

Coverage: tab bar ✕ (counts unsaved tabs), ≡, `+N`, per-tab chip / × /
marker / body (relative path); status bar segments; find bar toggles,
history labels, Replace, All; Find-all ⟳ ◇ ◨ ✕ + replace button; tree ⭑
and generic-header ✕; git panel, git log (+ search-mode chips),
problems (+ chips), compare, terminal, chat (+ attachment ✕), Conflicts
op buttons (a disabled Continue shows its `why`).

Wiring: `app.go` (state field, event case, pointer hook, resize/key
dismissals, draw call), `modals.go` (closeAllModals), `statusbar.go`.
Docs: README "### Button labels" after Overflow markers; CLAUDE.md map
line + a "Button labels" design-rule section (new clickable buttons get
a row in the enumerator).

## Verification

- `make test` (race) green; `go vet` clean.
- Real binary via the run-ced PTY driver: hid the tree (`Esc t`), sent
  SGR motion reports (`{esc}[<35;2;1M`) onto ✕ — the label
  "Quit ced — asks first / Esc q" drew under the button.
- Not yet tried through tmux / cats / macOS Terminal (N-073).

## Gotchas hit

- The simulation screen's `GetContents` only reflects a frame after
  `Show()` — test helper calls it after `draw`.
- `statusSegs` is stamped right side FIRST, so its last element is not
  the corner ≡.
- `openFile` can leave a flash that owns the status bar's left side,
  hiding ⧉ — the fixture clears `statusMsg`.
- `closeButtonTip` bumps `seq` even when nothing was armed (it must
  invalidate pending ticks), so "armed nothing" is asserted via
  `buttonTip.armed`, not `seq`.

## Next

Closed: None. Declined: None. Raised: N-073, N-074.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
