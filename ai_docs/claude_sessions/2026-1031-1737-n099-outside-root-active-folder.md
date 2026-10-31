# N-099: a file outside the root never makes its folder active

Session: `83af9ce5-c3a7-4c61-a055-2bed86b693df`

## Ask

1. From the cats-todo backlog (pasted): next-list item **N-099**: a
   go-to-definition into a file outside the root makes that folder ACTIVE,
   so the ≡ folder rows ("Rename folder (fmt)", "Delete folder (fmt)",
   "Copy folder") go live on GOROOT's `fmt` and New file lands there. The
   suggested fix: move Open file…'s outside-root guard
   (`openFileAnywhere`) into `openFile`, which makes the wrapper
   redundant.
2. `/sw`

## Starting point

`openFile` (app.go) called `setActiveFolder(filepath.Dir(path))` for
every caller, before the tab-switch loop. Only Open file… undid it, with
a wrapper (`openFileAnywhere`, openfile.go) that saved the active folder,
called `openFile`, and put it back when the file resolved (EvalSymlinks
both sides) outside `copyToRoot()`. Every other door that can reach
outside the root (`lspJumpTo`, implementation/type definition/
references, workspace symbols, Recent files, remote opens) went straight
through `openFile`.

A sweep of `setActiveFolder`'s writers found the same leak in a second
place: `restoreSession` (folder.go) set the active folder from the
restored active tab's path. Restore does not filter tabs by root, so a
session that ended on a GOROOT tab came back with the folder verbs armed
on it.

## What changed

### One gate (app.go)

- `adoptFileFolder(path)` is now the only place a FILE's folder becomes
  active. `openFile` calls it where the old `setActiveFolder` was (so it
  covers both the new-tab and switch-to-open-tab paths), and
  `restoreSession` calls it for the active tab.
- `projectFolderFor(dir) (string, bool)` decides inside/outside, cheapest
  test first:
  - **Lexically under `copyToRoot()`**: adopted as given, with no
    syscalls. Every tree click lands here, so nothing the tree can
    already name changes behaviour. That includes a symlinked folder
    INSIDE the project that points elsewhere; the old wrapper's
    resolve-only test would have refused it.
  - **Else EvalSymlinks on both sides** (the house confinement rule).
    When the resolved dir is under the resolved root, the answer is
    rebuilt as `root + Rel(resolved root, resolved dir)`, i.e. in the
    TREE's spelling. A language server tends to answer with resolved
    paths (`/private/var/…` for a root opened as `/var/…`, the real dir
    behind a symlinked checkout). The tree's active-row highlight and
    `hasActiveSubfolder` compare strings, so adopting the resolved
    spelling of the root itself would have read as a "subfolder" and
    armed Delete folder on the project.
  - Otherwise outside: the active folder is left where it was.
- `evalSymlinksOr` moved from openfile.go to app.go beside its only
  caller.
- Comments: the `openFile` doc comment, a why-note at the call (the
  guard lives at the single entry point because a per-door wrapper is a
  door someone forgets), and the `activeFolder` field comment.

### Open file… (openfile.go)

- `openFileAnywhere` is deleted; `openPathTyped` calls plain `openFile`.
  The file header's WHAT IT OPENS paragraph now says `openFile`
  guarantees the rule for every door.

### Session restore (folder.go)

- `a.adoptFileFolder(a.tabs[active].Path)` replaces the direct
  `setActiveFolder`, with a comment on why (a session can end on a GOROOT
  tab).

## Tests

New, in `app_test.go` (with an `outsideRootFixture`: root/sub/a.go plus
a separate temp `fmt/print.go`):

- `TestOpenFile_OutsideRootKeepsActiveFolder`: first open AND re-open
  (switch to the existing tab) leave `activeFolder` and the tree's
  mirror alone.
- `TestLSPJumpTo_OutsideRootLeavesFolderVerbsDark`: the exact door from
  the report. `lspJumpTo` into the outside file lands; `hasActiveSubfolder`
  stays false and `deleteFolderLabel` doesn't name `fmt`.
- `TestOpenFile_ResolvedSpellingAdoptsTheTreesSpelling`: root opened
  through a symlink, file opened by its real path → `activeFolder` is
  `link/sub`, not `real/sub`.
- `TestProjectFolderFor_Cases`: plain subfolder, in-tree symlinked
  folder pointing outside (inside), real spelling of the root (→ the
  root's spelling), unrelated dir (outside).
- `TestEvalSymlinksOr_FallsBackToTheInput`.

New, in `folder_test.go`:

- `TestRestoreSession_OutsideActiveTabKeepsTheRootFolder`.

Verified the tests catch the bug: with the old behaviour temporarily put
back inside `adoptFileFolder`, the four regression tests above (not the
`projectFolderFor` / `evalSymlinksOr` unit tests) plus the existing
`TestOpenPathTyped_OpensAFileOutsideTheRoot` all fail. With the fix,
`make test` (race detector) passes, 25 packages ok. No real-binary run:
the tests drive the same `lspJumpTo` → `openFile` path and read the
menu's own label/predicate.

Docs: CLAUDE.md's Open file… paragraph now states the rule for every
door (`adoptFileFolder`: lexical inside, else EvalSymlinks both sides,
re-spelled under the root).

## Noticed, not done

- `pathInside` (gitstatus.go) tests `HasPrefix(rel, "..")`, so a child
  named `..x` reads as outside. Fails safe everywhere; raised as N-109.
- `hasActiveSubfolder` trusts its writers: it checks "not the root and
  exists", not "inside the root". With every file-driven writer now
  gated, nothing leaks into it; left alone.
- `gofmt -l internal/app/` flags `openfile_test.go` and
  `findalllive_test.go`, which this session didn't touch.

## Next

Closed: N-099. Declined: None. Raised: N-109.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
