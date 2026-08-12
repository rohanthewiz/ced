# Session: cats-native plan, Phase 3.2 — the Problems panel

- Date: 2026-08-12
- Branch: `main`, commits `c9f4a33..5a5415e` (2 commits + this doc)
- Plan: `ai_docs/cats-native-plan.md` §Phase 3.2 — now marked ✅ done
- Predecessor: `2026-0812-1707-cats-native-phase3-1.md`

## What was asked

Continue with Phase 3.2 of the ced × cats plan — the Problems panel,
which the previous two session docs both flagged as the natural
breather because Phase 1 had already stamped the diagnostic status
segment as a click target and left it inert, waiting for this.

## What landed

One feature commit plus the plan update. `make test` (race) green.

### New: `internal/app/problems.go` + `problems_test.go`

Every diagnostic the language server has published, across every file
it has published for, as one clickable worklist in the bottom strip.

**The four decisions worth remembering**

1. **It docks at the BOTTOM, not in find-all's band** — a deliberate
   deviation from the plan's "styled after find-all's dock machinery".
   Both of find-all's docks exist to keep a list near the code it
   points at inside ONE file: the top strip is short, the right column
   is 62 cells. A problem row is `path:line │ message` — wide, prose,
   belonging to no particular file. So it takes the bottom strip and
   mirrors `gitlog.go`'s shape rather than sharing its code, which is
   the same choice gitlog made against gitpanel and for the same
   reason: panels evolve apart, and the house patterns are the shared
   part. Single occupancy is wired both directions (five sites).
2. **It is never a modal, not even briefly.** Diagnostics are ambient
   — you asked no question — so nothing here may own the keyboard.
   That is the one real difference from find-all, whose list starts
   life as a peek popup and only becomes furniture when pinned.
3. **The selection survives by IDENTITY** (`path:line:message`), never
   by index, across both filter toggles and publishes. A publish
   inserts rows above you; an index-preserving refresh would silently
   move the highlight onto a different problem. The key deliberately
   omits the column, because a diagnostic's range shifts by a
   character as you type on its line.
4. **Quick fix jumps first, and the jump is the mechanism.**
   `codeAction` is asked about a range in the ACTIVE document and its
   handler drops answers for a file the user has left; moving the
   caret onto the diagnostic is also what makes `diagsForRange` echo
   that exact diagnostic back as request context, which is how a fix
   finds the problem it fixes. So the row is three lines of code on
   top of machinery that already existed.

**Smaller pieces**

- **The scope chip is not in the spec** and closes a real gap: the
  status segment counts the ACTIVE FILE while the panel lists the
  project, so a user who clicks "✗ 2" and sees eleven rows needs one
  click to reconcile them. Counts honor scope but never severity — a
  hidden bucket's count is the argument for unhiding it.
- **Next / Previous problem** are what keep this from being a surface
  a click-eating terminal can only look at. They walk the panel's list
  from wherever the CARET is, treating the whole project as one
  document (the (path, line, char) sort makes each file's problems
  contiguous, so "after" is well-defined across a file boundary), work
  with the panel closed, honor the filters, and refuse at the ends with
  a flash rather than clamping.
- The caret is converted INTO LSP coordinates for that comparison
  (`lspPosFor`) rather than the rows out of them — converting a row
  needs its file open, converting the caret needs only the buffer it
  is already in.
- **Reuse over building**: `editorContextModal` for the right-click
  menu (its rows are already plain `func(*App)` with predicates),
  `rowLabelText` for front-truncating paths, `diagSeverityColor` and
  the `✗ ⚠ ℹ` vocabulary so the count you clicked and the rows you
  landed on are visibly the same thing.
- Deliberately NOT built: a filter text box. It would need a focus
  model in a panel that has none by design; the severity chips are the
  filter the spec asked for.

### Wiring (all one-liners in existing seams)

`problemsState` on App · `editorBandRows` subtracts the height · one
mouse-press case, one drag-continuation branch, one `scrollAt` branch,
one right-click branch ahead of the editor menu · one draw call with
the bottom panels · three ≡ Code rows · `Esc !` · `growBottomPanel` /
`shrinkBottomPanel` · `handleLSPDiags` and `handleLSPExit` refresh the
panel when it is open.

## Conventions / gotchas worth remembering

- **`refreshProblems` is NOT free**, so the publish path guards it with
  `if a.problems.open`. A closed panel's rows are allowed to go stale
  because the only other reader — next/previous — refreshes first.
- **Panel-relative coordinates in tests.** Four tests failed first run
  by using raw screen x (4, 6) where the panel starts at `leftBlockW()`
  with the sidebar open. Always `px, py, _, _ := a.problemsRect()`.
- **A live flash owns the whole left side of the status bar**, so a
  test asserting on status segments has to clear `statusUntil`/
  `statusMsg` first — `openFile` is enough to raise one.
- **Menu row counts are pinned in four places** (`app_test.go`:
  `TestMenuLayout_NoCustomActions` ×3 numbers + its divider list,
  `TestMenuLayout_WithCustomActions`, and two `a.height = 130` values
  in the modal-rect tests that must exceed the layout height). Three
  new rows meant six edits.
- gopls still flags `../ced` files as "not in workspace" when the
  editing session is rooted in cats — noise; `go build ./...` and
  `make test` in ced are the real checks.

## State / next steps

- Phases 1, 2, 3.1 and 3.2 all done (2026-08-12).
- Next per §7: **Phase 4 — the git suite**, starting with 4.1's push
  dialog ("never type the current branch").
- Phase 3.3 (hover on mouse dwell) is Tier-1 only and really belongs
  with Phase 5; 3.4 (recent-files picker) is an hour's work whenever a
  breather is wanted.
- Still on the table from Phase 2: clicking a tab's `⚠` marker should
  re-raise a deferred conflict prompt.
- Untouched follow-ups for this phase: no filter text box (above), and
  the panel was verified against fixtures and the simulation screen
  rather than a live gopls session — the diagnostics plumbing it reads
  was already covered by `lsp_test.go`, but a real-server pass would
  confirm the assumptions about `Source` being populated and messages
  arriving multi-line.
