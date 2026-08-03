# Session: LSP references

Session ID: `9789547f-5a05-4315-9860-ce1688753ce9`
Date: 2026-08-03
Branch: `main`
Parent commit: `67a6742`

---

## How this started

The previous session closed the work plan through stage 12 and left one
thing open — the tail of stage 9, the LSP verbs that hadn't been built:

| # | Stage | Still open |
|---|-------|-----------|
| 9 | LSP verbs | references, rename, code actions, signature help |

That note also said which one to take first and why: **references can
reuse the Find-all panel wholesale**, because that panel already carries
paths. Rename is last because it returns a `WorkspaceEdit` — edits to
files nobody has open — and ced has no primitive for that.

So: references.

---

## What was built

| File | What |
|---|---|
| `internal/lsp/types.go` | `ReferenceParams` / `ReferenceContext` |
| `internal/lsp/client.go` | `References()`, the `references` capability, `referencesTimeout` |
| `internal/app/lspreferences.go` | the verb — request, fetch, reconcile, open |
| `internal/app/findall.go` | one new field: `findAllModal.heading` |
| `internal/app/projectsearch.go` | `titleText` reads the heading |
| `internal/app/lsp.go` | `lspConn.References`, `lspState.refSeq` |
| `internal/app/app.go` | dispatch case + the ≡ Code row |
| `internal/app/leader.go` | `Esc R` |

The whole feature is ~280 lines of new app code, and that number is the
design: both halves already existed.

---

## The panel is REUSED, not rebuilt

`findall.go`'s project mode (added last session for Find-in-project) is
already a cross-file result list: rows carrying a path, a fixed-height
strip that DISPLACES the editor rather than covering it, a right-dock
alternative, scrolling, the Esc contract, the mouse story, and the
truncation notice. Every one of those is a decision a references list
would otherwise have had to re-make, and would have made differently.

So the modal grew exactly **one** field:

```go
// heading renames the list for a project-mode producer that is not a
// text search — "References to" (lspreferences.go). It is the ONLY
// thing such a producer is allowed to change.
heading string
```

That constraint is the interesting part, not the field. If a second
thing ever needs changing, that is the signal the two really are
different features and the fork should happen honestly rather than as a
drift of accumulated `if m.project` branches.

The truncation clause stays in the shared `titleText` rather than being
duplicated per producer, because the honesty it buys — a short list must
never read as a complete one — is the part neither producer may skip.

---

## Context text: fetched off-loop, reconciled ON it

This was the only genuinely new problem. An LSP `Location` names a file
and a range and nothing else, so every row's line of code has to be
found. Two constraints pull in opposite directions:

- Reading files is IO, and IO does not belong on the main loop.
- The **buffer** is the authority for any file that's open, because gopls
  answered from the text ced synced to it, unsaved edits included.

The split:

```
goroutine                              main loop
─────────                              ─────────
References() ──► collectRefLines       referenceHits
                 · sort                · open tab? → buffer line
                 · cap                 · else      → the disk line
                 · read each file ONCE · UTF-16 → runes, ONCE
```

Rendering a hit's columns against a stale on-disk line would put the
highlight on the wrong cells of the wrong text — the one failure mode
that makes a results list quietly lie, as opposed to visibly fail.

**Columns therefore stay in UTF-16 across the goroutine hop** and are
converted exactly once, against whichever text is finally chosen. A
multi-line range carries `refEndOfLine` (a sentinel far past any line)
rather than a computed end column, because `RuneCol` clamps — so the
sentinel resolves correctly against either text without the goroutine
having to know which one wins.

Each file is read **once** however many hits it holds; a symbol used
forty times in one file must not cost forty reads.

---

## Sorted before capped

Order is path → line → column, matching `search.Project`, so the two
lists read alike. The cap (`maxReferences = 2000`) is applied AFTER the
sort, so a truncated list is a **prefix** of what the user would have
seen rather than an arbitrary fraction of it — and the count is named in
the title, the project-search rule.

An unreadable file (deleted since, too big, no permission) costs its rows
their CONTEXT, not their existence. The location is the answer; the line
is decoration, and losing decoration is not a reason to drop a hit the
server is telling us about.

---

## The generation guard, and why only this verb has one

