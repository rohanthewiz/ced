# Session: the rename verb

Session ID: `cee887d0-bdda-4ba3-bd55-b19c82054dc6`
Date: 2026-08-03
Branch: `main`
Parent commit: `53dba51`

---

## How this started

The previous session built the WorkspaceEdit primitive and closed with a
two-row queue, each described as "a thin caller". This session cashed the
first of them. The request was one line ("Now build the rename verb"), and
the interesting result is that the estimate held: `handleLSPRename` is four
lines plus a label, and the only genuinely new problem in the whole feature
turned out to be one the primitive couldn't have anticipated — the PROMPT.

---

## What was built

| File | What |
|---|---|
| `internal/lsp/types.go` | `RenameParams` |
| `internal/lsp/client.go` | `Client.Rename`, `renameTimeout`, the `textDocument.rename` capability |
| `internal/app/lsprename.go` | the verb: prompt → request → `applyServerEdit` |
| `internal/app/lsp.go` | `Rename` on `lspConn`, `renameSeq` |
| `internal/app/app.go` | dispatch + the ≡ Code row |
| `internal/app/leader.go` | `Esc E` |

34 new tests. `make test` (race) green, `go vet ./...` clean.

---

## The prompt is the whole design problem

Every other LSP verb in this codebase is one gesture: press the key, the
request goes out. Rename is TWO — press the key, then type a name — and the
gap between them is where everything interesting lives.

The reflex reading is that the gap is safe because a modal owns the
keyboard, so the user cannot type into the buffer while the prompt is up.
That reading is wrong, and it is wrong in a way no test would find by
accident: the LSP debounce timer, auto-save, a chat agent's `fs/write` and
the three-way disk reconciliation are all still running. Any of them can
move the document under the prompt, and the position captured a moment ago
would then point at whatever text has since slid into that spot.

So `startRename` is split out of `menuRenameSymbol`, and the ordering is:

1. **Prompt open** — capture `(path, cursor, EditRev)`. The word under the
   cursor is resolved here too, but only to seed the field.
2. **Submit** — re-resolve the tab by path and compare `EditRev`. A
   mismatch refuses and says so, rather than renaming whatever is there now.
3. **Then** flush, **then** `captureWSRequest`, **then** dispatch.

Step 3's order is load-bearing in the opposite direction from what it looks
like. Capturing the staleness contract BEFORE the flush records the
pre-sync revision, and `planWorkspaceEdit` would then refuse the request
this very function just made — a feature that fails every time, for a
reason that reads like a race.

---

## Three smaller decisions

**Generation-checked, and for a harder reason than references.** The
references verb needs `refSeq` because its answer opens a panel. This one
needs `renameSeq` because its answer WRITES FILES: two renames in flight
together, answered out of order, means the older one's edit gets planned
against a buffer the newer one has already rewritten. The plan's own
staleness checks would catch most of that; the seq catches the rest and
costs one integer.

**The old name never goes on the wire.** `RenameParams` carries a position
and a new name, and that is the entire difference between this and a
textual replace-all — the server resolves the position to a symbol and
rewrites its BINDINGS. `cursorWord` is therefore a UI affordance and
nothing more: it seeds the prompt so `fooBar` → `fooBaz` is a keystroke,
and it refuses a cursor on nothing before the user has been asked to think
of a name for it.

**Two client-side refusals, deliberately narrow.** An unchanged name, and
whitespace. The first because the round trip is guaranteed to come back
with "nothing to change", which is a confusing way to answer "you didn't
change it". The second because no language ced will speak allows a space in
an identifier, so the server's error would only be a slower way to say the
same thing — and by then the user has already typed it. Everything else — a
keyword, a leading digit, a collision — is the SERVER's judgment, and its
message names the actual rule. This list must not grow into an identifier
validator.

---

## `prepareSupport` was considered and declined

`textDocument/prepareRename` validates a position and hands back a
placeholder before the user types anything. It was tempting and it is the
wrong trade here: the placeholder is the word under the cursor, which
`cursorWord` already has, and the validation is repeated by the rename
itself a moment later. What it would actually buy is a round trip on the
critical path of a two-step gesture, plus three response shapes to
normalise in `internal/lsp` for a check that happens twice. gopls's own
refusal message ("can't rename package: not supported") is better than
anything a prepare step's silence could convey.

