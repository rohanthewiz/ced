# Session: cats-native plan, Phase 3.1 — the completion popup

- Session id: `007c7900-9777-4359-b49d-3a7275f52b3f`
- Date: 2026-08-12
- Branch: `main`, commits `35671d5..b5056dc` (2 commits)
- Plan: `ai_docs/cats-native-plan.md` §Phase 3.1 — now marked ✅ done
- Predecessor: `2026-0812-1458-cats-native-phase2.md`

## What was asked

Load the last session, then continue with Phase 3 of the ced × cats
plan. §7's execution order puts **3.1 — the LSP completion popup** next;
the plan calls it the roadmap's centerpiece (~800–1000 LOC).

## What landed

One feature commit plus the plan update. `make test` (race) green;
verified against a real gopls process, which changed two decisions.

### New: `internal/lsp/completion.go` + `completion_test.go`

The wire layer. Three protocol unions normalised away so the app sees
one shape: `CompletionItem[] | CompletionList` (the `isIncomplete` flag
survives as a return value — it is load bearing), `string |
MarkupContent` documentation, and `TextEdit | InsertReplaceEdit` (always
takes `replace`, or completing mid-word doubles the tail).

`ParseCompletionItem` applies the spec's fallback chain once
(SortText/FilterText/InsertText default to Label) — getting that wrong
is quiet and nasty: an empty FilterText matches every prefix and an
empty InsertText types nothing. It keeps `Raw` verbatim because resolve
is defined as "send the item BACK", and a re-marshalled copy loses the
server's `data` correlation field.

`Client.Initialize` now READS its response (it discarded it before) to
capture `completionProvider.triggerCharacters` and `resolveProvider`.
Completion is the first verb whose TRIGGER is the server's business
rather than the editor's.

### New: `internal/app/completion.go` + `completion_test.go`

**Not a modal, and that is the whole design.** Every other
code-intelligence verb takes the single modal slot and owns the
keyboard; a list that owned the keyboard would swallow the keystrokes it
exists to narrow. So it sits where ghost text sits: drawn over the
editor, reading four keys off the top of `handleKey` (arrows, Tab/Enter,
Esc) and letting everything else through to the buffer, which is what
the filter reads.

Wiring, all one-liners in the existing seams: `completionState` on App ·
three cases in the event switch · `completionAfterEvent()` in the
dispatch tail (before `copilotAfterEvent`, so the ghost gate sees fresh
state) · `completionKey` after the chord check and before the Esc branch
· `completionMouse` after the which-key branch · `drawCompletion` above
the panels, below menu/modals · a `closeTab` cleanup line.

### The four decisions worth remembering

1. **Auto-trigger is trigger characters ONLY.** Typing an identifier
   does not open it — Copilot ghost text already occupies "guess what
   I'm typing", and two overlays racing per keystroke is noise. The
   deliberate gesture is `Esc Space`.
2. **Accept goes through `ApplyMultiEdit`** so the item's edit and its
   `additionalTextEdits` are ONE undo step. The caret lands via the
   returned `EditResult`, not the request — an import inserted above
   shifts every following line.
3. **`resolveSupport` is deliberately NOT declared** (the trade
   codeAction already makes), so the auto-import arrives up front.
4. **Staleness is looser than hover's**: a response survives if the
   caret is still on its line with only identifier runes typed since,
   and the prefix is re-read from the CURRENT caret so the list arrives
   already narrowed.

### What the real-gopls check changed

A throwaway harness (`Start` → `Initialize` → `didOpen` → `Completion`
on `fmt.`) proved three things the fake could not:

- `triggerCharacters: ["."]`, `resolveProvider: false`. So
  **`completionItem/resolve` is wired but dormant against gopls** — and
  that is correct, not a gap: not declaring `resolveSupport` is exactly
  why gopls computes everything up front. Resolve only ever enriched the
  detail pane, so it is gated on `CompletionResolves()` and skipped.
- Every item carries `addl=1` — the auto-import, eagerly. This is the
  payoff of decision 3, confirmed rather than assumed.
- `isIncomplete: true`. **The re-request path, not local filtering, is
  the one Go actually uses.** Both exist; a complete list filters in
  memory with the palette's fzy scorer.

### Smaller pieces

- **`Tab.PosScreenCell`** (editor/tab.go) — `CursorScreenCell` for an
  arbitrary position; `CursorScreenCell` now delegates to it. The popup
  anchors at the TOKEN START, so labels sit above the text they replace.
- **`icons.CompletionKind`** — the Codicon block, keyed by bare ints to
  keep `lsp` from importing `icons`. Spelled as `\ue…` escapes: pasted
  private-use glyphs came through the editor as empty strings that still
  compiled (caught only by a "every glyph is one rune" test).
- **`leaderBinding.keyLabel`** — space is the only rune in the table
  with no printable form; without this the which-key overlay documents
  it as a blank cell.
- **Menu surfaces**: ≡ Code group heads with "Completions", editor
  context menu, `Esc Space`.

## Conventions / gotchas worth remembering

- **The four-line insertion in `handleKey` nearly ate the chord
  dispatch.** An Edit whose `old_string` spanned `if a.handleChordKey(ev)
  { return }` dropped it; eight leader/chord tests caught it instantly.
  Worth a `git stash -u` baseline whenever a test failure looks
  unrelated — the first stash left untracked files behind and the
  "baseline" was a build failure, which proved nothing.
- **`finder.Score` is a fuzzy SUBSEQUENCE matcher.** `Pr` matches
  `Sprintf`. Two test fixtures were written as if it were a prefix
  match; the code was right and the tests were wrong.
- **Menu row counts are pinned in `app_test.go`** (`TestMenuLayout_*`) —
  adding one ≡ row means updating four numbers.
- `capLines` returns `maxLines+1` entries when it truncates (the "…"),
  so any caller sizing a box from it must measure at the width it will
  draw at. `completionRect` now computes the width first for that reason.
- gopls still flags `../ced` files as "not in workspace" from the cats
  session — noise; `go build ./...` + `make test` in ced are the real
  checks.

## State / next steps

- Phases 1, 2 and 3.1 all done (2026-08-12). Plan §7 updated with
  progress and the deviations recorded under Phase 3.1.
- Next per §7: **Phase 4 — the git suite**, starting with 4.1's push
  dialog ("never type the current branch").
- **Phase 3.2 (Problems panel) is the natural breather**: Phase 1 already
  stamped the diag status segment and left it inert, waiting for this
  panel to claim its click.
- Still on the table from Phase 2: clicking a tab's `⚠` marker should
  re-raise a deferred conflict prompt.
- Untouched follow-up for this phase: the popup has no "documentation
  pane" beyond four lines, and no `Esc Space` twice → cycle-to-verbose.
  Neither was asked for; both are cheap if the detail row proves thin.
