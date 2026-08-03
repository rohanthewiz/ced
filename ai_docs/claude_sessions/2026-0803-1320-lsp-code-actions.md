# Session: code actions, and what they found in the primitive

Session ID: `5bbc828f-696e-4075-a766-1ea8f56cbd41`
Date: 2026-08-03
Branch: `main`
Parent commit: `ed390f8`

---

## How this started

The previous session shipped rename and left one item queued, with an
unusually specific prediction attached to it:

> It supplies a different label, an EMPTY `wsRequest` (a server-initiated
> edit has no origin revision to claim), and arrives from a server REQUEST
> rather than a response — three things rename never exercised, and none of
> which should require changing a line of `workspaceedit.go`. If any of them
> does, that is the finding.

Two of the three did. The label and the empty `wsRequest` cost nothing, as
predicted. The server REQUEST cost two changes, one in the primitive and one
in the transport, and both are the interesting part of this session.

---

## What was built

| File | What |
|---|---|
| `internal/lsp/codeaction.go` | the response union, `Command`, `ParseCodeActions`, `ParseApplyEditRequest` |
| `internal/lsp/types.go` | `Diagnostic` round-trips its raw JSON |
| `internal/lsp/client.go` | `CodeActions`, `ExecuteCommand`, `ErrRequestUnhandled`, `StartWithRequests`, three capabilities |
| `internal/app/lspcodeaction.go` | the verb: ask → picker → edit or command; the applyEdit handler |
| `internal/app/workspaceedit.go` | `applyServerEditWith` — the outcome callback |
| `internal/app/lsp.go` | the narrow `onRequest` hook, `actionSeq`, two conn methods |
| `internal/app/app.go` | dispatch + the ≡ Code row |
| `internal/app/leader.go` | `Esc c` |

46 new tests. `make test` (race) green, `go vet ./...` clean.

---

## Finding 1: the primitive needed to report an OUTCOME

`applyServerEdit` reports whether the edit was ACCEPTED. That was the right
answer for every verb built so far, and the doc comment says why: a verb asks
a question and is told the answer, so "did you take it?" is the only thing it
needs to know.

`workspace/applyEdit` is not a response. It is a REQUEST, with a JSON-RPC id
waiting on a field literally named `applied`. So this is the first edit in
the codebase that ced must **report on** rather than merely perform — and
acceptance and outcome come apart at exactly one point, which is the
confirmation prompt. An edit reaching files the user never opened returns
"accepted" with a dialog still on screen; answering the server `applied:true`
there is a lie if the user then says no.

Hence `applyServerEditWith(we, label, req, done)`, with `applyServerEdit` as
the thin wrapper so rename is untouched. `done` fires EXACTLY ONCE on every
path: the refusals answer immediately, the no-confirm path answers after the
commit (so `commitWorkspaceEdit` now returns `(bool, string)`), and the
confirmation answers from whichever of its two hooks runs.

That last one cost nothing, and is worth noticing: `confirmModal.cancelHook`
already existed, for the format-trust prompt. The shape this needed was
already in the building.

**The mutation test was the useful part.** Deleting the cancel hook made the
declined-confirmation test HANG rather than fail — a bare `<-ev.reply` on a
path that never answers. That is precisely the production failure (a request
goroutine blocked forever), so the test was right about the defect and wrong
about how to report it. Both deferred-answer tests now go through
`awaitReply`, a timed receive.

---

## Finding 2: a request hook has to be able to DECLINE

`lsp.Client` already had an `onRequest` hook, added for ACP. Installing it on
the gopls connection to answer `workspace/applyEdit` would also have taken
over `workspace/configuration` — which gopls BLOCKS on while type-checking,
and which the built-in auto-responder answers with one empty object per
requested item. A hook returning the honest "I don't handle this" null there
wedges the server on the first file opened.

So `ErrRequestUnhandled`: a sentinel that falls through to the auto-responder
as though no hook were installed. The built-in answer moved into
`autoRespond` so both paths reach the same code.

`StartWithRequests` is a separate constructor rather than a parameter on
`Start`, for `NewClientACP`'s reason: the hook has to be in place BEFORE the
read loop starts, so it cannot be assigned afterwards without racing the very
goroutine that reads it.

The hook itself takes the SCREEN, not the App (`lspServeApplyEdit` is a free
function) — it runs off the main loop, where the App is off limits, and the
screen is the whole of what posting an event needs.

---

## The two-minute timeout, and why it points the way it does

`executeCommandTimeout` is 2 minutes — by far the longest in this client, and
for a reason no other call has: **the user is inside it.** A command-only
action runs server-side and sends `workspace/applyEdit` back *during* the
call; ced answers that by planning the edit and possibly asking the user to
confirm. The server is still blocked in executeCommand while that dialog sits
on screen, so the budget has to cover a human reading it, not a type-check.

The app's own wait (`wsApplyTimeout`, 90s) is deliberately SHORTER, so a user
who walks away releases the server rather than the other way round. The cost
of that ordering is a narrow window — a confirmation answered after the
deadline still applies, having already reported that it didn't — and that is
the right way round: the report is a courtesy to the server's log, the edit
is what the user asked for.

---

## Three smaller decisions

**The range is the SELECTION when there is one, the cursor otherwise**, and
that is the whole interface. "Extract to function" only exists for a span, so
a verb that always asked about a point would silently never offer half of
what the server has; a quick fix only exists where a diagnostic is, so a verb
that always asked about a selection would need one first. The ≡ row's label
is dynamic and says which span it will cover — the one thing a user has to
know before pressing the key.

