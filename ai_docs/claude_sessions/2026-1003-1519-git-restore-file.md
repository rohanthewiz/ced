# Session: Git restore for one file

Session ID: `80520c18-55f0-485c-acce-11f1881898a8`
Date: 2026-10-03

## Request

From the cats-todo backlog: "Under a file's context menu, and main menu |
git, add 'restore' to abort a file's changes."

## What was built

One verb, `restoreFile(path)` in the new `internal/app/gitrestore.go`,
with three ways to reach it:

| Door | Label | Acts on |
|---|---|---|
| File tree right-click (`openTreeContext`, modals.go) | `Git restore…` (files only, only in a repo, under Delete) | clicked file |
| Tab right-click (`tabContextItems`, tabcontext.go) | `Restore (discard changes)…` (after Validate file, enabled `inRepo`) | clicked tab, in place |
| ≡ Git (app.go, after Unstage file) | `Restore file (discard changes)…` (enabled `hasGitFileTab`) | active tab |

### Flow

```
restoreFile(path)
  │  probeGitRestore — sync: status --porcelain, cat-file -e HEAD:./name, diff --numstat HEAD
  ├─ untracked / new (code set, !InHead) → flash "… is new — no committed version (Delete removes it)"
  ├─ clean, tab clean                    → flash "No uncommitted changes in …"
  ├─ clean, tab dirty                    → confirm → adoptRestoredFile (buffer-only ReloadUndoable)
  └─ changed                             → confirm (sized loss) → runGitRestore
                                             git checkout HEAD -- path
                                             onOK: adoptRestoredFile → ReloadUndoable + clearConflict
```

### Design decisions

- **`checkout HEAD --`** = `restore --source=HEAD --staged --worktree`;
  identical to the git panel's Discard (`gitPanelDiscard`), so both
  surfaces mean the same thing, and it works on pre-2.23 git.
- **Confirm sizes the loss**: `+N −M lines`, or binary/unsized wording;
  extra lines for staged changes and the open tab's unsaved edits; the
  "One Undo in its tab" line only when a tab is open. Uses
  `openConfirmLines` (focus on No).
- **Open tab adopts with `ReloadUndoable`** so one Undo brings the buffer
  back (unsaved edits included). Done in onOK rather than left to the
  reconcile tick, which on a dirty tab would raise a conflict about ced's
  own write.
- **Reconcile hold**: `formatRunBegin(path)` before git, `formatRunEnd`
  in BOTH onFail (returns false → generic error modal) and onOK. Reused
  the formatter's in-flight counter — same meaning ("ced's own write in
  flight on this path"). `runGitRestore` returns early when
  `screen == nil || rootDir == ""` so the hold can't leak.
- **Probe runs from the file's own dir** with `HEAD:./name`, which avoids
  any toplevel/symlink arithmetic (macOS /var → /private/var).
- **Rows not gated on the dirty snapshot** (10s stale); the verb's flash
  answers "nothing to restore". The tree row is conditional only on
  file + repo (Paste-style conditional row).
- `tabForSamePath` = `tabForPath` + `samePath` fallback for symlinked
  spellings.
- The tree-marks multi-select picker still has NO discard/restore
  (treemarks.go's reasoning: it can't show the loss for a set). Untouched.

## Files

- `internal/app/gitrestore.go` (new): probe, `parseNumstat`,
  `restoreFile`, `restoreConfirmLines`, `runGitRestore`,
  `adoptRestoredFile`, `tabForSamePath`, `menuGitRestoreFile`,
  `ctxGitRestore`.
- `internal/app/gitrestore_test.go` (new): numstat parse, probe
  classification, end-to-end restore with dirty tab + Undo, staged reset,
  the no-op flashes, buffer-only path, hold release on git failure, all
  three doors, no tree row outside a repo.
- `internal/app/modals.go`: tree row. `tabcontext.go`: row + header
  diagram/notes. `app.go`: ≡ Git row.
- Pins updated: `app_test.go` (163 group actions / 180 rows / height 186
  / dividers `[2, 5, 183]`, custom-actions 189, Git section 26 rows,
  tall-window heights 186); `tabcontext_test.go` row order.
- `CLAUDE.md`: pin counts, tab-menu order, ≡ twin list, architecture map
  entry, new Restore rule under Git. `README.md`: tab-menu bullet + ≡
  twin.

## Verification

`make test` (race) passes. Not driven in the real binary.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
