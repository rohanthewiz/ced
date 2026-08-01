# Session: Multi-caret editing + the matching word highlight

Session ID: 76442d6b-25ce-409e-9285-90050b81dd8b
Date: 2026-07-31

### Ask

> "Add multiline edit capability to CEd and quick matching word
> highlight for the word under the cursor"

Two features, and they turned out to share a scanner: "every other
instance of this word" is the highlight, and it's also what the
occurrence-based caret gestures need.

### Multi-caret: the shape that avoided a rewrite

The obvious model — replace `Tab.Cursor`/`Anchor` with a list of carets
— would have touched every feature that reads the cursor: find, ghost
text, hover, the nav history, line ops, the status bar, `EnsureVisible`.

Instead: **primary + secondaries.** `Cursor`/`Anchor` stay exactly what
they were; extras live in a normally-empty `Tab.Carets`. Nothing
downstream learned anything new.

The fan-out is the piece that makes it cheap. `applyAtCarets` swaps each
caret into `Cursor`/`Anchor`, runs the **original single-caret
primitive**, and reads the position back — so `InsertRune`, `Backspace`,
`Delete`, `InsertString`, `MoveCursor` became thin wrappers over
unexported cores (`insertRuneAt`, `backspaceAt`, …). Two rules hold it
together:

- **Bottom-up.** Carets are visited in descending document order, so an
  edit only ever moves text *after* the carets still waiting. Top-down
  invalidates every position below the first edit.
- **One undo step.** The fan-out pushes one structural snapshot and sets
  `undoSuppress`, which `pushUndo` honours. Without it, undoing a
  five-caret keystroke took five presses.

A core must never call an exported sibling — that re-enters the fan-out
and visits every caret once per caret. That's why `deleteSelectionAt`
exists separately from `DeleteSelection`.

### What drops the carets, and why that's the load-bearing part

A stale caret editing the wrong line is the one failure mode this design
can't have. So:

- **Explicit jumps drop them**: `MoveCursorTo` (click, definition jump,
  nav history), `FocusCurrentMatch`, `SelectAll`, `Reload`, and
  `applySnapshot` (undo/redo — the buffer those positions were measured
  against is gone).
- **Arrows and Home/End move them all**, which is the opposite rule and
  the point: place carets, press `End`, type.
- **Whole-line gestures collapse the set** (`dropCaretsForLineOp` in
  DuplicateLines / MoveLines / ToggleLineComment). Two of them change
  line count or order. Fanning them out isn't the fix either — two
  carets on one line would duplicate it twice.

### Painted, not decorated

Secondary carets are drawn by `paintCarets`, called per row from
`Render`, not emitted as decoration spans. A caret is a **zero-width
position**: the one at end-of-line has no cell for a `Span` to cover,
and end-of-line is exactly where a column lands after `End`. Same
exception shape as ghost text, for a related reason. Their *selections*
are decorations, though — `selectionSource` now iterates `AllCarets`.

### Gestures

```
Esc m / Esc M   add a caret below / above the column (repeat)
Esc *           select this word, then claim the next occurrence (repeat)
Esc &           a caret on every occurrence in the file (no repeat)
Alt+click       toggle a caret at the pointer
Esc             back to one caret
```

`Alt+click` deliberately starts **no drag** — the press placed a caret,
and a stray mouse wiggle turning that into a selection would wipe the
whole set. Esc clears carets as a *side effect* (like the ghost and the
chat highlight): it must not consume the keystroke, or the double-Esc
menu and every leader would break from multi-caret mode.

`Esc &` was added at the end of the session, after the owner asked how
to get from "I can see the highlight" to "I have carets on all of them"
and the honest answer was "palette or ≡ menu". It's one shift-key over
from `*` on the same row, and it's the one multi-caret leader that
doesn't repeat — it already claimed every occurrence, so a second press
adds nothing and re-arming would swallow the next keystroke.

### The word highlight

A built-in `DecorationSource` running **first** among the built-ins, so
selection and find always paint over it.

- **Window-scoped**, unlike find. Find caches a match list against a
  query; the word under the cursor changes on every caret move, so
  there's nothing to cache and the scan runs inside the frame. Scanning
  `[firstLine, lastLine]` keeps the cost proportional to the screen. The
  tradeoff — a match scrolled off-screen doesn't light its on-screen
  twin — is deliberate.
- **Case-sensitive, whole-word from a bare cursor.** `Cursor` and
  `cursor` are two identifiers.
- **Quiet unless it has something to say**: a lone on-screen match, a
  caret in punctuation, a one-rune selection, and multi-caret mode all
  produce nothing.