**Diagnostics are echoed back VERBATIM**, which is how a quick fix finds the
problem it fixes. `lsp.Diagnostic` grew `UnmarshalJSON`/`MarshalJSON` keeping
the original bytes, because the fields doing that matching — `data`, `code`,
server-private extensions — are exactly the ones this client has no reason to
model. Same argument the Copilot layer already makes for echoing a completion
item's raw JSON, and the same reason a command's `Arguments` stay
`json.RawMessage`.

**No `resolveSupport`**, the `prepareSupport` trade one verb later. Declaring
it tells the server to withhold edits and wait for a `codeAction/resolve`
round trip; not declaring it makes the server compute them up front, so a
picked row applies immediately. The cost is work the server does for actions
nobody picks, which is its own cheapest work — and it buys away a second
response shape plus a "the row did nothing" failure mode.
`codeActionLiteralSupport` IS declared, or a server sends only bare Commands
the editor could execute blind.

---

## One gap closed unprompted

`openModal` REPLACES rather than refuses. A server-initiated edit is
unprompted and can land at any moment, so applying it while the user is
mid-answer in a prompt would pop a confirmation over that prompt and silently
drop its own pending reply. `handleLSPApplyEdit` now refuses while a modal or
the menu owns the screen, with a reason the server can show.

Deliberately NOT the chat permission layer's queue: there an agent is stuck
and has nowhere else to go, so the prompt has to resurface. Here the server
can simply be told no, and the user runs the action again.

---

## Verification

`make test` (race) green. `go vet ./...` clean.

**46 new tests.** In `internal/lsp`: both response shapes and the
`command`-field-type discriminator, disabled and unresolvable actions
dropped, `KindFamily`, `ParseApplyEditRequest` (including a null version
staying nil), the codeAction wire payload with a `data`-carrying diagnostic
surviving byte-for-byte, empty context going out as `[]` and not null,
executeCommand's raw arguments, the three declared capabilities plus the
ABSENCE of resolveSupport, the `ErrRequestUnhandled` fallthrough, and the
Diagnostic round trip both ways.

In `internal/app`: the two ranges, the diagnostic overlap predicate as a
table, flush-before-ask on the call log, stale generation and departed-file
drops, the empty answer, the server's own error, the row label, the edit
route end to end, the command route with arguments intact, edit-before-command
and refused-edit-skips-command, and the whole applyEdit half — applied,
refused with a reason, empty, the deferred answer through both confirmation
hooks, answered-exactly-once, the label fallback, the busy-dialog refusal, and
the serve function's post-and-wait. Plus two directly on the primitive's
callback, so it is pinned without going through this verb.

**Real gopls (v0.23), real PTY** — through the `run-ced` capture tool:

- `Esc c` opens the picker in a real terminal. The SS2-family concern that
  ruled out `Esc N` last session was worth re-checking; 'c' is clear.
- gopls answered with 5 actions at the file head and 6 with the cursor inside
  an import, titles plus kind families — so range and diagnostics are
  reaching it and the answer moves with the cursor.
- Picking **Organize Imports** removed the unused import in the BUFFER, left
  the tab dirty, left disk untouched, opened the receipt
  (`Changed by "Organize Imports"`), flashed `1 edits in 1 file (unsaved)`,
  and `Esc z` took it back in one press with the tab clean again.
- Picking **Add test for Add** (a bare Command) proved the whole second
  route: executeCommand → gopls sent `workspace/applyEdit` with NO label →
  the "Server edit" fallback → confirmation `1 edits in 1 files. 1 file
  written to disk now.` → yes → `calc_test.go` rewritten on disk with no tab
  opened, tree refreshed, and gopls got `applied:true` and finished clean.
- The refusal round trip too: with no test file present, gopls asked to
  CREATE one; ced refused by name and gopls echoed ced's own sentence back
  inside its RPC error (`edits not applied because of ced can't perform 1
  create file`). Both directions of the contract, on a real server.
- ≡ Code renders `Code actions…  esc c` directly above `Rename symbol…
  esc E`, above `Undo multi-file edit`.

Menu geometry pins updated — 122 rows / height 128 / dividers `[2, 5, 125]` —
in the tests and CLAUDE.md.

`gofmt -l` reports the same three pre-existing files (`internal/app/app.go`,
`internal/app/tabbar.go`, `internal/editor/syntax.go`). Untouched, as the last
five sessions did — app.go's is a stray blank line at ~3633, unrelated.

---

## What's queued

Stage 9 is done: the primitive has two verbs on it, and the second one was
chosen specifically to test the API rather than use it. Nothing is queued
behind this.

Notes for anyone working near it:

- **`applyServerEditWith` is the shape a third verb should reach for**, not a
  new one. If something needs more than a label and an outcome from the
  primitive, that is a signal about the primitive — the same rule
  `lsprename.go` states.
- **Don't widen the `onRequest` hook.** It answers one method on purpose.
  Every method it claims is one the auto-responder stops handling, and
  `workspace/configuration` is load-bearing in a way that fails silently: the
  server just stops type-checking.
- **Don't add `resolveSupport` to buy speed.** It moves the edit to a second
  round trip, which is the failure the current shape rules out — a picked row
  that visibly does nothing while a request is in flight.
- **The applyEdit refusal-while-busy is not a queue, and shouldn't become
  one** unless a server appears that cannot re-offer the action. The
  asymmetry with the chat permission prompt is deliberate and documented.
