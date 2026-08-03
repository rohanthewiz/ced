# Session: the WorkspaceEdit primitive

Session ID: `dfa34a97-ac33-4b0a-91a4-213bfedb7f4b`
Date: 2026-08-03
Branch: `main`
Parent commit: `cc6e459`

---

## How this started

The signature-help session closed with a queue of exactly two verbs —
code actions and rename — and one sentence explaining why neither had
shipped: they share a blocker, and the blocker is a BUFFER-layer problem,
not an LSP one. This session built the blocker instead of a verb, on that
session's own argument: *build the primitive properly and it pays for
two.*

The request was one line ("Let's build the WorkspaceEdit primitive"), so
the design work was front-loaded — an Explore pass over the edit/undo/LSP
layers, a Plan pass, then three questions put to the owner. All three
recommendations were taken, and two of them are the whole shape of the
feature.

---

## What was built

| File | What |
|---|---|
| `internal/lsp/workspaceedit.go` | wire structs, `ParseWorkspaceEdit`, both shapes → one normal form |
| `internal/editor/multiedit.go` | `Edit`, `ApplyMultiEdit`, `EditResults`, `OverlapIndex` |
| `internal/app/workspaceedit.go` | plan → confirm → apply → journal → receipt |
| `internal/editor/buffer.go` | `ClampEnd` |
| `internal/editor/undo.go` | `UndoDepth` |
| `internal/editor/replace.go` | `ReplaceAll` REFACTORED onto `ApplyMultiEdit` |
| `internal/lsp/client.go` | the `workspace.workspaceEdit` capability |
| `internal/app/copilot_chat_perm.go` | `chatFSResolve` → `resolveInRoot` |
| `internal/app/app.go` | `wsGroup` field, undo/redo claim, ≡ Code row, `closeTab` hook |

45 new tests. `make test` (race) green.

---

## The three decisions that were the owner's to make

### 1. Files the user never opened → detached write, confirm first

The obvious answer is "open every touched file as a tab" — maximum
visibility, per-tab undo for free. It is the wrong one, and this codebase
had already written down why in a different section: find-in-project
refuses to open a file per row because that fires didOpen for the LSP and
Copilot, every plugin's open hook and a syntax pass, and leaves a tab
behind. A twelve-file rename pays all of that AND leaves twelve DIRTY
tabs — twelve modal round-trips at quit, most of them not even laid out
on the strip, since `layoutTabs` doesn't lay out a tab that doesn't fit.
"Visibility" that shows you four of twelve files isn't visibility.

So: files with no tab are loaded into a **detached `editor.Tab`**, edited,
and written with `Tab.Save()`. That choice pays for itself three times —
`NewTab` applies the open guards (size, binary sniff), `Save` re-emits the
BOM and line ending, and the retained Tab is what makes both the group
undo and the rollback possible. A raw string would have bought none of it.

The confirmation exists because the alternative is a rename silently
rewriting nine files on disk, and nothing else in ced does that.

### 2. Plain undo CLAIMS the group

This is the question the whole journal exists to answer, and it's the one
where the safe-looking option is the dangerous one. Leaving Esc-u alone
"touches no existing keybinding" — and leaves a user who presses it in one
renamed file with eleven files renamed and one not, silently. A
half-applied refactor is worse than either extreme.

So plain undo in a participant tab unwinds the whole group. When it can't,
it degrades **loudly**: names the file that moved, undoes just this tab,
and CLEARS the slot. Each third of that is load-bearing — falling through
because an undo that does nothing reads as broken, announcing because
silence is what we're trying to avoid, clearing because a later press must
not half-apply the rest against text that has moved.

### 3. Primitive + undo + receipt in one change

Shipping the primitive dormant would have made it untestable end to end.
The receipt is what makes the no-tabs decision honest.

---

## The undo journal is the only thing above the per-tab stacks

Undo in this editor is per-Tab, snapshot-based, with no transaction
concept anywhere. Rather than invent one inside `internal/editor` — which
would have meant a transaction ID on `snapshot` and a new invariant on
every push path — the group lives on the App as ONE slot.

Validity per participant is **`EditRev` AND `UndoDepth`**, and the pair is
not belt-and-braces:

- `EditRev` alone can't say the snapshot is still on top. `trimUndo`
  evicts from the BOTTOM, so a stack shrinks without the buffer changing.
- `UndoDepth` alone can't either — a push plus an eviction nets to zero.

`UndoDepth()` is therefore the only thing about the stack that got
exported, and its doc comment says what it's for so nobody widens it.

A detached participant also checks its **mtime**: somebody else writing
the file since means rewriting it would discard their change, which is not
what undo means. And `closeTab` drops the journal outright — a closed tab
takes its undo stack with it, and a group that silently skipped a file is
the exact failure this prevents.

---

## Two traps found while reading, not while debugging

**`Buffer.Clamp` is wrong for a range end.** It pins the line to the last
line and THEN pins the column to that line's length. So `{LineCount, 0}` —
how the protocol spells "replace the whole document" — resolves to
`{lastLine, 0}` and spares the final line's text. That's a whole-file
rewrite that silently leaves the last line behind. `ClampEnd` is a
separate primitive because only range ENDS want this: a cursor past the
end of the buffer is a bug wherever it came from, and Clamp's job is to
make it harmless.

**Lexical root confinement is escapable here in a way it isn't for a
read.** `writeFileAtomic` resolves symlinks BEFORE writing, so a link
inside the root pointing outside it passes a textual check and then gets
written through. `resolveInRoot` re-checks after `EvalSymlinks` — and
resolves the ROOT too, which turned out to be necessary rather than
tidy: on macOS `t.TempDir()` hands back `/var/...` while the kernel
reports `/private/var/...`, so resolving only one side put every file in a
temp-rooted project "outside" its own root. Four chat-FS tests caught it
immediately.

