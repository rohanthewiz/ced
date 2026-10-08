# 2026-10-08 17:37 — Anchored popups share one width rule (N-058)

Session: `bbc75950-daae-40f1-bf27-8292f7bc1b99`

## Request

From the cats-todo backlog, next-list item **N-058**: the tree popup had
been sized to its widest label (`contextMenuWidthFor`) because rows like
"Add to favorites…" ran over its right border. Check the other popups said
to still take the fixed `contextMenuWidth` (problems.go, gitlogactions.go,
conflictpanel.go, contextmenu.go:411) and size them the same way if any
overflowed.

## Findings

The premise was stale — **nothing overflowed**:
- problems.go, gitlogactions.go, conflictpanel.go — and also tabcontext.go,
  tabgroups.go and the editor menu (contextmenu.go:109) — already grew to
  their widest label. `contextMenuWidth` appeared in them only as the
  floor of a hand-copied loop, which is why a grep made them look fixed.
- contextmenu.go:411, the preview's one-row "Stop Preview" menu, was the
  one truly fixed width; the label needs 12 + 6 = 18 cells ≤ 19, so it fits.

The real issue was **seven hand copies** of the sizing loop with drift:
some clamped with `a.width > 0` guarding, others would clamp a popup to 0
columns on an unmeasured screen; one used `max()`, others `if`.

## What changed

- `internal/app/modals.go` — new `contextMenuWidthForLabel(widest int)`:
  the ONE rule (floor `contextMenuWidth`, `widest + 6` for
  border/pad/chevron/pad/pad/border, clamped to a MEASURED `a.width`; 0 =
  unknown, not zero columns). `contextMenuWidthFor` (tree chassis) now
  finds the widest label and hands it over. Header comment has the cell
  diagram.
- `internal/app/contextmenu.go` — new `editorContextMenuWidth(items)`,
  the adapter for the `editorContextModal` chassis. Used by the editor
  menu AND the preview menu (no longer fixed, so a renamed row can't
  overflow).
- problems.go, gitlogactions.go, conflictpanel.go, tabcontext.go,
  tabgroups.go — their loops replaced by `a.editorContextMenuWidth(items)`.
- `placeContext` (fixed width, test-only caller) left alone.

## Tests

- `TestEditorContextMenuWidth_SharesTheTreeRule` (contextmenu_test.go):
  floor, growth to the widest label, parity with the tree chassis, clamp
  to a narrow screen.
- `TestContextMenuWidthForLabel_UnmeasuredScreenKeepsTheFloor`
  (modals_test.go): zero `a.width` keeps the floor / grows, never 0.
- `go test -race ./internal/app` — ok (113s).

## Next

Closed: N-058. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