Definition and hover are content with a path check — they move a cursor
or pop a modal, and a response for a document the user left is simply
dropped. References is the first code-intelligence verb whose answer
**opens a panel**, so a reply to a question the user has moved on from
would land on top of whatever they are doing now. Hence `lsp.refSeq`, and
the same two guards project search keeps: an empty answer flashes rather
than opening a blank list, and a result arriving while a modal or the
menu owns the screen reports its count instead of stealing the slot.

---

## Smaller decisions

- **The word under the cursor is resolved BEFORE the request, and a miss
  refuses.** It isn't only the title: the panel tints that word in
  whatever file a row opens, which is what makes the landing visible.
  And an identifier is what the server needs anyway — asking about a
  blank line is a round trip guaranteed to come back empty.
- **`includeDeclaration` is always true.** A "who uses this?" list that
  omits the thing being used reads as a hole.
- **`referencesTimeout` is 30s**, not the client's 5s default. This is
  the one verb whose cost scales with the PROJECT rather than the file:
  gopls has to type-check every package that could import the symbol.
- **No response-shape normalisation.** Unlike `definition`, the spec
  allows only an array or null here, so a single object would be a server
  bug rather than a variant worth tolerating.

### The leader key

`Esc R`. The letter References' own name offers, once `r` is spoken for
by redo — the same argument that put rEplace on `e`.

It is deliberately **not** read as a shifted twin of redo: redo's twin is
`Z`, sitting beside its own `z` alias, so the pair convention was already
spent on that key and `R` is free to mean what it says. The f/F, p/P, d/D
pattern is a hint for guessing, not a law that claims every capital.

---

## Verification

`make test` (race detector) green.

**14 new tests.** Twelve in `internal/app` — the word/selection/
punctuation ladder, sort-before-cap, one-read-per-file, the dropped
non-file URI, a missing file keeping its row, buffer-beats-disk, UTF-16
conversion across a non-BMP rune, the multi-line sentinel, the pre-request
flush with `includeDeclaration` on the wire, the no-symbol refusal, and
the four landing paths (opens / truncated title / stale seq / empty +
error / yields the modal slot). Two in `internal/lsp` pin the request's
context object and the null answer.

Plus one pinning the ≡ hint column against the leader table, since
dispatch and display live in different files and a rebind that updated
one would leave the menu naming a key that does something else.

**Real binary**, through the `run-ced` PTY capture skill against real
gopls:

- `Esc R` on `lspFlushChange` returned **7 hits across three files**,
  correctly ordered, relative paths, line numbers matching the source
  (the declaration at `internal/app/lsp.go:401`)
- title read `References to "lspFlushChange"`, footer `↑↓ walk · enter
  open · esc close · d dock` — project mode, as intended
- `↓×5` then Enter opened `internal/app/lspreferences.go` **centered** on
  line 131, status bar `Opened lspreferences.go`

Menu geometry pins updated for the new row — 118 rows / height 124 /
dividers `[2, 5, 121]` — in the tests and in CLAUDE.md.

`gofmt -l` reports `internal/app/app.go`, `internal/app/tabbar.go` and
`internal/editor/syntax.go`. All three predate this work (app.go's is a
stray blank line nowhere near these edits); left alone, as last session
did.

---

## What's queued

Stage 9's tail, minus one:

| Verb | Note |
|---|---|
| Signature help | Probably the next cheapest — it's hover's shape with a different trigger and a different modal |
| Code actions | Needs a way to APPLY a `WorkspaceEdit`, same blocker as rename |
| Rename | The blocker itself |

**The rename blocker is unchanged and is a BUFFER-layer problem, not an
LSP one.** A `WorkspaceEdit` edits files the user never opened, and every
write path in ced today is one buffer at a time. A rename has to be ONE
undo step across all of them. Whoever picks it up should build that
primitive first and put the LSP verb on top — and note that code actions
want the same primitive, so it pays for two.

Two notes for anyone working near this change:

- **`findAllModal.heading` is a door held open by one hinge.** It exists
  so a non-search producer can name the list; it is not a general
  customisation seam. The moment a third producer wants a second field,
  the right move is to ask whether project mode is still one feature —
  not to add the field.
- **The UTF-16 columns surviving the goroutine hop is deliberate and
  fragile-looking.** Anyone "simplifying" `refLoc` by converting to rune
  columns in `collectRefLines` will make every test pass and quietly
  break the dirty-buffer case, which is the case the whole two-phase
  design exists for.
