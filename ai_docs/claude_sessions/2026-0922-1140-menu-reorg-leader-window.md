# Session: "file:" title prefix, leader window, ≡ menu reorganization

Session ID: 96fc5810-b6cc-4441-8352-5d091f1f62bf
Date: 2026-09-22

## 1. Host title prefix (committed 25e7bea)

> When CEd sets the tab title from a filename, prefix it with "file:"

- `internal/app/hostident.go`: new `hostIdentTabName(path)` →
  `file:<base>`; the OSC 2 title reads `file:main.go · ced`
  (`● file:main.go · ced` when dirty).
- Untitled buffers stay `untitled` (no filename to vouch for); the
  no-tab workspace title stays the bare folder name.
- OSC 2 is the only place ced sets a title (checked cats glue — no
  control-socket rename).
- Tests: `TestHostIdentTabName`, lifecycle test strings updated.

## 2. Esc P (Find in project) typed a literal P (committed ca19ea1)

> The chord for "find in project" - Esc+P is not working

Diagnosis path:
- `handleKey` unit repros (Esc,P / Esc,P+Shift / Alt+P) all opened the
  prompt. Real binary over a pty (run-ced capture) also worked.
- tcell 2.13.9 does NOT enter DCS on `ESC P` (only `[ ] N O X ^ _ \`),
  so the CLAUDE.md "P is unbindable" lore doesn't apply to tcell itself.
- Host = cats mac app (GUI + libghostty encoder, not a terminal), so no
  outer terminal eats it. User reported: a `P` gets typed.
- Built a throwaway tcell key logger (scratchpad) and had the user run it
  in a cats pane — note `!` in Claude Code has no tty, it must run in a
  real pane. Result: clean `Esc` then `P` **1125ms** apart; `Esc`→`p`
  was 432ms. Cause: the leader window was `doubleEscMs` (500ms).

Fix (owner chose "all leaders, ~1.2s" over shifted-only):
- New `leaderWindow = 1200ms` in `internal/app/app.go` for the Esc→rune
  gap. Double-Esc (≡ menu) stays 500ms; CHAINED repeats (`Esc z z z`)
  also stay 500ms, since the chain is re-armed by the action, not by an
  Esc, and stretching it turns a word starting with z into undos.
- Tests: `TestHandleKey_ShiftedLeaderAfterSlowReach`,
  `TestHandleKey_ChainWindowStaysShort`; `LeaderTimesOut` backdates past
  `leaderWindow`.
- Trade-off accepted: a bound letter typed <1.2s after a dismissing Esc
  fires the leader.

## 3. ≡ menu reorganization (this commit)

> Move Copilot, MCP, and Skills under a new top-level item "AI"… organize
> the menu like an app's top menu, only vertical.
> (Follow-up: rename Go → Nav, and put Compare under File.)

New top level: **File · Edit · View · Find · Nav · Code · Git · AI ·
Tools**, then Quit.
- File: + Save, Save & close tab, Close tab, Revert, auto-save toggle
  (after New file/folder). **Compare** is a sub-section of File.
- Edit: Undo/Redo at the head (old History group dissolved).
- Find: was "Search"; Go to line moved out; Find file stays beside
  Find in project (p/P adjacency).
- Nav: was "Navigation"; + tab switching, Recent files, Go to line,
  Go to matching bracket, Go to terminal output location (the two
  no-server jumps left Code; LSP jumps stay in Code).
- AI (no rows of its own): Copilot, MCP, Skills sub-sections.
- Tools (no rows of its own): Notes, Plugins, plus spliced Cats /
  Plugin commands / Custom. Must stay the LAST parent before Quit.

Mechanism:
- `menuGroup.parent` (one level, flat list — palette and tests still walk
  one slice); `menuItemDef.depth` stamped by `menuLayout`; a folded parent
  hides children's headers too; nested rows draw 2 cells right.
- `menuSectionParent`; `openMenuAtSection` unfolds the parent first (the
  Copilot status-bar door).
- `drawMenu` now `elide`s labels to the fixed modal width (the indent
  pushed "Block agent file changes (read-only chat)" into the border).
- Geometry pin unchanged (163 rows, height 169, dividers [2,5,166]) —
  header count happened to stay 15.
- `TestMenuLayout_TerminalRowsAboveTheFold` re-scoped: in the collapsed
  default every top-level header fits in 24 rows, and terminal rows fit
  with only View unfolded.
- New tests: `TestBuiltinMenuGroups_ChildrenFollowParent`,
  `TestMenuLayout_FoldedParentHidesSubsections`,
  `TestDrawMenu_SubsectionIndented`,
  `TestDrawMenu_LongNestedLabelStopsAtBorder`; `TestOpenMenuAtSection`
  asserts AI unfolds.
- CLAUDE.md: menu-bar order + nesting rules documented; references to
  Navigation/Search/Code rows/Compare/Notes/Skills sections updated.

## Open item

- Pre-existing: inside cats, the Cats section is spliced in AFTER
  `seedMenuFoldDefault` runs, so it starts expanded and the toggle reads
  "Collapse all" on first open. Proposed fix: treat sections unseen at
  seed time as folded (a default-fold flag consulted by
  `sectionCollapsed`). Not done — offered to the user.
