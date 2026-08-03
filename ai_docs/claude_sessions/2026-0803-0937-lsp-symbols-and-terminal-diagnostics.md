# Session: go to symbol in file, and clickable terminal output

Session ID: `220d3907-a90f-4c5d-9d75-7852abcc8a13`
Date: 2026-08-03
Branch: `main`
Commit: `068bf04` (1 commit, +1609 / −38 across 19 files)

---

## How this started

Stages 9 and 10 of [`ai_docs/opus-improvements-analysis.md`](../opus-improvements-analysis.md),
asked for together:

| # | Stage | Items |
|---|---|---|
| 9 | LSP verbs | document symbols first, then references/rename/actions |
| 10 | Terminal diagnostics | scrollback → `diag.go` → clickable jumps |

They turned out to be two halves of the same question — **"take me to
the problem"**, asked of the language server and of the toolchain — and
they land on the same two surfaces: a palette picker and a row in the ≡
**Code** group. That's why they shipped as one commit.

Stage 9 shipped **document symbols only** (the stage's own "first"); see
"What's queued" for why the rest is a different kind of work.

---

## Stage 9 — go to symbol in file

`internal/lsp/types.go`, `internal/lsp/client.go`, `internal/app/lspsymbols.go`

### The bug that would have shipped silently

`textDocument/documentSymbol` answers with one of two shapes: the modern
hierarchical `DocumentSymbol[]` (children, `selectionRange`) or the
legacy flat `SymbolInformation[]` (`location`, `containerName`). The
obvious normaliser is try-one-then-fall-back:

```go
var tree []DocumentSymbol
if json.Unmarshal(raw, &tree) == nil { … }   // ← always nil
```

**Both shapes decode cleanly into either struct.** JSON ignores unknown
fields and leaves missing ones zero, so a `SymbolInformation[]` read as
`DocumentSymbol[]` "succeeds" with every `Range` at its zero value —
every symbol reporting position 0:0, every picker row jumping to line 1.
An error-based sniff cannot detect that, ever.

`ParseDocumentSymbols` discriminates on **content**: a `"location"` key
exists only in the flat form. The comment in types.go says so, because
the next person to touch it will reach for the unmarshal.

The jump target is `selectionRange` (the NAME), not `range` (the whole
declaration). Landing on a 200-line function's opening brace is exactly
what a symbol jump is for avoiding. A server that omits selectionRange
falls back to range.start rather than to the document origin.

### Not a palette source

palette.go's own doc comment floats "(LSP symbols, …)" as a future merge
source. This is the one place it shouldn't go: sources are collected
**synchronously at open**, and symbols cost a round trip to a server that
may be cold. Feeding them in would either block the palette on gopls or
make its contents arrive late and reorder under the user's fingers. A
verb of its own asks first and shows the list once it has one.

### The row label

`<indent><name>  <kind>` — kind LAST. The fuzzy scorer rewards early
matches, so a leading `function ` would make every row score alike on the
first letters typed. Trailing, `func` still narrows by kind while the
name keeps the position that ranks. Two spaces of indent per Depth are
pure decoration that survives filtering — and are what make an unfiltered
list read as the file's outline.

### Leader: `Esc D`

The shifted twin of `Esc d`: same verb, wider scope — 'd' goes to the
definition of what's under the cursor, 'D' lists every definition in the
file and lets you pick. Exactly the f/F, p/P, h/H convention.

Everything else is the definition/hover contract, unchanged: flush before
asking (an unsynced new function missing from the list reads as the
feature being broken), drop a response whose document is no longer
active, **re-check the path when a row FIRES** (a picker owns the
keyboard but not the world), record nav explicitly (a same-file jump is
invisible to openFile's path-change recording), center an off-screen
landing per goToLine's policy.

---

## Stage 10 — clickable terminal output

`internal/plugins/diag.go`, `internal/app/termdiag.go`, `internal/app/terminal.go`

### The guard IS the feature

`plugins.ParseDiagnostic` (new — the exported single-line twin of
`ParseDiagnostics`, so an in-file mark and a terminal link can never
disagree about the format) is **permissive by design**. It has to be: its
usual caller already knows which file the output describes, so `12:30:
still building` is a legitimate parse there.

Terminal output belongs to nobody. So the rule here is stricter, and it's
the whole reason this is safe:

> a row is a link only when it names a **path**, that path resolves to a
> **regular file**, and that file is **inside rootDir**.

That single sentence rejects progress lines, `go: downloading …`, stack
frames into GOROOT, and a directory named in a `.:1:1:` line. The unit
test walks all five.

### Three details that took a second pass

- **Relative paths resolve against the SHELL's cwd**, not rootDir. `go
  build` prints relative to where it ran, and grsh's `cd` moves that.
  Output that stops resolving after a `cd` is inherent to a scrollback —
  a real terminal's file links behave identically.
- **Resolution is cached, keyed by cwd + the path as printed.** Drawing
  asks per visible row per frame; uncached that is a `stat` syscall per
  row of terminal output on every repaint — fine locally, a storm over a
  slow filesystem. Putting the cwd in the key means a `cd` invalidates
  exactly the answers that changed, for free.
- **`termLocSpan` measures the underline; it does not re-parse.** The
  parse is lossy on purpose (a printed column of 0 clamps to zero-based
  0), so rebuilding `path:line:col` from parsed values would match
  nothing and the underline would vanish. Measuring the raw text can't
  disagree with itself. The underline **is** the affordance — without it
  the feature is invisible — so a row is a link only when parser and
  span agree.

### The ordering question

The one real design decision. A picker of every location in the
scrollback, in document order, buries the build you just ran under every
build before it. In reverse order, one build's three errors read
backwards.

The scrollback already records the echoed command line as `termCmd` — so
the boundaries are free. Blocks go **newest-first, printed order inside
each**. Capped at 200 with the cap named in the title (the
project-search rule: a silently short list reads as "that's all of
them").

### Surfaces

Double-click is primary (reusing the editor's own `lastClick` record, so
the panel's gesture is the git panel's gesture). `Esc ~` — the shifted
twin of the terminal's `` Esc ` `` — and the ≡ row are the keyboard twins,
because macOS Terminal swallows clicks.

The row lives in the **Code** group, not with the terminal's View
toggles: it answers the code-intelligence question, and it's the one row
there that needs no language server at all.

`hasTermOutput` is deliberately approximate (`len(lines) > 0`).
`menuLayout` runs every frame the menu is open, and the honest predicate
is a scrollback walk with a stat behind it — cheap gate plus honest
flash is the right trade.

---

## Verification

`make test` (race detector) green. 20 new tests: `internal/lsp`
(hierarchical, flat, degenerate, kind table, two request tests),
`internal/plugins` (1, asserting the single-line parser agrees with the
whole-output one), `internal/app` (6 symbol + 7 termdiag + 3 in
terminal_test.go).

Real-binary checks through the `run-ced` PTY capture skill, which earned
its keep again — the symbol picker needs a live gopls, which no unit test
has:

- `Esc D` on `internal/app/nav.go` → 19 symbols, fields nested under
  their structs, methods rendered as `(*App).hasNavBack  method`
- picking `navBack` → landed at **Ln 102, Col 15** — the name in `func
  (a *App) navBack()`, centered rather than parked on an edge
- `grep -rn` in the embedded terminal → five accent-colored links; a bare
  `grep -n` (no path prefix) produced **none**, which is the guard doing
  its job
- `Esc ~` → all five listed; picking one opened the file

The capture tool's HTML shows the link color but not the underline
attribute (its emulator is a documented subset); the SimulationScreen
test asserts `AttrUnderline` reaches the cell.

Menu geometry pins updated (116 rows / height 122 / dividers
`[2, 5, 119]`) — and CLAUDE.md's copy of them, which had gone stale two
features ago, now says explicitly that adding a row means updating them.

---

## What's queued

| # | Stage |
|---|---|
| 9 | LSP verbs — **references, rename, code actions, signature help** still open |
| 11 | `--wait` / `--remote` — `$EDITOR` integration, single-instance open |
| 12 | Undo memory cap — byte-budget the snapshot stack |

Three notes for whoever picks these up:

- **Rename is not "one more request".** It returns a `WorkspaceEdit` —
  edits to files the user never opened — and ced has no primitive for
  that. Every write path today is one buffer at a time, and a rename has
  to be ONE undo step across all of them. That's a buffer-layer feature
  with an LSP verb on top, not an LSP verb. References and code actions
  are ordinary work by comparison; references in particular can reuse the
  Find-all panel wholesale (it already carries paths — see the project
  search).
- **`plugins.ParseDiagnostic` is now a shared format, with two very
  different callers.** The decoration path knows which file it asked
  about; the terminal path doesn't. Any third caller has to decide which
  of those it is before trusting a parse.
- **`termRowLine` is the scrollback's single accessor.** The renderer and
  the link scanner both go through it precisely so a click can't land on
  a row the user isn't looking at. A fourth thing that reads the
  scrollback goes through it too.
