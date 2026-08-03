# Session: find verbs (replace, options, go to line) and the compare panel

Session ID: `b38d73e2-881f-45dc-b901-e73ee39bd9a2`
Date: 2026-08-03
Branch: `main`
Commits: `127bb67` … `c46bc7a` (3 commits, +3508 / −259 across 27 files)

---

## How this started

Stages 5 and 6 of [`ai_docs/opus-improvements-analysis.md`](../opus-improvements-analysis.md),
picked off the queue the previous session left:

| # | Stage | Items |
|---|---|---|
| 5 | Find verbs | replace, case/whole-word, go to line |
| 6 | Compare | file↔file, compare with pasted text |

Both landed. Two design calls went against what the plan had sketched;
both are written up as notes in the plan doc so the next reader finds
them there rather than here.

---

## Stage 5 — find verbs

`127bb67` · `internal/editor/find.go`, `internal/editor/replace.go`,
`internal/app/find.go`, `internal/app/goto.go`

### One scanner, three consumers

`FindOptions{CaseSensitive, WholeWord}` rides on the Tab beside the
query. Adding it meant the editor briefly had two answers to "is this a
hit?" — find's, and the word highlighter's case-sensitive whole-word
scanner from `wordhl.go`. They were merged into `matchCols`, which
`FindAllOpts`, `FindAll` and `MatchOccurrences` now all call. A test
asserts the two consumers agree on the same text, because the failure
mode of two implementations isn't a crash, it's the user getting
different answers to one question and having no way to tell which.

Case folding is **per rune** (`unicode.ToLower`), not `strings.ToLower`:

```
"İx foo"  ── strings.ToLower ──►  "i̇x foo"   (4 runes became 5)
                                    └── every column after it is now wrong
```

A handful of runes lowercase to more than one rune. A fold that changes
the length shifts every column the matcher reports after it, and the
highlight paints over the wrong cells. Per-rune folding is
length-preserving by construction.

### Where the options live

On the **App**, pushed onto tabs through `applyFindOptions`. They
describe how the *user* searches, so flipping "match case" and switching
files must not silently switch it back. `Tab.SetFindOptions` re-runs the
query, so a match list can never outlive the options the toggles are
showing.

Deliberately **not** persisted, unlike the Find-all dock. A saved "match
case" would quietly narrow the first search of every future session,
with no bar on screen to explain the hit that didn't appear.

### The bar grew a row

```
 Find: buffer                    Enter: next · … · Esc: close  1 of 7   Aa  |W|
 Repl: sink                                             Replace   All
```

Consequences worth remembering:

- **`findBarHeight` is no longer what layout code asks for.**
  `findBarRows()` (0/1/2) is, and every surface pinned above the status
  bar — both git panels, the terminal, `editorBandRows` — was moved onto
  it. A replace row that floated over the editor would cover the line
  it's about to rewrite.
- **Both inputs are the shared `textField`.** The hand-rolled
  value/cursor/scroll trio and `adjustFindScroll` that `find.go` carried
  since the fork are gone; the house rule already said every single-line
  input is a `textField`, and this file predated it.
- **Alt is safe inside the bar and nowhere else.** `handleFindKey` runs
  before `handleKey`'s Alt+rune leader branch, so `alt+c`, `alt+w` and
  `alt+a` can be chords here even though the leader table owns Alt
  everywhere else — including inside tmux, where "Esc c" arrives folded
  as Alt+c. Verified through the real PTY: `{esc}a` in the bar ran
  replace-all rather than arming the AI namespace.
