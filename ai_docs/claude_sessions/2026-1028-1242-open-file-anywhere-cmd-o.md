# Open file… / ⌘O: open a file by typed path, anywhere on disk

Session: `54727992-4d03-4d9d-8b5d-c14c29f97f1c`

## Ask

1. "I would like to use CMD+o to open a file from anywhere on the file
   system"
2. `/sw`

## Findings

- No verb existed for opening a file outside the project. Every door was
  scoped to the project: tree clicks, the fuzzy finder (Esc-p / ⌘P) over
  the root's index, and Recent files. Open folder re-roots, which tears
  the workspace down.
- The ⌘ layer is a bonus layer: `TestMetaAccelsAreNeverTheOnlyPath`
  fails unless a ⌘ verb also has an Esc key or a ≡ row. The flat Esc
  table is out of letters, so the twin is a new ≡ File row.
- `openFile` already copes with outside-root paths. The recent-files
  ring stores them absolute (`history.encodeFile`), and go-to-definition
  into GOROOT already opens them. But `openFile` makes the opened file's
  folder ACTIVE. For an outside-root folder that arms the ≡ folder verbs
  (Rename / Delete / Copy folder) on a folder the tree can't show. The
  new door guards against this. The LSP-jump path still does it (N-099).
- cats forwards ⌘ chords only from `CMD_TO_PANE`
  (`~/projs/go/cats/cmd/catway/web/js/20-keys.js`), which holds no
  `KeyO`. ⌘O is therefore dark in browser-cats and the mac app until
  cats adds it (N-098). It is live in kitty, Ghostty and WezTerm.

## What shipped

- **`internal/app/openfile.go`** (new):
  - `menuOpenFileByPath` is both ≡ File "Open file…" (above Open folder)
    and ⌘O.
  - The prompt is seeded with the active file's folder (else the root)
    plus `/`, in display form (`~`).
  - Paths are spelled as Open folder spells them: absolute, `~/`, or
    relative to the root, via `expandOpenPath`. They resolve against
    `copyToRoot()`, never the process cwd.
  - `resolveOpenPath` refuses missing paths, folders (flagged) and
    non-regular files. A fifo read would block the main loop.
  - `openPathTyped`: a refusal re-opens the prompt holding the value,
    with the reason under the field. A folder gains a trailing `/`.
  - `openFileAnywhere` = `openFile`, except that an outside-root file
    restores the previous active folder. The inside test runs
    EvalSymlinks on both sides.
  - `completeOpenPath` is shell-style Tab completion of the last segment
    only, so the user's `~` or relative spelling survives:
    - one match → completed (folders get a `/`; symlinked folders are
      followed);
    - several → extended to their longest common prefix (never shorter
      than what was typed), with the note "N matches: …", count first;
    - none → "no match"; an unlistable folder → "can't list …".
  - Dotfiles appear only after a `.`. Exact case wins; a case-folded
    match counts only when nothing matches exactly.
- **`modals.go`**: a generic Tab hook on `promptModal`. `complete`
  returns (next, note), and `completeNote` is drawn on row 5 when there
  is no live `status`. `edited()` clears the note; `tabComplete` sets
  the note after the edit so it survives its own Tab. Every other
  prompt has `complete == nil` and is unchanged.
- **`metakeys.go`**: `{key: 'o', action: menuOpenFileByPath}`, with a
  comment on where the chord is live and the missing cats `KeyO`.
- **`app.go`**: the ≡ File row.
- **README**: an "Open file…" paragraph after the Open folder one, and
  `⌘O` in the Command-key accelerators list.
- **CLAUDE.md**: architecture-map line, a design-rules bullet under
  Workspace, and the menu pins bumped.

## Tests

- `openfile_test.go`:
  - resolve: absolute / relative / tilde; missing / folder / blank
    refusals; `/dev/null` refused as non-regular.
  - open: an outside-root open keeps the active folder; an inside-root
    open takes its folder; a mistake re-opens with the value and adds
    the `/` for a folder.
  - seeding: the seed with no tab and with a tab.
  - completion: one match and folder slash; common prefix and count
    note; dotfiles; case folding that never deletes letters; tilde
    spelling kept, bare `~` → `~/`; no-match and unlistable; symlinked
    folder.
  - `commonRunePrefix` smoke test (multi-byte).
  - an end-to-end Tab → Enter through `a.handleKey`.
- `modals_test.go`: Tab completes and keeps the note; an edit drops it,
  caret motion doesn't; Tab is inert without a completer; the note draws
  on row 5.
- `metakeys_test.go`: `TestMetaAccelOpensFileByPath`.
- Menu pins: 207 rows (190 actions), height 213, dividers `[2, 5, 210]`,
  custom-actions 216, and the two tall-window heights 213.

## Verification

- `make test` (race): every package ok.
- run-ced PTY capture of the real binary:
  - palette → "open file" → Tab on the seed listed "14 matches: AGENTS.md
    CLAUDE.md LICENSE Make…" on row 5;
  - `int` Tab → `edi` Tab → `buf` Tab gave `~/projs/go/ced/internal/
    editor/buffer` with "2 matches: buffer.go  buffer_test.go";
  - `~/.zsh` Tab, `rc`, Enter opened `.zshrc` as a tab.
- Not seen: the ⌘O chord itself in a real kitty-protocol host. The PTY
  driver sends no kitty CSI-u sequences and `metaAccelArmed` needs a
  kitty / Ghostty / WezTerm env (N-100).

## Next

Closed: None. Declined: None. Raised: N-098, N-099, N-100.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
