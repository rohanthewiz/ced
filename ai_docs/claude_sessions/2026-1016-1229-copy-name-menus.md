# 2026-10-16 12:29 — Copy name in the tree, tab and ≡ File menus

Session: `7237e3a4-f35f-43e9-9120-03e1f2554b1b`

## Request

From the cats-todo backlog: "Add Copy Name to the filetree item context
menu". Then: "also add it to the tabs right click menu".

## What was built

- **Tree right-click** (`internal/app/modals.go`, `openTreeContext`):
  a "Copy name" row directly above "Copy rel path", so the three
  clipboard rows read shortest string first (name → rel → abs). Offered
  on every node, the project root included: a folder's or the project's
  name is as pasteable as a file's.
- **≡ File twin** (`internal/app/app.go`): "Copy file name" above "Copy
  relative path", on the active tab, gated by `hasFileTab`. Required by
  the rule that right-click is never the only path (macOS Terminal +
  tmux swallow it).
- **Tab right-click** (`internal/app/tabcontext.go`): "Copy file name"
  above "Copy relative path", acting IN PLACE on the clicked tab (the
  answer is a flash, so a background tab is not brought forward), dimmed
  on a pathless tab like the copy-path rows. Header diagram updated.
- **Verbs** (`internal/app/fileops.go`): `menuCopyFileName` and
  `ctxCopyName`. Both reuse `copyPathToSystemClipboard` (OSC 52, flash
  `Copied name: <name>`). The name is `filepath.Base` of the path, not
  `Node.Name`, so a row can never hand over a display label.

## Tests

- `fileops_test.go`: `assertCopiedName` helper (accepts "Copy failed"
  where the test run has no usable `/dev/tty`, otherwise the exact
  flash), `TestCtxCopyName_FileAndRoot`, `TestMenuCopyFileName` (no-tab
  no-op, closes the menu).
- `tabcontext_test.go`: row list updated;
  `TestTabContext_CopyFileNameActsInPlace` (background tab's name, active
  tab unchanged).
- `modals_test.go`: the three tree-menu label lists gain "Copy name".
- Menu pins bumped for the new ≡ row: 176 group actions, 193 rows,
  height 199, dividers `[2, 5, 196]`, 202 with custom actions, and the
  two tall-window heights in `TestMenuModalRect_*`. CLAUDE.md's pin note
  and the tab-menu order line updated to match.

`make test` (race) passes.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