---

## `ReplaceAll` was refactored onto the new primitive

The bottom-up, one-snapshot pass already existed — `ReplaceAll` was this
codebase's first multi-range edit and its file comment already stated both
rules. Copying it would have left two implementations of "edit a set of
ranges as one step" to drift apart exactly where a user would notice, so
the pass moved into `multiedit.go` and `ReplaceAll` became ten lines that
build `[]Edit`. The existing `replace_test.go` is now the regression net
for the new primitive, which is why that refactor was done FIRST and
alone.

The one thing `ReplaceAll` never had to think about: its edits come from
one scanner and are well-formed by construction. A server's are not, hence
`OverlapIndex` — and the refusal matters because overlapping edits applied
bottom-up don't fail, they produce plausible-looking garbage.

`EditResults` is the non-obvious piece: it derives where each edit LANDED
analytically (a line-shift accumulator plus a column-shift that only
survives on the previous edit's end line) rather than recording positions
during the pass, because the pass runs backwards and every recorded
position would need fixing up as earlier edits arrived. A test cross-
checks its claims against `Substring` of the really-applied buffer, so the
arithmetic can't quietly drift from reality.

---

## Smaller decisions

- **Resource ops are declined at the capability AND refused by name at the
  parse.** `Initialize` now declares `resourceOperations: []`, so a
  conforming server refuses a package rename ITSELF, with its own reason,
  before anything is applied — a better message than ced could synthesise.
  The parse still keeps them so a non-conforming server gets a refusal
  that names what it asked for. Applying the text edits and dropping the
  file move would rewrite every identifier and leave a tree that doesn't
  build.
- **Planning reads files on the MAIN LOOP**, deliberately, with the reason
  and the escape hatch written down. Which files are open, which buffers
  are dirty and which revisions are synced is main-loop-only state;
  splitting the read from the validation across the loop boundary
  re-opens the staleness window the whole design closes.
- **Open participants are not saved.** That would also commit whatever
  else the user had unsaved and bypass format-on-save's prompts.
  Auto-save takes them two seconds later; the flash names the asymmetry
  rather than hiding it.
- **`plural` and `pathInside` already existed.** Both were written fresh
  and then deleted in favour of the versions in `multicaret.go` and
  `gitstatus.go` — the second one matters, because a path the git panel
  considers inside the project must not be one a workspace edit considers
  outside it.
- The receipt reuses `findAllModal{project: true}` and changes only
  `heading` — the one field lspreferences.go established a non-search
  producer may touch.

---

## Verification

`make test` (race detector) green. `go vet ./...` clean.

**45 new tests.** Twenty-three in `internal/app`: the buffer-vs-disk
split, no-tabs-opened, CRLF+BOM round-trip through a detached write,
one-gesture undo across a buffer and a disk file, plain undo claiming the
group, the loud degradation (asserting the flash names the moved file AND
that the slot is cleared), redo's mirror, the six planning refusals
(stale origin, unsynced participant, outside root, symlink escape,
binary, missing, overlapping), resource-op refusal by name, the receipt's
shape, rollback on a failed write, the close-tab hook, and a UTF-16
conversion exercised with a non-BMP rune where UTF-16 and rune columns
disagree.

Thirteen in `internal/editor`: bottom-up preserving later columns (the
test a top-down implementation fails), multi-line ranges, the ClampEnd
whole-file replace, equal-position inserts keeping array order, one undo
step, caret drop + EditRev, the no-touch refusal, and the EditResults
cross-check. Nine in `internal/lsp`: map-shape sorting run twenty times
(a single pass can pass by luck), array-order preservation, spec
precedence, the kind-not-unmarshal sniff, resource ops surviving,
non-file URIs, annotated edits, and the empty/null answers.

**Real binary**, through the `run-ced` PTY capture: the ≡ Code group
renders `Undo multi-file edit` as its last row, with an empty shortcut
column (correct — no leader) and gated off.

Menu geometry pins updated — 120 rows / height 126 / dividers
`[2, 5, 123]` — in the tests and in CLAUDE.md.

`gofmt -l` reports the same three pre-existing files
(`internal/app/app.go`, `internal/app/tabbar.go`,
`internal/editor/syntax.go`). Confirmed pre-existing by stashing: they
are listed with this session's work removed. Untouched, as the last three
sessions did.

---

## What's queued

Both remaining stage-9 verbs are now thin callers:

| Verb | What's left |
|---|---|
| Rename | `Client.Rename` + `lspConn` method + an event + a prompt → `applyServerEdit` |
| Code actions | `textDocument/codeAction` + a picker; `act.Edit` → `applyServerEdit`, command-only actions go through `workspace/applyEdit` |

The second is the one that proves the API: it supplies a different label,
an EMPTY `wsRequest` (a server-initiated edit has no origin revision to
claim), and arrives from a server request rather than a response — without
changing a line of the primitive.

Three notes for anyone working near this:

- **Do not open tabs for the touched files.** It looks like the obvious
  improvement and it is the thing this design deliberately refuses. See
  the no-tabs rule and the find-in-project precedent it rests on.
- **`ClampEnd` looks like a redundant wrapper around `Clamp`.** Anyone
  who "simplifies" it away will make a whole-file server rewrite spare the
  last line, and no existing test outside `multiedit_test.go` would catch
  it.
- **The journal validates on TWO fields on purpose.** Dropping either one
  gives a check that passes when it shouldn't, in a case that only shows
  up on a long editing session (an eviction) — which is the flavour of bug
  that ships.
