# N-092 + N-091: keyboard walks pick a row; ⟳ and pinned ↺ get keyboard doors

Session: `4b05934c-1702-4a67-b6a7-b2109ca1a26b` (continues
`2026-1025-2232-find-all-esc-revert-and-seam`)

## Ask

"do N-092, then N-91". Partway through, the user said keyboard nav should
NOT count as choosing a row. I reverted the work in progress (`git
checkout` of the three files, after confirming the tree held nothing
else). A minute later they reversed that: keyboard nav SHOULD count the
same as a single click. I re-applied the same change. The final decision
is: keyboard walking picks.

## What shipped

### N-092 — keyboard walking counts as choosing a row

- `findAllModal.clicked` → `picked`. Set by a single row click (as before)
  AND by the list-focus walking keys ↑↓ / PgUp / PgDn / Home / End, set in
  `handleKey`'s cases rather than in `selectRow`. That way the list's own
  moves don't pick: the open's nearest-hit preview, ⟳'s re-anchor (it
  calls `selectRow`), and a dismissal sliding the highlight.
- `escape`: picked + in-file → accept (stay), else abort. So Esc restores
  only when the user opened the list and touched nothing. `↺ revert loc` /
  `r` is the way back after a pick. Project mode unchanged (Esc always
  aborts there, which already keeps the user where a click took them).
- Footer hint "esc back" → "esc stay" follows `picked`.
- Tests: `TestFindAll_ArrowsPreviewAndEscRestores` →
  `…AndRevertRestores` (walks, then `r`). `TestFindAll_AbortRestoresScroll
  WithoutSnapping` drops its End, so Esc is still the abort.
  `TestFindAll_ArrowWalkLeavesEscAsRestore` → `TestFindAll_ArrowWalkPicksTheRow`
  (every walking key). New `TestFindAll_EscWithoutAPickRestores` and
  `TestFindAll_RerunAndDismissDoNotPick`.

### N-091 — keyboard doors for ⟳ and the pinned ↺

- `a` ("again") re-runs ⟳ in list focus (`r` is revert). It is in the
  widest in-file and project footer hints, and the button tip's keys are
  `a` unpinned.
- ≡ Find "Find all: revert location" (`menuFindAllRevert`, under "Find
  all in file", `alwaysTrue`). It runs `revertLocation` on
  `findAllVisible()`, so a pinned panel restores and stays up. With no
  list it flashes "Revert location: no Find all list is open". Unpinned
  it is unreachable by design (the list owns the keyboard, and a click
  outside accepts it), and `r` is the door there. The pinned ↺'s tip
  names the row.
- Menu pins: 206 rows (2 + 189 actions + 15 headers), height 212,
  dividers `[2, 5, 209]`, custom-actions height 215, and the two "tall
  window" tests now use 212 rows. CLAUDE.md's pin line was updated to
  match.
- Pinned ⟳ ◆ ✕ remain mouse-only: the pinned panel is mouse-driven, and
  its footer hint says so.
- Tests: `TestFindAll_RerunKeyIsTheKeyboardTwin`,
  `TestFindAll_MenuRevertRowServesThePinnedPanel`,
  `TestFindAll_MenuRevertRowExplainsWithNoList`; the button tip test
  checks `a`, then pins the list and checks that the revert tip names the
  ≡ row.

### Docs

- README Find-all paragraph: "picked a row (clicked one, or moved with
  the arrow keys)", `r` / the ≡ row for revert, "`⟳` (or `a`)". Re-wrapped.
- CLAUDE.md Find-all bullet: a PICK = row click or keyboard walk, set at
  the gestures, not in `selectRow`.

## Verification

- `make test` (race) green.

## Next

Closed: N-091, N-092. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
