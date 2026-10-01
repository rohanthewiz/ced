# Session: tab right-click menu packed with file verbs

Session ID: b25a79c4-e09a-45c0-8492-923fb9a32b8e
Date: 2026-09-30

Follows `2026-0930-1930-tab-context-menu`.

## 1. The ask

"Pack the tab context menu with features relevant to the selected file."
The user's must-haves: show uncommitted changes, show git history,
compare with clipboard, move to split, copy to split, format, validate,
preview, close, zip.

## 2. What landed

**Tab menu (`internal/app/tabcontext.go`).** Rows are grouped look →
change → place → copy → close:

    Reveal in file tree · Show uncommitted changes · Show git history ·
    Compare with clipboard · Preview (markdown only; "Stop preview" when
    on) · Format file · Validate file · Move to split → · Copy to split →
    (both inside cats only) · Zip file · Copy relative/absolute path ·
    Close tab · Close other tabs

- `onTab(t, verb)` brings the clicked tab forward first (through
  `switchToTab`) for rows whose answer is a VIEW or a buffer write:
  compare, preview, format, validate. Every other row acts in place.
- Every row calls an existing verb, the same one as its ≡ twin. Only the
  three verbs with no door at all were new:
  - **`gitPanelRevealFile(path)`** (gitpanel.go): opens the changes panel
    with the file selected, after a synchronous refresh. With no changes
    it flashes "No uncommitted changes in X" (adding "unsaved edits
    aren't on disk yet" for a dirty tab) and closes a panel that was
    opened only to look. `samePath` looks through symlinks, because git
    toplevel paths are resolved (/var → /private/var).
  - **`gitLogShowFile(path)`** (gitlogfilter.go): opens the git log with a
    `p:<rel>` query (`--follow`). The field is NOT focused, so the next
    keystroke doesn't edit the path.
  - **`validateFile()`** (validate.go): re-parses ced's own kinds right
    away, skipping the debounce, then reads the merged `diagsFor`
    (LSP + plugins + ced). With problems, the caret goes to the first
    error and the flash reads "N errors, M warnings — line L: msg". With
    none, the flash names who checked. With no checker at all, it
    explains rather than giving a false all-clear.
- **Splits (`catssplit.go`).** `catsOpenInSplit` became `catsSplitTab(t,
  dir, arrow, move)`, with `catsSplitPath(t)` split out. A dirty tab is
  SAVED first, because the sibling ced reads the disk. That also changes
  the existing "Open in split" rows. Move posts the new
  `catsKindSplitMoved` (tab in `clipTab`) and closes the tab only on the
  host's answer. A tab edited in the meantime is kept, with a flash
  saying so.

**≡ twins.** Every row has one:
- File → "Validate file"
- Git → "Show file's uncommitted changes" and "Show file's git history"
- Cats → "Move to split →" (dynamic group, so no pin change)
- "Copy to split" = the existing "Open in split →"

## 3. Tests

- New tests:
  - `tabcontext_test`: row order, dims on an untitled tab, git rows dim
    outside a repo, Preview only on markdown (from a background tab),
    Validate runs on the clicked tab, Zip acts in place, split rows only
    inside cats, Copy keeps the tab.
  - `gitpanel_test`: reveal selects the file, a clean file says so,
    non-repo, `samePath`.
  - `gitlogfilter_test`: `p:` filter and commit list, refusals.
  - `validate_test`: jump + flash, clean names the checker, nothing can
    check, `diagCounts`.
  - `catssplit_test`: move saves then closes after the split, a tab
    edited meanwhile is kept.
- Menu pins: 152 → 155 group actions, 169 → 172 rows, height 175 → 178,
  dividers `[2, 5, 175]`, custom-actions height 181, Git section 23 → 25.
  The Cats test now expects 11 rows and 10 keys.
- `make test` (race) is green; vet and gofmt are clean.
- Real binary (`run-ced`, a scratch git repo with a dirty `conf.json`):
  the menu drew all 15 rows (inside cats).
  - Show uncommitted changes opened the panel on the diff.
  - Show git history showed `⌕ p:conf.json · 1 match`.
  - Validate flashed "conf.json: 1 error — line 2: trailing comma…".

## 4. Docs

- README: the tab bullet is now a sub-list of the rows and their ≡ twins.
- CLAUDE.md: the Tabs rule is rewritten (onTab split, verbs, split
  save/close rule), and the menu pins are updated.
- The `tabcontext.go` header has a new diagram and design notes.

## Next

Closed: None. Declined: None. Raised: N-038, N-039.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
