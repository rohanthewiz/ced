# Session: highlight debounce, file-IO durability, tab strip, project search

Session ID: `783f08d7-329a-44b3-9c6a-06527b160a37`
Date: 2026-08-03
Branch: `main`
Commits: `7171f45` … `0c30580` (6 commits, +3476 / −189 across 23 files)

---

## How this started

The ask was open-ended: what low-hanging fruit remains in ced, across
features, robustness, and performance? Two candidates came with the
question — file↔clipboard / file↔file comparison, and "open a new
folder".

Rather than answer from the CLAUDE.md description, the codebase was
surveyed: the menu group table (the real feature inventory), the leader
table, the render path, `NewTab`/`Save`, the tab-bar geometry, the finder,
and the undo model. Two throwaway measurement harnesses were written into
`internal/editor`, run, and deleted.

That survey turned up three things that outranked both of the proposed
features, and the owner picked the order from the resulting list.

## What the survey found

**The headline, and it was measured, not guessed.** `Tab.Render` called
`Highlight` whenever `StyleStale` was set, and every buffer mutation sets
it. So one typed rune re-joined the whole buffer, re-tokenised it with
Chroma, and rebuilt a per-rune style grid:

| file | lines | KB | ms per keystroke | garbage |
|---|---|---|---|---|
| `internal/app/app.go` | 3,831 | 134 | **69.95** | 36 MB |
| `internal/app/copilot_chat.go` | 1,898 | 65 | 36.61 | 18 MB |
| synthetic 25k-line `.go` | 25,000 | 1,586 | **1757.23** | 854 MB |

Typing in ced's own largest source file ran at ~14 fps and produced 36 MB
of garbage per character, before SSH latency.

**Other findings, in the order they were ranked:**

- `NewTab` called `os.ReadFile` on anything clicked — no size cap, no
  binary sniff. One misclick on a vendored `.so` or a 300 MB log.
- No CRLF handling anywhere in `internal/editor`. Every line of a Windows
  file kept a trailing `\r`, and a save wrote untouched lines back CRLF
  and newly typed lines LF.
- `os.WriteFile` truncates in place — a full disk or a killed process
  leaves a truncated file, and the undo history dies with the process.
- `layoutTabs` walked tabs without bound and `drawTabBar` clipped the
  overflow, so the ACTIVE tab could be off-screen — and there was **no
  tab-switching key binding of any kind**.
- No replace, no go-to-line, no project-wide text search; find is
  unconditionally case-insensitive with no options.
- Only two LSP verbs wired (definition, hover) despite a working client.
- 500 full `[]string` undo snapshots per tab (~200 MB of slice headers on
  a 25k-line file).

All of it, plus a 12-stage plan, is in
[`ai_docs/opus-improvements-analysis.md`](../opus-improvements-analysis.md).

---

## Stage 1 — stop re-lexing on every keystroke

`6d86b08` · `internal/editor/syntax.go`, `internal/app/syntax.go`

Chroma has no incremental API, so the fix is asking less often and making
what gets painted meanwhile indistinguishable.

```
    intra-line edit          structural edit
   (typing, backspace)     (Enter, paste, undo,
          │                 line ops, reload…)
          ▼                        │
   patch the grid                  │
   in place, defer                 ▼
          │                  re-lex on the
          │                  next render
          ▼
   ── SyntaxSettle idle ──►  re-lex on the next render
```

Two rules hold it up:

- **Only intra-line edits defer.** Anything changing the line structure
  re-lexes immediately, because a grid whose ROWS no longer align with
  the buffer's repaints the whole screen below the edit wrongly. That
  boundary is free — it is the same "structural" cut the undo grouping
  already makes — and it covers exactly the keystrokes arriving dozens
  per second while skipping the ones arriving once a line.
- **A deferred edit patches the row it touched.** Without it everything
  right of the caret smears one column for the settle window. Typed runes
  inherit their left neighbour's style, so a character typed inside a
  string stays string-colored.

`InvalidateStyles()` became the DEFAULT contract for a mutation path;
deferral is the opt-in. That inversion matters: a future mutator that
just sets `StyleStale = true` would inherit whatever `styleDefer` the
last edit left.

The settle timer lives app-side (the editor has no loop to wake itself
from), mirrors `lspAfterEvent`, and is armed only while a tab waits on it
— the caret-blink constraint. Over `MaxHighlightBytes` (512 KB) a tab
opens `SyntaxOff` and says so in the status bar.

**Result: 70.17 ms → 0.0015 ms per keystroke** during a burst, with one
70 ms pass at its end. Confirmed against the real binary via the
`run-ced` capture skill: a mid-burst frame is token-for-token identical
to a settled one (`var` purple, `150` orange, `*` blue, everything after
the insertion point unmoved).

## Stage 2 — guard what opens, round-trip endings, save atomically

`ee891fe` · `internal/editor/fileio.go`

Three concerns grouped because they are the same question asked twice:
can we faithfully round-trip this file, and did we?

- **Guards run BEFORE the read** — `MaxOpenBytes` (32 MB) is checked on
  the stat, since a limit checked afterwards has already paid for the
  damage. The binary sniff is one NUL in the first 8 KB: it catches
  executables, archives, images and UTF-16 without a content-type table,
  and UTF-16 is a file ced genuinely cannot round-trip.
- **The buffer always holds bare LF.** `LineEnding` and `BOM` are
  detected on load, stripped, and re-emitted on write. Normalisation is
  whole-file: a file's ending is a property of the FILE, and re-emitting
  it uniformly is what stops a mixed-ending file getting more mixed.
