# 2026-10-11 21:59 — Next-list run: cherry-pick filter, folder tab groups, log-tick order fix

Session: `9b98e626-fd19-476b-bfc8-32905ef1963e`

## Request

`/work-next-list`, third batch (after
`2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks`).

## What changed

**N-048 fix — tick order** (`gitlogticks.go`, `ea34914`)
- The previous batch's doc claimed a plain `git rev-list` walk never
  lists a parent before its child. False here: every ticked hash is a
  START point, so a ticked parent sits in the date queue from the start
  and can win a same-second tie. `TestGitLogTicks_CherryPickEndToEnd`
  caught it on a later run (s3 applied before s1). Now
  `rev-list --topo-order`, still streamed and stopped once every tick is
  seen; held over `-count=15`. CLAUDE.md and the N-048 closure updated.

**N-051 (part) — cherry-pick dialog type-to-filter** (`cherrypick.go`)
- The tree's type-to-find model: printable runes (no Ctrl) narrow by
  subject / short hash / author, case-insensitive; Space still ticks;
  Backspace edits; Esc clears the filter, a second Esc closes.
- A VIEW over the commits: `m.view` (indices) built lazily by `vis()`
  (`viewBuilt`, so tests or a load that set `commits` directly never
  read a stale view). `cursor` still indexes commits (-1 when nothing
  is visible); `scroll` is a view position; mouse rows map through the
  view.
- Select all / none acts on visible pickable rows only; hidden ticks
  are still picked and counted ("N ticked hidden") in the summary,
  which also says "type to filter" when the list is longer than the
  tallest dialog.
- Tests: `TestCherryPickModal_TypeToFilter`,
  `TestCherryPickModal_FilterMouseMapsThroughTheView`.
- Left in N-051: merge commits (`-m` parent choice is a real question)
  and a per-commit diff preview.

**N-042 (part) — folder tab group from the tree** (`tabgroups.go`, `modals.go`)
- `promptNewTabGroup` accepts `t == nil` for a FOLDER group (a rule
  needs no starting tab).
- Tree folder rows (not the root) get "New tab group…", or "Tab group
  <name>…" (the group's own menu) when a group already claims that
  folder — `tabGroupFolderItem`, `tabGroupForFolder`.
- ≡ twin: File → "Tab groups…" ends with "New folder group: <active
  folder>…" when the active subfolder is unclaimed, and now opens even
  with no groups if that row exists.
- Tests: `TestFolderTabGroup_TreeRowAndMenuTwin`; the folder-context
  and `TestMenuTabGroups` pins updated. README + CLAUDE.md updated.
- Left in N-042: choosing a group's colour; a coloured underline.

## Commits

- `ea34914` Git log ticks: order the set with rev-list --topo-order (N-048 fix)
- `139b48d` Cherry-pick dialog: type to filter the list (N-051, part)
- `1c4d554` Tab groups: start a folder group from the tree's folder menu (N-042, part)

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-051, N-042, and the N-048 closure line.
Full list: `ai_docs/todo/next-list.md`.
