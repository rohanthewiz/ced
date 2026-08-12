# Session: cats-native plan, Phase 1 — click-first discoverability

- Session id: `f410652e-531f-451a-8539-82fa56d2ed2e`
- Date: 2026-08-12
- Branch: `main`, commits `071755a..ca6e640` (7 commits, pushed)
- Plan: `ai_docs/cats-native-plan.md` §Phase 1 — now marked ✅ done

## What was asked

Continue with Phase 1 of the ced × cats integration plan. Phase 1 is
pure-ced, fully functional at Tier 0: six click-first discoverability
items, each shippable alone, each new file with a `_test.go` sibling,
`make test` (race) green per item, one commit per item.

## What landed

### 1.1 Clickable status bar — `statusbar.go` (071755a)
- `drawStatusBar` extracted from app.go and rebuilt as `statusSegment`
  spans (text + optional onClick + rect stamped at draw — the
  `lastTabRects` pattern: draw is the one geometry source, the click
  handler only reads it).
- Segments: filename → switch-tab picker · dirty ● → save · language →
  ≡ Code section · Ln,Col → go-to-line · diag counts (stamped, inert
  until Phase 3's Problems panel claims the click) · Copilot → ≡
  Copilot section · branch → branch switcher · ≡ pinned bottom-right →
  the menu.
- New `openMenuAtSection(title)`: opens the ≡ menu with a named section
  unfolded and its first enabled row hovered.
- Narrow windows drop right segments whole, least-vital first; a live
  flash still owns the left side. One new bottom-row case in the mouse
  router. Two legacy status-bar tests updated to the new right-edge
  contract (`… feat/widgets  ≡ `).

### 1.2 Editor right-click context menu — `contextmenu.go` (b4c61fb)
- `editorContextModal` on the tree menu's chassis, anchored at the
  click with flip-to-fit (`placeContextSized`). Width computed from
  labels (the search row embeds its argument).
- Click places the caret first UNLESS it lands inside the current
  selection (`posInSelection` — the selection is the argument of
  Copy/Cut/Compare). Rows dim by predicate instead of vanishing.
- Rows: Go to definition / Find references / Rename symbol / Hover /
  Code actions (existing verbs, aimed at the caret the click set) ·
  Copy/Cut/Paste · Compare selection with paste… · Search project for
  "word" (seeded via `findAllSelectionQuery` else new `wordAt`).
- compare.go gained the armed-selection mode: `selPending` snapshots
  the selection so the next paste (or ⌘V clipboard) diffs against IT,
  not the whole buffer; ⟳ is deliberately inert on the result; closing
  the panel or any ordinary compare disarms.
- Mouse router right-click order: tree → editor → ≡ menu fallback.

### 1.3 Which-key overlay — `whichkey.go` (f171da5)
- `leaderBinding` gained `label`; backfilled across the whole table +
  AI namespace (script-assisted), plugin bindings label from their
  command names; `TestWhichKeyEveryLeaderBindingLabeled` keeps future
  entries honest.
- Esc + ~350ms hesitation → bottom-anchored band of key → label pairs,
  column-major, clickable rows (stamped rects). Prefix namespaces
  re-render the band with their sub-table.
- Mechanics: one-shot `time.AfterFunc` posts a `whichKeyEvent`
  (events-only rule); a generation counter (`whichKey.seq`) kills
  ticks from Escs the user typed through.
- Passive contract: leader keys fire exactly as before; any unbound key
  dismisses then types; double-Esc (checked first) still opens the
  menu. While visible, the leader/chord windows stop expiring —
  reading must not time out. `closeAllModals` closes it.

### 1.4 Searchable ≡ menu (044cfdf)
- A rune typed while the menu is open → `menuSearchFrom(r)`: the
  palette's action source under a "Search the menu" title, query
  seeded with the typed rune. Reaches rows in collapsed sections
  (flattens `visibleMenuGroups`); file index deliberately excluded.
  Modified runes (Alt/Meta/Ctrl) stay with the leader/Cmd layers.
- Menu layout tests re-pinned (122 rows / height 129 — the new View
  row from 1.5 counted here too).

### 1.5 File-tree keyboard navigation — `treenav.go` (f3c3c93)
- filetree gained `Selected` (keyboard cursor, distinct from
  ActiveFile/ActiveFolder) + `Focused` (highlight only when the tree
  owns keys) + mechanics: `VisibleNodes`, `SelectDelta`, `ParentOf`,
  `EnsureSelectedVisible`, `SelectedIndex`.
- Esc-T (shifted twin of 't', + ≡ View row) toggles focus. Arrows
  walk; → expands-then-descends; ← collapses-then-ascends; Enter opens
  (focus follows the file into the editor); PgUp/PgDn page; letters
  typeahead-jump with wraparound; n/d/r run the context menu's
  New/Delete/Rename verbs (n resolves folder → selected dir, file →
  parent, none → root).
- Focus discipline mirrors term/chat panels: branch sits after the
  Esc/leader/menu blocks (global gestures keep working), all other
  keys claimed (nothing leaks into the buffer), clicks focus what they
  touch (sidebarClick also moves the cursor).

### 1.6 Interactive find-all (cb090dd)
- `view []int` indirection: display = rows surviving filter +
  dismissals; `selected`/`scroll` are view positions. Chrome grew one
  row (`findAllChromeRows` 4→5) for the fields row; row band starts at
  my+4.
- **Pin** ◇/◆ (single-width per the marker rule, not the plan's 📌; `p`
  key twin): the same object moves modal-slot ⇄ `App.findAllPin`
  panel. Pinned: editor keeps keyboard and clicks, panel survives
  edits, ✕ closes (esc-hint spot), single click previews,
  double-click/Enter commits with the panel staying. Wheel + press
  routing added; a focused filter/replace box claims keys via the
  find-bar pattern.
- **Filter** (`/`): live substring narrowing over text+label, document
  order preserved. **Dismiss**: per-row ✕ (last interior cell) +
  Delete key; rows marked not removed. **Re-run** ⟳: in-file sync
  re-search (resets dismissals, keeps filter); project mode re-enters
  `startProjectSearch`; heading producers (references) decline.
- **Replace in N results**: `buildReplacePlan` turns surviving,
  non-stale rows into rune-coordinate `editor.Edit`s grouped per file
  → `wsEditPlan` → `commitWorkspaceEdit` (one undo gesture, journal,
  rollback, report — the server-edit machinery without the UTF-16
  round trip). Confirm modal states occurrence/file counts, where
  bytes land, and stale rows skipped.
- **Stale dimming**: `rowStale` (match range no longer holds the query
  text) → muted row; replace skips them.
- A fresh search (in-file or project) drops any pinned survivor.

## Conventions established / worth remembering
- Status bar & which-key use the stamped-rect pattern (draw stamps,
  click reads) — the list-shaped cousin of the btnRect rule.
- `openMenuAtSection` is the door for "open the menu at X" segments.
- Panel focus trio (term/chat/tree, now find-all fields) stays
  mutually exclusive; new focused surfaces slot in after the
  Esc/leader/menu blocks in handleKey.
- Menu layout tests pin exact row counts — adding a ≡ row means
  re-pinning `TestMenuLayout_*`.
- gopls in this workspace flags ../ced files as "not in workspace" —
  noise; `go build ./...` in ced is the real check.

## State / next steps
- Phase 1 marked done in `ai_docs/cats-native-plan.md` (with the three
  spec deviations noted there).
- Next per §7: **Phase 2 — never clobber** (`reconcile.go`: conflict
  matrix, save guard that aborts on newer disk mtime, autosave
  suspension). Strictly before Phase 5's splits.
- Pulled-forward OSC 7/title emission (hostident.go) was already done
  pre-session (d2bf955).