- **Temp file + rename**, with three load-bearing details: the temp lives
  in the TARGET's directory (rename is only atomic within a filesystem),
  symlinks are resolved first (renaming onto a link replaces the LINK
  with a regular file — this would have broken dotfile layouts), and mode
  is copied from the existing file, because `os.WriteFile`'s perm only
  applies at creation and the old in-place write preserved it for free.
  A read-only directory falls back to an in-place write.

Verified live: a `.so` refuses with `Error: vendor.so looks like a binary
file`, and a CRLF file edited and saved comes back `0d0a` on every line
including the edited one.

**Caveat found while verifying:** gofmt/goimports always emit LF, so
format-on-save still normalises a CRLF `.go` file. That is gofmt's call,
not ced's, but it is documented so it does not get filed against this
stage.

## Stage 3 — scroll the tab strip, give tabs a keyboard

`0606035` · `internal/app/tabbar.go` (tab-bar code moved out of `app.go`)

```
 ≡ │ main.go × │ app.go × │ tab.go ×          … │ +4 │
   └── tabScroll ──┴── drawn while they fit ──┘  └ click to pick
```

- `tabScroll` is DERIVED every frame: push forward until the active tab
  fits, **pull back** when closing tabs leaves dead space. The pull-back
  is invisible to any test that only opens files — it has its own test.
- A tab that does not fit is not laid out at all, because `lastTabRects`
  is what hit-testing reads.
- `switchToTab` is the single place a switch records nav history; the
  click path used to do it inline.
- `+N` overflow button opens the switcher — the only mouse path to a
  hidden tab.

**The trap of the session:** `Esc [` / `Esc ]` — what every editor with
buffers uses — CANNOT be bound. `\x1b[` is the CSI introducer and
`\x1b]` is OSC, so the terminal's parser consumes the pair before the
leader table sees it. The binding built, tested green against a synthetic
key event, and did nothing in a real terminal; only the PTY capture
caught it. Shipped as `Esc ,` / `Esc .` (`<` and `>` live on those keys)
plus `Esc b` for the picker. Same trap applies to `P`, `N`, `\`, `^`,
`_`, `#`.

## Stage 4 — Find in project

`eb4bfe9` · `internal/search/search.go`, `internal/app/projectsearch.go`

Small because the find-all panel already had everything a cross-file
result list needs — two-column rows, a displacing strip, a right dock,
scrolling, an Esc contract, a mouse story. It needed a path per row and a
different verb behind Enter.

- **Pure Go, not ripgrep**: neither rg nor grep is on every machine, and
  the promise is one static binary. Cost is bounded instead by searching
  the FINDER'S index (gitignore rules already applied), skipping
  too-big/binary files, and capping results — with the cap **reported**,
  since a silently short list reads as "that's all of them".
- **One matcher**: delegates to `editor.FindAll`, so the two scopes of
  "find" cannot disagree about what a hit is.
- **Project mode deliberately does not preview.** Walking rows across
  files would open a file per keystroke — firing the LSP's didOpen,
  Copilot's didOpen, every plugin's open hook and a syntax pass, and
  leaving a tab behind for every row scrolled past. The row IS the
  preview; Enter opens, centered, with the query lit.
- Labels truncate from the FRONT (`…rnal/app/findall.go:386`) — the
  distinguishing part of a path is its tail.

## Housekeeping

`0c30580` — CLAUDE.md gained architecture-map entries for the five new
files and house-rule sections for each stage, plus refreshed menu
geometry pins (2 top-zone + 87 group actions + 13 headers = 102,
height 108, dividers `[2, 5, 105]`).

---

## Verification

Every stage: `make test` (race detector) green, plus a real-binary check
through the `run-ced` PTY capture skill. That last part earned its keep —
the `Esc [` failure was invisible to the unit tests and only showed up
when a real terminal was on the other end.

New tests: `editor/syntax_test.go` (9), `editor/fileio_test.go` (14),
`app/syntax_test.go` (3), `app/tabbar_test.go` (10),
`app/projectsearch_test.go` (9), `search/search_test.go` (7), plus
`TestOpenFile_RefusesBinary` in `app_test.go`.

## What's queued

Stages 5–12 of the plan, untouched:

| # | Stage |
|---|---|
| 5 | Find verbs — replace, case/whole-word, go to line |
| 6 | Compare — file↔file, compare with pasted text |
| 7 | Open folder — recent folders, bare-`ced` restore |
| 8 | Session restore — open tabs + cursors per root |
| 9 | LSP verbs — document symbols first, then references/rename/actions |
| 10 | Terminal diagnostics — scrollback → `diag.go` → clickable jumps |
| 11 | `--wait` / `--remote` — `$EDITOR` integration, single-instance open |
| 12 | Undo memory cap — byte-budget the snapshot stack |

Two notes for whoever picks these up:

- **Compare**: ced cannot read the system clipboard (`clipboard.go` is
  OSC 52 write-only, and that is correct for an SSH-first editor), so
  "compare with clipboard" means the INTERNAL clipboard. The more useful
  framing is "compare with pasted text" — a scratch buffer filled by
  bracketed paste — which makes file↔file the same code path with a
  different left side. `git diff --no-index` supplies the patch even
  outside a repo, and two unified-diff render surfaces already exist.
- **Open folder**: implement as teardown → `New(newRoot)`, not a field
  reassignment. `rootDir` itself is touched in about six places, but
  gopls's `rootUri` is fixed at initialize, and the finder index, tree,
  git status, ACP session cwd, MCP roots and plugin cwd are all derived
  from it.
