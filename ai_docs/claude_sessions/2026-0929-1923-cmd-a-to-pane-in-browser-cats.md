# Session: ⌘A handed down to a ced pane in browser-cats (N-026, cats side)

Session ID: 78608c4e-3828-42d0-91f2-2d365d914c8f
Date: 2026-09-29

One next-list item from the cats-todo backlog: N-026. The whole change
is in the **cats** repo; ced itself is untouched apart from this doc and
the next-list.

## N-026: ⌘A stopped at the browser

> cats-side: add `"KeyA"` to `CMD_TO_PANE` (`cmd/catway/web/js/20-keys.js`)
> so ⌘A reaches a ced pane in browser-cats. […] Worth checking first
> that forwarding it does not cost other panes (a shell) a select-all
> they relied on.

### The cost check (done before the edit)

- **Legacy shell panes lose nothing.** `cmdGoesToPane` is
  `CMD_TO_PANE.has(e.code) && focusedPaneKitty()`, so a pane that never
  asked for the kitty keyboard protocol still leaves ⌘A to the browser.
- **The browser's select-all never selected terminal text here.** Panes
  are canvases. With a pane focused, `document.activeElement` is the
  invisible text sink (`42-textsink.js`), so select-all selected an
  empty textarea.
- **Text inputs are untouched.** Every real input (dialogs, picker,
  palette, settings, worktree prompts) lives under `uiOpen()`, and chat
  returns first in `onKey`. Both paths return before the ⌘ gate.
- **Kitty-protocol panes other than ced** (another editor, fish 4) now
  get super+a instead of a no-op browser select-all, which they can
  bind or ignore.

### What changed (cats `bd18905`, not pushed)

- `"KeyA"` added to `CMD_TO_PANE`, plus a paragraph under the set's
  existing ⌘E note recording the cost check and the delivery reasoning.
- No JS test covers the set (there wasn't one for ⌘E either). The cats
  Go tests for `./cmd/catway/...` and the 12 `jstest/*.test.mjs` files
  pass.

### Still owed: a hand check in both hosts

The 20-keys.js header says to test new entries in a real browser,
because ⌘E looked fine on paper and Chrome never dispatched it (a
native menu item resolved before keydown). ⌘A was not pressed in a real
host this session. Expected to deliver:

- **Chrome:** Edit ▸ Select All is a menu item like ⌘S / ⌘P / ⌘F, which
  Chrome dispatches to the page first and which were confirmed on
  2026-08-14.
- **Mac app:** `menu_darwin.m`'s Edit ▸ Select All is a nil-target
  `selectAll:`, the same shape as Copy / Undo, and ⌘C / ⌘Z already
  reach kitty-protocol panes. The caveat is that the same app's menus
  catch ⌘+ / ⌘- / ⌘0 before the page sees them.

If either host keeps the chord, the failure is benign: ⌘A is inert in
ced there, never wrong.

### Commit trailer

The cats commit carries a `Co-Authored-By` trailer, matching recent
cats history. ced's CLAUDE.md "no Co-Authored-By" rule was read as
ced-scoped. The user was told and can ask for an amend.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: N-026 (cats side landed in `bd18905`; only the hand check in
Chrome + the mac app remains). Full list: `ai_docs/todo/next-list.md`.