`caretQuery` decides whole-word from what the **range is**, not from
which branch found it — a selection that exactly spans an identifier
matches whole-word. That fell out of a test failure: `Esc *`'s first
press turns the word into a selection, and without this rule the second
press quietly widened `count` to also mean `counter`.

### Two things the tests didn't catch

**The color was invisible.** I derived `word-highlight` as an 18% accent
wash "because it's ambient, so it should be quiet", verified it by
grepping the PTY capture for the hex, and shipped. The owner looked at
it on a real screen and couldn't find it. Turning it *up* wouldn't have
worked either: `selection` is also an accent wash, so any accent tint
strong enough to see reads as "I selected that". The fix is a **neutral**
box (26% fg over bg) plus **bold** on the span — neutral keeps the blue
fill exclusive to the selection, bold survives a terminal's contrast.
The palette test was rewritten to assert what actually matters:
≥90 channel-units off the background, and *more neutral* than the
selection (smaller channel spread — the two sit at similar brightness by
design, so channel distance was the wrong metric).

**The caret column drifted left.** Found by running the real binary, not
by a test: a column started at the end of a long line walked left every
time it crossed a short one, because `Clamp` pulled the caret back and
that shortened column seeded the next add. `caretGoalCol` takes the
*widest* caret's column instead, so the caret that wasn't clamped keeps
carrying the goal — no sticky-column field to keep in sync.

Both were caught by driving `./bin/ced` through the PTY capture tool
(`/run-ced`). Neither was reachable from a `SimulationScreen` assertion I
would plausibly have written.

### Files

```
internal/editor/multicaret.go   carets, the fan-out, occurrence gestures, paintCarets
internal/editor/wordhl.go       WordRange, MatchOccurrences, wordHighlightSource
internal/app/multicaret.go      ≡ rows, Esc-m/M/*/&, Alt+click, status suffix
internal/app/wordhl.go          ≡ toggle + the per-tab flag's single write path
```

Plus: the `word-highlight` theme key (derives, so every existing theme
file gains it), the `wordhl` config key, six new ≡ rows (five in Edit,
one in View), and the menu geometry pins moved to 92 rows / height 98 /
dividers `[2, 5, 95]`.

### Tests

Four new `_test.go` files. The ones worth keeping in mind: bottom-up
ordering proved with an edit that changes line count (two carets each
joining their line upward), the single-undo-step contract, the goal
column surviving a short line, both paint paths (mid-line and
end-of-line, which needs `scr.Show()` before `GetContents`), and the
precedence chain end-to-end — a selected word paints `Selection`, its
unselected twin paints `WordHL`.

### Not done (deliberate)

- **No column/block selection** (Alt+drag). Alt+click covers the same
  ground one caret at a time; a block drag needs its own drag mode and
  interacts with auto-scroll.
- **Line ops stay single-caret.** See above — collapsing is honest,
  fanning out is wrong.
- **No caret-aware ghost text or LSP.** Both read the primary, which is
  the right answer: a completion at five carets isn't a thing.
- **`Esc &` isn't repeatable** and the multi-caret rows don't get a
  namespace. The flat table had exactly enough letters left.

### Postscript: the carets didn't blink

Noticed by the owner after the doc above was written. The primary caret
is the terminal's *hardware* cursor and the terminal blinks it; the
secondaries are cells ced paints, so they sat there static and read as
highlights rather than as cursors.

The tempting fix is tcell's `AttrBlink` — free, no timer, the terminal
does the work. It's wrong here: SGR blink toggles the **glyph**, and a
caret past the end of a line paints a *space*, so exactly the carets a
column produces after `End` wouldn't blink at all. Inconsistent beats
nothing only when you can't see which case you're in.

So it's ced's own ticker, following the auto-scroll pattern: a
`caretBlinkEvent` posted every 530ms, the main loop toggling
`Tab.CaretsHidden`, and `Run`'s existing redraw doing the rest. Two
details that matter more than they look:

- **Armed only while carets exist.** The loop is `PollEvent → handle →
  draw → Show`, so a standing timer would wake an idle editor twice a
  second forever. The dispatch-tail hook (`caretBlinkAfterEvent`) arms
  and disarms it, so a future way of creating carets can't forget.
- **Stopping restores the on-phase.** Disarming mid-blink would
  otherwise strand a caret invisible until something else redrew.

Phase deliberately isn't reset on keystrokes: the hardware cursor blinks
to a clock we can't read, so the two are out of phase regardless, and
restarting the cycle on every keypress only makes the mismatch louder.