- The toggles also get ≡ rows (a keyboard-owning surface makes the menu
  unreachable, which is the Find-all dock's argument inverted) and
  clickable `Aa` / `|W|` buttons, which needed a new `findBarPress` case
  in the mouse router — the bar sat in the editor's catch-all y-range, so
  a click on a toggle would otherwise have landed in the file behind it.

### Replace: one undo step, bottom-up

`ReplaceCurrent` selects the match and calls `InsertString`, which
already records exactly one structural step and patches the style grid.

`ReplaceAll` is the interesting one. It needs one undo step for a burst,
which is what `undoSuppress` exists for — and CLAUDE.md says nothing but
the caret fan-out may touch that flag, since an unbalanced `true`
silently disables undo for the rest of the session. The way out: file
one snapshot with `pushUndo`, then edit the **Buffer** directly. Buffer
primitives record no history, so `pushUndo` is never re-entered and the
flag is never involved.

Bottom-up, for the reason the caret fan-out is:

```
"a-a-a"  replacing a → long
   top-down:  "long-a-a"   … and cols 2, 4 now point into the wrong text
   bottom-up: "a-a-long" → "a-long-long" → "long-long-long"
```

A replacement of a *different width* is what makes this bite, which is
why the test uses one — an equal-width replacement passes either way.

`ReplaceCurrent` also advances past what it wrote, so `s/a/aa/`
terminates instead of matching its own output forever.

### Go to line

`Esc j`. Takes `42`, `42:10`, `42,10`, and a pasted `app.go:314:22` — the
shape a user actually has in hand is a fragment of compiler output, not a
number. Out-of-range **clamps**: the line usually comes from a build log
a few edits stale, and the last line is a better answer than a refusal.

`j` not `l`, because a lone `l` is one stray Esc away from every word
with an L in it — the same argument that put the git Log on a shifted
`L`. `e` for replace because `r` is redo; pairing replace with find under
the shift convention would have said the wrong thing about a mutating
verb.

---

## Stage 6 — compare

`e9f1680` · `internal/diff/diff.go`, `internal/app/compare.go`

### The differ is ced's own

The plan suggested `git diff --no-index`. It lost on two counts: the
sources a comparison actually has are **buffers** (unsaved text, a pasted
block), so handing them to git means temp files; and git isn't
guaranteed present, and refuses the job outside a repo anyway, which
would have made the feature repo-only for no reason. Same argument stage
4 made against ripgrep. `internal/diff` is ~420 lines with no dependency.

**Patience, not Myers or a plain LCS** — a readability choice before a
performance one:

```
trim common prefix/suffix
  └─ lines appearing exactly ONCE on each side   → candidate anchors
       └─ longest increasing subsequence          → anchors we keep
            └─ recurse between anchors
                 └─ nothing unique left? small: LCS table
                                         big:   wholesale replace
```

Anchoring on unique lines is what stops "a function was added" rendering
as "every closing brace moved". A full LCS table appears only inside
small unanchorable regions (`lcsCellBudget`); two 20k-line files would
otherwise want 400M cells.

Two implementation notes that are load-bearing:

- The common-**suffix** peel is counted, not collected. The first cut
  prepended each line to a slice, which made the most common case of all
  — one edit near the top of a long file — quadratic. There's a
  20k-line test for it.
- `SplitLines` does not invent a trailing empty line. A file ending in
  `\n` has as many lines as it has newlines; a phantom one would report
  an edit on *every* buffer-vs-file comparison.

Output is byte-compatible with `git diff` — checked against real git
output on a Go file, identical but for git's function-context suffix
after `@@`. That's what let `gitPanelDiffStyle` (coloring) and
`diffTargetLine` (row → file line) be reused as-is.

### The panel

Fourth occupant of the bottom strip. Three sources: a picked file, the
buffer's own saved copy, pasted text.

- **The active buffer is always the NEW side.** Not cosmetic: it makes
  the `+` lines the ones that exist in the open file, so `diffTargetLine`
  maps a display row straight to a line in the tab and the double-click
  jump costs nothing. It also reads the way the question is asked.
- **Both sides come from the buffer when the file is open** — you compare
  what you're looking at, including unsaved edits. The exception is a
  file compared with *itself*, which only means anything against the
  saved copy: that side reads disk and the label says `(saved)`, because
  "t.txt ↔ t.txt" would look like a bug.
- **Pasted text is first-class** because ced cannot *read* the system
  clipboard (OSC 52 is write-only, and that's correct for an SSH-first
  editor). "Compare with pasted text" ARMS the panel — visibly, with the
  instruction in the body — and `comparePasteTarget` then outranks the
  editor, chat and terminal for the next bracketed paste. Outranking them
  is safe precisely because only a deliberate act can arm it. Cmd+V feeds
  it from the internal clipboard; Esc disarms it as a side effect, like
  clearing the ghost.
- **⟳ re-reads the file side** and re-diffs a pasted one. `oldPath` is
  kept for that rather than reconstructing a path from `oldLabel` —
  the label is prose (it carries "(saved)") and wouldn't survive a path
  outside the project root.
- Its own ≡ group rather than rows under Git, because **none of it needs
  a repo**.
- No leader key. The flat table is out of mnemonic letters and this isn't
  a namespace's worth of surface; ≡ and the palette carry it.

---

## Verification

`make test` (race detector) green at every commit, plus real-binary
checks through the `run-ced` PTY capture skill — which earned its keep
again, since the tmux-folded `alt+a` path is invisible to unit tests:

- the two-row bar with both toggles and both buttons, rendered
- `{esc}e` → type → `{tab}` → type → `{esc}a` replaced all three hits in
  a scratch file, case-insensitively (it caught `Alpha` too)
- `{esc}c` in the bar flipped the counter from 4 hits to 3
- `{esc}j` with `88:5` centered line 88
- a real file↔file comparison: header, `+1 −5` stats, ⟳ / ✕, colored diff
- the armed paste mode's instruction row

Not covered by a real-binary check: the bracketed paste itself. The
capture tool sends keystrokes, not paste markers, so `compareInsertPaste`
and the target predicates are unit-tested only.

New tests: `editor/replace_test.go` (9), `diff/diff_test.go` (13),
`app/compare_test.go` (13), `app/goto_test.go` (6), plus 11 appended to
`editor/find_test.go` and `app/find_test.go`.

---

## What's queued

Stages 7–12, untouched:

| # | Stage |
|---|---|
| 7 | Open folder — recent folders, bare-`ced` restore |
| 8 | Session restore — open tabs + cursors per root |
| 9 | LSP verbs — document symbols first, then references/rename/actions |
| 10 | Terminal diagnostics — scrollback → `diag.go` → clickable jumps |
| 11 | `--wait` / `--remote` — `$EDITOR` integration, single-instance open |
| 12 | Undo memory cap — byte-budget the snapshot stack |

Three notes for whoever picks these up:

- **`internal/diff` is now available to anything that wants a diff.**
  Stage 10's terminal-output parsing has no use for it, but a future
  side-by-side compare, a "what did the agent change?" review surface, or
  an undo-history preview all would.
- **Regex find was considered and skipped** in stage 5. It's not a third
  checkbox: it needs its own input grammar, an error surface for a
  half-typed pattern, and a different replacement language (`$1`). If it
  ever lands it should go through `matchCols`'s call site, not around it.
- **Open folder** (stage 7) still wants teardown → `New(newRoot)`, and
  the compare panel adds one more thing derived from `rootDir`:
  `compare.oldPath` / `newPath` are absolute, so they survive, but the
  panel should be closed on a root switch rather than left showing a diff
  of files from the old project.
