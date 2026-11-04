# N-102, N-093: return-type hints read `-> T`; displaced modals hand back

Session: `5448be56-e10b-47e5-87c5-e082f76486a3`

## Ask

`/work-next-list`, third batch (triage in
`2026-1104-1308-n080-n098-n089-label-plural-cmd-o`).

## What changed

### N-102 — return-type inlay hints (`f42cb7f`)

typescript-language-server anchors a function's return type at its `)`
with the label `: string`; `inlayTypePart` found no subject word there and
the note read `» : string`.

- `inlayAfterCloseParen` (spaces skipped) spots the anchor;
  `inlayReturnPart` spells it `-> T`; a label that already carries an
  arrow (basedpyright `-> Any`, clangd `-> int`) passes through.
- **Decision (minor):** arrow, not naming the function (`greet(): T`),
  of the item's two options. An arrow function has no name beside its
  `(`, and the arrow is what two servers already send, so return types
  read alike everywhere. rust-analyzer's chaining hints (also after a
  call's `)`) now read `-> T` too, which is no worse than the bare `: T`.
- **Real binary:** typescript-language-server 5.3.0 + TypeScript 5.9
  installed into the scratchpad (`npm i`, not global), run through
  run-ced on a `greet.ts`. Before (the stale `bin/ced`):
  `function greet(name: string, times: number) {  » : string`. After:
  `» -> string`, and the arrow function's line ends `· -> string`.

### N-101 updated (still Open, the user's call)

While npm was at hand: TypeScript 7.0.2's own `tsc` is a language
server. `tsc --lsp --stdio` answers `initialize` (and refuses one missing
`processId` / `rootUri` / `capabilities`). TS 5.x's `tsc` has no
`--lsp`, so registering it needs the two told apart before spawning.
Recorded in the item; the decision is left with the user.

### N-093 — `replacedModal` (`52ed6c5`)

`openModal` → `closeAllModals` replaced the live Find-all prompt or the
Find-all list without telling them, and both restore borrowed state only
on their own exits.

- `replacedModal` (modal.go): an optional `replaced(a)` on a modal.
  `closeAllModals` clears the slot, THEN calls it, so the hook runs once
  and can't re-enter; a modal's own exits clear the slot before acting,
  so they never reach it. Checked first that the prompt's submit/cancel
  and every list exit (`accept`, `abort`, `settleAt`, `revertLocation`,
  `togglePin`) clear the slot before their restore, and that the one
  internal displacement (the list's Replace confirm) is fine with a
  second, idempotent `restoreFind`.
- The live prompt: `replaced` → `onCancel` (the only prompt that sets
  one) — view and find state back, exactly as Esc. Being displaced is
  not an answer.
- The list: `replaced` → `restoreFind` only. A displacement is one of
  the "other dismissals", which ACCEPT, so the caret stays on the
  preview.
- Tests: `TestFindAllLive_ReplacedRestoresViewAndFindState`,
  `TestFindAll_ReplacedHandsBackTheTint`, and a contract test with a spy
  modal (`TestCloseAllModals_TellsADisplacedModal`: once, slot clear,
  never on `closeModal`). All three fail with the hook stashed.
- CLAUDE.md's single-modal-slot rule records the interface.

## Tests

`go test ./...` + `go vet` before each commit; run-ced for N-102.

## Next

Closed: N-102, N-093. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-101. Full list: `ai_docs/todo/next-list.md`.
