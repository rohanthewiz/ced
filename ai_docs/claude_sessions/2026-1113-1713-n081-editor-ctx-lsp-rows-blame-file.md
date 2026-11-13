# N-081: three LSP rows on the editor right-click, git rows folded into a "Git…" picker, blame screen-first

Session: `b3b58ae1-b86a-45b7-91bb-8ef601e3faae`

## Ask

"Next list item N-081": the editor right-click menu carried six LSP verbs;
"Highlight symbol uses", "Go to implementation" and "Go to type
definition" aim at the clicked symbol the same way but were ≡ Code rows
only. The item's own plan: add them, and fold the git rows (Show change,
Blame, Git status, New branch) into one "Git…" picker row to keep the
popup short.

Follow-up from the user mid-session: "'Blame this line...' would be
better as 'Blame this file' — we could always run for non-visible pages
in the background for extremely long files." Then this wrap (`/sw`).

## The design

**LSP rows** (contextmenu.go `editorContextItems`): inserted after Find
references in ≡ Code's relative order — definition, references, symbol
uses, implementation, type definition — with Rename / Hover / Code
actions still closing the run (the first split stays after Code actions,
pinned by `TestEditorContext_SplitRowsAreInert`). Each calls its ≡ verb;
the right-click already moved the caret, so they aim at the clicked
symbol. "Find incoming calls…" stays ≡-only (a refinement of references,
not a new question).

**"Git…" picker row** (`editorGitPicker` / `editorGitPickerItems`): one
row, last, only in a work tree; opens `openPicker` titled
`Git · <file>:<line>` (the palette is centred, away from the click). Rows,
omitted rather than dimmed (a picker has no dim state):
Show change… (changed line only, FIRST so the picker opens on it),
Blame this file / Hide blame (saved file), Git status…, New branch….
Same verbs as the ≡ rows. Popup length in a repo: unchanged on a changed
line, +1 elsewhere (+3 outside a repo, where there were no git rows).

**Blame this file** replaced "Blame this line…" in the picker at the
user's ask: it is the Esc A toggle (`menuToggleBlame`), labelled by what
the pick does (`blameFileLabel`; "Hide blame" matches ≡ Git's wording).
Global like its other doors — stays on across tab switches. ≡ Git keeps
"Blame this line…" as the keyboard twin of clicking the column.

**Screen-first blame** (gitblame.go, new decision 5). Measured before
building: ced app.go (6,274 lines) 0.17s whole vs 0.12s for a 45-line
window — nothing to gain; duckdb's gram.cpp (32,182 lines) 3.4s whole vs
0.14s mid-file / 1.85s at lines 1–45. So:

- A FRESH blame (nothing installed for the path) of a buffer over
  `blameWindowAbove` (5000) lines runs two passes under one seq on two
  goroutines: `-L` over `blameWindow` (a screen above the viewport
  through a screen below, 1-based, clamped to git's last line — a
  trailing `""` buffer line is no line to git) and the whole file.
- `fileBlame.partial` marks the window answer; the porcelain carries
  final line numbers, so the parser already places it and leaves the
  rest zero-value. `at` now reports an empty-Hash entry as UNKNOWN (an
  uncommitted line keeps the all-zero hash), so nothing mistakes "not
  blamed yet" for "not committed".
- Ordering (`handleGitBlame`): the FULL pass always wins. `blameLanded`
  (new App map) records the seq whose full pass was handled; a window
  arriving after it is dropped, also after a nil full answer; a nil
  window deletes nothing. A parked "Blame this line…" question is
  answered by a window only if covered, else waits for the full pass.
- A partial on screen means a full pass is out, so `menuBlameCommit`
  parks instead of re-asking (a re-ask bumps the seq and discards it).
- Unanswered runs paint a muted `…` on their first row in both styles
  (`blamePendingMark`, shared). Re-blames (settle, save) stay
  whole-file: the previous column is still up.
- The column may change width once when the full pass replaces the
  window — the noted exception to "width is a property of the file".

## What changed

- `internal/app/contextmenu.go`: three LSP rows; git rows → "Git…" row;
  `editorGitPickerItems`, `blameFileLabel`, `editorGitPicker`.
- `internal/app/gitblame.go`: `blameWindowAbove`, `blamePendingText`,
  `fileBlame.partial`, `at` / `pendingAt`, `loadFileBlameRange`,
  two-pass `requestFileBlame` + `blameWindow`, `handleGitBlame`
  ordering + parked question, `blamePendingMark` in both painters,
  `menuBlameCommit` park; header now "Five decisions".
- `internal/app/app.go`: `blameLanded` field.
- `internal/app/hunkview.go`: `hasHunkAtCaret` comment.
- README: Editor right-click table (three rows; `Git…` replaces four),
  change-bar prose ("right-click → *Git…* → *Show change…*"), ≡ Git
  `Show blame` row notes the long-file behaviour.
- CLAUDE.md: symbol uses + leaders line, hunk-view doors, blame rules.

## Tests

- contextmenu_test.go: `TestEditorContextSymbolRows` (order, live with a
  fake server, each row's own wire method at the clicked symbol),
  `TestEditorContextGitRow` (was NewBranchRow), `TestEditorGitPicker_UntitledBuffer`,
  `TestEditorContextGitStatusRow` via the picker,
  `TestEditorContextBlameFileRow` (toggle on, "Hide blame", off).
- hunkview_test.go: `TestEditorContextShowChangeRow` via the picker, first
  row, picker opens on it.
- gitblame_test.go: `TestFileBlameAt_UnansweredLineIsUnknown`,
  `TestBlameWindow_LongFilesAroundTheViewport`,
  `TestHandleGitBlame_FullPassWinsOverTheWindow`,
  `TestHandleGitBlame_ParkedLineWaitsForItsPass`,
  `TestMenuBlameCommit_WaitsForTheRunningFullPass`,
  `TestBlameAnnotations_MarkUnansweredRuns`,
  `TestRequestFileBlame_LongFileTwoPassesEndToEnd` (real repo, 5,200
  lines: one window + one full answer under one seq; full stays).
- `make test` (race): all 26 packages ok.

Real binary (`run-ced`): right-click in version.go with gopls up shows
the nine LSP rows live and "Git…" last; the picker reads
`Git · version.go:15` with Blame / Git status / New branch. On duckdb's
gram.cpp, ~3s after Esc A lines 1–76 were blamed and line 77 showed `…`;
a few seconds later the whole column had filled in.

## Next

Closed: N-081. Declined: None. Raised: N-143.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
