# Session: Find in project leaves its highlights behind → a tint ledger, 0.3.6

Session ID: 735d678d-5352-4a02-a167-e14cddf63a8d
Date: 2026-09-24

## 1. The report

> The highlights from the previous find are not cleared when the find in
> project mode is dismissed and even after ESC is pressed.

## 2. Cause

Every project-mode producer (Find in project, references, the
workspace-edit receipt) navigates through `findAllModal.jumpToSelected`
(projectsearch.go), which ended with `tab.SetFindQuery(m.query)` on the
file it just opened — so the hit is lit on arrival. Nothing ever took that
back:

- `restoreFind` (findall.go) returned early in project mode, on the theory
  that project mode "never borrowed" find state. It did — once per file a
  row opened, not once per list.
- The editor's Esc side-effect block clears ghosts, carets, symbol uses and
  the tree filter, but find state only ever left through `closeFind`,
  i.e. while the find bar was open. A project tint had no bar to close.

So every file clicked through or accepted kept the query tinted
indefinitely.

## 3. Fix (`5dc0c0c`)

- **A ledger on the App**: `projFindTints map[string]projFindTint`, keyed by
  path, holding the query the list lit and the tab's PRIOR query (captured
  on the first tint of that path only, so a second click into the same file
  doesn't record the list's own tint as the thing to restore).
- `tintForProjectFind(tab, query)` replaces the bare `SetFindQuery` in
  `jumpToSelected`.
- `clearProjectFindTints()` restores each still-open tab's prior query (or
  `ClearFind`), **skipping any tab whose query has changed since** — the
  user or the find bar owns that state now. Then drops the ledger.
- Called from:
  - `restoreFind` in project mode → covers Esc on the list (`abort`), the
    pinned ✕ (`closePin`), and a fresh search replacing a pin
    (`dropFindAllPin`).
  - `openSelected` when the list closes (not pinned): clears the files only
    clicked THROUGH, then the jump re-tints the accepted file so the hit is
    still visible on arrival.
  - The editor's Esc side-effect block in `handleKey` — how the accepted
    file's tint goes away.
- CLAUDE.md's Find-in-project bullet rewritten: "Esc restores no VIEW, but
  it must take back the TINT".

Tests (projectsearch_test.go):
- `TestProjectSearch_DismissTakesBackTheTintItScattered` — two jumps, abort,
  bravo cleared and alpha's own `"alpha"` query restored.
- `TestProjectSearch_EscClearsTheAcceptedTint` — accept, then a real Esc
  through `handleKey`.
- `TestProjectSearch_TintClearLeavesAUsersNewSearchAlone` — a query set
  after the tint survives the clear.

The existing `TestProjectSearch_EscRestoresNothingBecauseNothingMoved`
still passes unchanged: a tab the list never tinted is not in the ledger.

`make test` green.

## 4. Release

ced **0.3.6**: `version.go` and `cats-plugin.toml` bumped together
(latest tag was v0.3.5), `Release ced 0.3.6` (`44320a9`), tag `v0.3.6`,
pushed `main` + tag. Hand flow only — no GitHub Release / install.sh
update (still N-001, Roadmap).

## 5. Notes

- Esc in the editor while a PINNED project list is open also clears the
  tints the pin scattered. Deliberate: the next click on a row re-tints
  that file, and a pinned worklist has no other "drop the highlight"
  gesture.
- A new search opened over an unpinned list (the prompt replaces the modal
  through `openModal`, which runs no `restoreFind`) leaves the old tints in
  the ledger until the next dismissal or Esc — which then clears all of
  them, since the ledger is App-wide.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
