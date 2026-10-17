# 2026-10-16 21:11 — Recent folders: local half + cats workspace routing

Session: `30a8f166-bc0c-4946-aa24-bb6e7a51edcf`

## Requests

From the cats-todo backlog, then a follow-up:

1. The tree's ⭑ popup lists recent GLOBAL folders; add LOCAL folders at
   the bottom too.
2. Clicking a global folder: go to its cats workspace, to any existing ced
   rooted at that folder, else open a new ced rooted there in that
   workspace. If the workspace is not active, open the ced in the current
   workspace.
3. (follow-up) Have cats report each workspace's cwd, so the workspace
   match is exact instead of inferred. Companion cats session doc:
   `~/projs/go/cats/ai_docs/claude_sessions/2026-1016-2111-workspace-list-cwd.md`.

## What was built

### Local half of Recent folders (`internal/app/folder.go`, `recentlocations.go`)

- `menuRecentFolders` (the ⭑ and its ≡ File twin): global rows (ced's
  own recents, then `· cats` frecency rows), a spacer, then
  `recentFolderLocalRows` — Recent locations' top level, recent then
  frequent as ONE section (its inner spacer dropped so the picker has one
  divider), each label prefixed `./`. Local rows keep `pickLocation`
  (reveal, or drill when `›`), never re-root.
- Picker sized with `locationRows` so the bottom half isn't below the fold.
- `hasRecentFolders` is true on the local half alone; the empty early-out
  checks it too.

### Tier-1 routing (`internal/app/catsworkspace.go`, new)

Global rows now call `openRecentFolder`: Tier 0 → `requestOpenFolder`
(re-root, unchanged); Tier 1 → `catsGoToFolder`, a goroutine doing
pane.list + workspace.list, then `planFolderRoute` (pure) and
`runFolderRoute`, ending in one `catsPostNotice`:

1. a ced (`Agent` "ced", not our pane, same host) whose cwd IS the folder
   → `agent.focus` (reveals workspace + tab; falls back to pane.focus).
   Several → the one in the folder's workspace, then lowest pane id.
2. the folder's workspace, awake and unlocked → `tab.create` with
   `workspace` set, then `agent.focus` on the new pane (tab.create into
   another workspace doesn't move the view).
3. else → `tab.create` in the current workspace.

Workspace match, ranked: 3 = `workspace.list` `cwd` equals the folder
(skipped for a workspace pinned to a named other host); 2 = name equals
the folder label (basename, `~` for home) AND a pane at/under it; 1 = a
pane exactly at it. Never the name alone. Ties → workspace order.
A ced pane's cwd is its root because ced emits OSC 7 for the root at
startup and cats stops probing process cwd after an OSC 7.

`catsProjectTabParams` is now shared with `catsOpenProjectInTab`.

### cats client (`internal/cats/client.go`)

`MethodWorkspaceList`, `MethodAgentFocus` (own const block — no realign
churn), `WorkspaceEntry` (incl. `Cwd`), `WorkspaceList()`, `AgentFocus()`,
`PaneInfo.Host`, `TabCreateParams.Workspace`.

### cats side (other repo)

`WorkspaceEntry.Cwd` (`json:"cwd,omitempty"`) = the stored
`ws.IdentityCwd`, in `wire/vocab.go` + `internal/app/query.go`, with a
test and a control-api.md paragraph.

### Docs

README (Recent folders paragraph), CLAUDE.md (star rule: halves +
routing ladder + match order).

## Verification

- New tests: `catsworkspace_test.go` (every rung, editor tie-break, self
  excluded, strong/weak/identity ranking, name-alone refusal, asleep /
  locked / other-host fallbacks, three end-to-end runs against the spy,
  Tier-0 re-root), folder_test (halves + gate), recentlocations_test
  (one marked section), client_test (workspace.list / agent.focus / tab
  workspace wire shapes). ctlSpy answers workspace.list and tab.create.
- `make test` (race): everything passed except
  `TestCommitPromptTrailer_CommitsWithAndWithoutIt`, a 3s wait that timed
  out under full-suite load and passed 3/3 alone; after the cwd change
  `go test -race ./internal/app ./internal/cats` passed.
- cats: `go test ./...` passed.
- Not run in a real cats (N-071).

## Next

Closed: None. Declined: None. Raised: N-071.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