`renameTimeout` is 30s rather than the 5s default, for references' reason
and then some: a rename IS a project-wide reference search, plus the work
of building an edit for every file it found. A timeout here is also the
most expensive one in the client, because the user has already typed the
name.

---

## The leader key, and the letter that couldn't be used

`Esc E` — the shifted twin of rEplace's `Esc e`, under the f/F, p/P, d/D
convention that already runs through this table: same verb, wider and
smarter scope. `e` replaces text you name, in this file, by matching
characters; `E` replaces a SYMBOL the compiler names, everywhere it is
bound, in files you may never have opened.

The obvious letter was 'n' for reName, and it is new-file. The obvious
fallback was 'N', and it is a trap: `\x1bN` is SS2, one of the ESC pairs a
terminal can consume before tcell's parser sees it — the same family that
keeps `[` and `]` off the tab-switching keys and gave this codebase its
"the binding tests green and does nothing in a real terminal" rule.

Which is why 'E' was not trusted to a unit test. See below.

---

## Verification

`make test` (race) green. `go vet ./...` clean.

**34 new tests.** Four in `internal/lsp`: the wire payload (asserting the
three fields that ARE on it, since the old name deliberately isn't), the
null-is-a-real-answer case, a server refusal keeping its own reason
verbatim, and the `textDocument.rename` capability appearing in the
handshake — that last one needed the trailing `initialized` notification
drained, because the test pipes are unbuffered and `Initialize` otherwise
never returns.

Thirty in `internal/app`: the happy path end to end, the prompt seeding,
the label being the same string in the group / the ≡ undo row / the flash,
both client-side refusals, the buffer-moved-under-the-prompt refusal,
flush-before-ask ordering asserted on the call log, the dropped stale
generation, the server's reason surviving, the empty edit, the
reaches-files-with-no-tab path (confirm → disk write → no new tabs), the
one-press cross-file undo, and menu/leader agreement.

**Real gopls, real PTY** — the part that mattered most, through the
`run-ced` capture tool on a seeded two-file module:

- `Esc E` opens the prompt in a real terminal. The SS2 concern was real
  enough to check and 'E' is clear of it.
- The ≡ Code group renders `Rename symbol…  esc E` directly above
  `Undo multi-file edit`.
- Renaming `greeting` → `salutation` produced *5 edits in 2 files (1
  written, 1 unsaved)*; the receipt listed all five under
  `Changed by "Rename greeting → salutation"`; `other.go` was rewritten on
  disk with no tab opened for it (verified with `cat`); the open tab went
  dirty rather than being saved.
- One press of `Esc z` in the open file took BOTH files back — status bar
  `Undid Rename greeting → salutation (2 files)`, disk file restored, tab
  clean again.

Menu geometry pins updated — 121 rows / height 127 / dividers `[2, 5, 124]`
— in the tests and in CLAUDE.md.

`gofmt -l` reports the same three pre-existing files (`internal/app/app.go`,
`internal/app/tabbar.go`, `internal/editor/syntax.go`). Untouched, as the
last four sessions did.

---

## What's queued

One stage-9 verb left, and it is the one that actually proves the
primitive's API rather than merely using it:

| Verb | What's left |
|---|---|
| Code actions | `textDocument/codeAction` + a picker; `act.Edit` → `applyServerEdit`, command-only actions go through `workspace/applyEdit` |

It supplies a different label, an EMPTY `wsRequest` (a server-initiated
edit has no origin revision to claim), and arrives from a server REQUEST
rather than a response — three things rename never exercised, and none of
which should require changing a line of `workspaceedit.go`. If any of them
does, that is the finding.

Two notes for anyone working near this:

- **Don't collapse `startRename` back into `menuRenameSymbol`.** It looks
  like an artificial split — the prompt callback could just close over the
  request — and it is the only place the "the document moved while a modal
  was up" check can live. The failure it prevents is silent and renames the
  wrong symbol.
- **Don't grow the client-side name validation.** Two refusals is the
  designed size. A third one starts a slow slide toward reimplementing the
  server's judgment in the editor, where it will be wrong for some language
  ced adds later.
