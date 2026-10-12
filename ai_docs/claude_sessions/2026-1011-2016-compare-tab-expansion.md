# 2026-10-11 20:16 — Diff views keep tab indentation (N-059)

Session: `798c8d3b-d8fb-4ea7-aa03-4c595d7df80f`

## Request

From the cats-todo backlog, next-list item **N-059**: the compare panel
(and the git panel's diff, which shares the row painter `drawAt`) drew a
TAB as one cell, so tab-indented code — every Go file — lost its
indentation depth: `\t\treturn nil` read as ` return nil`. Seen in the
real binary on a Go conflict's Compare sides. Expand tabs (the hunk popup
uses 4 cells) before clipping, keeping the leading `+`/`-`/space column.

## What changed

**One shared expander** (`internal/app/gitpanel.go`, beside
`gitPanelDiffStyle`)
- `diffDisplayLine(line)`: drops a trailing `\r`, then expands tabs to
  stops every `hunkTipTabWidth` (4) cells — the hunk popup's width, so
  every diff surface indents alike.
- The `+`/`-`/space column is kept verbatim and stops are counted from
  the cell AFTER it, so code lines up with its own tab stops (a mid-line
  tab before an aligned comment lands where the editor puts it) and the
  prefix never eats the first stop. Lines with no prefix column (`@@`
  headers, `git show` message lines) expand from cell 0.
- Tab-free lines return untouched (no allocation).

**Wired into all three painters, BEFORE the clip** (the clip counts cells)
- `drawComparePanel` (compare.go) — compare panel body.
- `drawGitPanelDiffRow` (gitpanel.go) — git panel diff.
- git log detail rows (gitlog.go) — `git show` output carries the same
  tab-indented diff lines.

Nothing else reads these lines by screen column: the git panel's hunk
chips are right-edge anchored, and jump-to-row maps by line index
(`diffTargetLine`), so geometry is unaffected. Style is still chosen from
the RAW line (prefix detection unchanged).

**Not changed**: `hunkTipLines` (hunktip.go) still does a fixed
`ReplaceAll("\t", 4 spaces)`. Identical for leading indentation; only
mid-line tabs differ slightly. Left alone to keep the change scoped.

## Tests

- `TestDiffDisplayLine_ExpandsTabsAfterThePrefixColumn`
  (gitpanel_test.go): prefix column kept, two tabs → 8 cells, mid-line
  tab pads to the next stop (`+ab\tc` → `+ab  c`, `+abcd\te` →
  `+abcd    e`), no-prefix lines from cell 0, CR dropped, empty line.
- `TestComparePanel_KeepsTabIndentation` (compare_test.go): renders a Go
  diff through the real panel; `+        return nil` and the context line
  `     if x {` reach the screen. Verified FAILING with compare.go's change
  stashed, passing with it.
- `make test` (race) green across all packages.

Not driven in the real binary this session — hence N-059 moves to
Validate rather than Closed.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: N-059 → Validate.
Updated: N-059. Full list: `ai_docs/todo/next-list.md`.
