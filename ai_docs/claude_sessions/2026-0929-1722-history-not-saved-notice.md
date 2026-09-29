# Session: Say so when the history can't be saved

Session ID: 63939c12-bf96-427d-852e-92c8113c07dc
Date: 2026-09-29

(Same Claude session as `2026-0929-1710-select-all-file-row`.)

## 1. The prompt

From the cats-todo backlog — next-list item N-028:

> A read-only checkout (or unwritable `.ced/`) silently loses that
> session's folder + recent-file history — `writeHistory` is silent by
> design. A fallback location, or one flash at startup, would say so.

## 2. What already existed

- `writeHistory` (recentlocations.go) runs only on Close, gated by
  `historyPersistsFn` (N-027), and discards the error — there is no screen
  left to report on.
- `repoHistory` already flashes a failed LOAD (not `ErrLocked`), so an
  existing but unreadable database was not silent. The silent case was a
  fresh read-only checkout: empty load, no error, then a Close write that
  fails unseen.

## 3. Decision

Neither suggested option:
- **Fallback location** — would be a new dotfile location, which
  CLAUDE.md's "What NOT to add" forbids.
- **Startup flash** — CLAUDE.md: "Startup errors are HELD for a ≡ label,
  not flashed" (a startup flash scrolls past).

Instead: probe once, hold the answer, show it on the ≡ rows that face the
history.

## 4. What changed

- **`history.WriteProblem(dbPath) error`** (db.go): answers by doing, not
  by mode bits (which miss read-only mounts, ACLs, uid):
  - `.ced/` missing → `MkdirTemp` beside it, removed;
  - `.ced/` present → `CreateTemp` inside (bytdb's lock sidecar lives
    there), removed; + database present → `OpenFile(O_WRONLY)`, no trunc;
  - `.ced` a file → reported.
  Errors re-labelled (`renamePathErr`) to name `.ced/` or the database,
  never the probe's temp name; the OS cause is kept for `errors.Is`.
- **App** (recentlocations.go, recentfiles.go, app.go):
  - `historyWriteProblem()` — probed on first ask (first ≡/palette draw,
    never at startup), held in `historyProbed` / `historyNoSave`. Skipped
    when `historyPersistsFn` says no: a non-repository root is memory-only
    BY DESIGN (N-027) and never labelled. Not re-asked; `writeHistory`
    never consults it, so a mid-session fix still saves on Close.
  - ≡ Nav "Recent files…" / "Recent locations…" switched to `labelFor`
    (`recentFilesLabel`, `recentLocationsLabel` → `historyRowLabel`),
    reading "… (not saved)"; predicates `hasRecentFilesRow` /
    `hasRecentLocationsRow` keep them clickable with nothing to list.
  - Clicking flashes `historyNoSaveMsg` ("History won't be saved this
    session: can't create .ced (permission denied)") instead of the
    "fills in as you open files" line; with a list, the picker opens and
    the reason rides in the status bar.
- Menu row count unchanged (labels only), so no pin updates.

## 5. Tests

- `TestWriteProblem_WritableLeavesNothing`, `_ReadOnlyRoot`,
  `_ReadOnlyCedAndDatabase`, `_CedIsAFile` (history/db_test.go).
  Permission tests skip under root (unsatisfiable environment).
- `TestHistoryNoSave_WritableSaysNothing`, `_ReadOnlyCheckout`,
  `_ReasonAlsoWhenListing`, `_NotARepositoryIsNotAProblem`
  (app/recentlocations_test.go), via a `readOnlyHistory` helper pointing
  `historyPathFn` into a 0555 temp dir.
- `make test` green. Not exercised in the real binary (run-ced).

## 6. Docs

- CLAUDE.md, Recent locations bullet: the rule above.
- next-list: N-028 moved to Closed.

## Next

Closed: N-028. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
