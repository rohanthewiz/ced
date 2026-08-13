# Session: cats-native plan, Phase 3.3 — the tooltip nobody asked for

- Session id: `cd4e49f2-b63e-4b73-a307-23e5aef617c3`
- Date: 2026-08-13
- Branch: `main`
- Worked from the cats checkout (`~/projs/go/cats`); the ⌘E one-liner
  landed in cats, the feature in ced (`~/projs/go/ced`).
- Plan: `ai_docs/cats-native-plan.md` §Phase 3 — **3.3 now ✅, so Phase 3
  is closed and Phases 3, 4 and 5 are all done.**
- Predecessor: `2026-0813-1516-recent-files-picker.md`

## What was asked

Load the last session, see what remains in the cats-ced integration
plan, then: do the ⌘E one-liner in cats, then start 3.3 — hover on
mouse dwell. Both finished.

## What landed

**cats** — `ed4962c` (pushed):

```
cmd/catway/web/index.html    KeyE added to CMD_TO_PANE + why
```

No cats-mobile regen: this is the front end only, no wire change (unlike
`77285f9`, which had added `pane_modes.kitty`).

**ced** — the feature:

```
internal/app/hoverdwell.go      + test   the whole feature   (new, 13 tests)
internal/app/hovermodal.go               geometry + painter split out for sharing
internal/app/lsp.go                      lspHoverEvent gains the dwell half
internal/app/app.go                      field, event case, mouse hook, key + resize dismissal, draw
internal/app/modals.go                   closeAllModals takes the tooltip with it
internal/app/lsp_test.go                 the fake records WHICH position it was asked about
ai_docs/cats-native-plan.md              3.3 done + notes; §5 item 2, §7, critical files
```

`make test` (race) green; `go vet` clean; `gofmt` clean apart from the
four pre-existing offenders (`hostident_test.go`, `reconcile.go`,
`tabbar.go`, `editor/syntax.go`).

## The design

### (a) The feature is one LSP call. Everything else is not being in the way.

ced already had hover — `menuHoverInfo` asks about the CARET and opens a
modal. 3.3 is the same request asked about the POINTER, and essentially
all of the new code is refusals:

- **Where it will not ask.** The pointer must be ON an identifier rune.
  `Tab.HitTest` answers "which column is nearest", which is exactly
  right for a click (ten cells past the end of a line means "caret to
  the end") and exactly wrong for a pointer, which is over nothing and
  means nothing. Gutter, whitespace, punctuation, past-EOL and blank
  lines are all refused; so are mid-drag, mid-completion, under a menu,
  under a modal, in the find bar, and under the which-key band — each of
  those is a deliberate action in flight.
- **What it will not paint.** A superseded answer, an answer for a file
  the user has left, an error, an empty answer. The last one is the
  interesting one: `menuHoverInfo` flashes "No hover info" because a
  person asked and deserves a reply. Nobody asked here, and a status
  line that blinked whenever the mouse came to rest would be noise the
  user cannot switch off.

### (b) "On the rune" round-trips through the renderer

Rejecting the gutter needs to know where content starts, and
`gutterWidth` is unexported in `internal/editor`. Rather than export it
or re-derive it, `hoverDwellPos` asks the renderer where it PUT the
column it just derived (`PosScreenCell`) and accepts only if the pointer
is inside that cell's footprint — width taken from where the next column
landed, so a double-width glyph is two cells and a tab is however many
it is. Reusing the renderer's arithmetic is the only way to stay honest
about tab stops; duplicating it would have been a second source of truth
for where a column is.

### (c) Ambient, not modal — and the lifetime is the pointer

It does not take the modal slot (plan §6's rule, and the same argument
the Problems panel makes about diagnostics): the user asked no question,
so nothing here may own the keyboard or block the dialog they were about
to open. The keyboard flavour stays a modal because THAT one was asked
for.

The tooltip is anchored under the cell it describes and dies when the
pointer leaves that cell — the comparison is against the ANCHOR, not the
last-seen cell, because the tooltip's claim is "this is what is under
the pointer" and that stops being true immediately. Typing kills it too,
and cancels the request behind it. A press inside its box is swallowed
(the completion popup's contract): the box covers code, so a click on it
must not move the caret into text the user cannot see.

### (d) Staleness is one counter for two hazards

which-key's shape: one-shot `AfterFunc` per motion posts a tick, and the
goroutine never touches App state. The generation counter rides the LSP
request as well as the tick, so it invalidates both "the user moved on
before the timer fired" and "the user moved on before the server
answered" — the second being the one that would have painted a tooltip
about the wrong symbol while pointing at a new one. Every `close` bumps
it, which is why `closeAllModals` calling it is also what stops a late
answer painting over a modal that opened during the round trip.

### (e) Tier-1 gate, kept as specced

`hoverDwellArmed()` is `a.catsTier1()`. Inside cats, motion reporting is
precise and the round trip is a local socket. Tier 0 keeps the explicit
verb, so this is a degraded capability rather than a missing one — the
ladder's own rule. It is deliberately NOT `metaAccelArmed`'s
"Tier 1 or kitty host" predicate: that one is about the kitty KEYBOARD
protocol and has nothing to say about mouse motion. If it should light
up in bare kitty/Ghostty, that one function is the place.

### (f) One box, measured once

`hoverModal.rect`/`draw` were split into `tooltipSize`, `tooltipPlace`
and `drawTooltipBox`. Two hover surfaces disagreeing by a cell about
their own width would read as two different features. `tooltipPlace`
also carries the property the mouse flavour needs and the caret flavour
merely enjoyed: it never covers its own anchor, flipping above when the
window would clip it.

The one difference kept: the modal flavour hides the caret (it owns the
screen), the dwell flavour does not (the editor still has the keyboard
behind it, and a vanished caret would say otherwise).

## Verification

- `make test` (race) green, `go vet` clean.
- 13 new tests in `hoverdwell_test.go`. The load-bearing one is
  `TestHoverDwellAsksAboutThePointerNotTheCaret` — caret parked at 0:0,
  pointer on line 2, and the assertion is on the position that reached
  the WIRE (the fake now records it). The rest are the refusals, one per
  category, plus the two dismissal contracts and the stamped-rect clear.
- Tests locate screen cells through `PosScreenCell` rather than
  hard-coding a gutter width they have no business knowing; the
  refusal cases use raw offsets from `editorRect`, which is independent.
- **Not verified by machine:** the actual dwell, end to end, in a cats
  pane — it needs a mouse resting on a symbol with gopls live. The timer
  path is exercised by calling the tick directly, so what is untested is
  only "the host sends motion events the way we think" — the same class
  of claim 5.2 left to a hand-check.

## State / next steps

- **Phases 3, 4 and 5 are closed.** The whole roadmap now has two
  unclaimed items, both small: 4.6 (git blame, explicitly optional) and
  Phase 2's `⚠` tab marker re-raising a deferred conflict prompt.
- **Phase 6 is entirely upstream-gated, critical path `clipboard.read`**
  (§5 item 1) — the one ask with a whole feature behind it (§4 Tier 1 +
  native paste anywhere). Other asks unchanged: `theme_changed`,
  `pane.split` returning its pane and taking an argv, nothing reporting
  a pane child's exit. Item 6 (⌘click) stays retired as impossible.
- Still owed, neither of them code, and now a day old: the ⌘ chords end
  to end in a browser (the running catway is still the old binary), and
  the mac app, which analysis says is free of collisions. ⌘E joins that
  list.
- Fifth shape, named in the plan: **an unbidden surface is defined by
  its refusals** — and the ambient flavour of a verb must not inherit
  the modal flavour's failure behaviour.
