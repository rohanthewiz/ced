# Session: README catches up on Recent files / Recent locations (N-029)

Session ID: 78608c4e-3828-42d0-91f2-2d365d914c8f
Date: 2026-09-29

(Same Claude session as `2026-0929-1923-cmd-a-to-pane-in-browser-cats`.)

One next-list item from the cats-todo backlog: N-029, a README-only
change.

## N-029: the history features had no README paragraph

> README does not mention ≡ Nav → Recent locations…, the drill-in, or
> that recent files / locations now live in `<repo>/.ced/history.bytdb`
> (gitignored), nor the "No file open" placeholder's Recent files ·
> Recent locations links. An instance of N-003.

### What changed (README.md only)

- **New `### Recent files and recent locations`**, placed after "Find
  file in project" (both answer "get me to a file / place"):
  - Recent files… (`Esc B`, `⌘E`): lists closed files too, the current
    file is left out so row one is the two-file flip, and deleted or
    moved files are pruned.
  - Recent locations…: 5 recent, a `┄┄┄` spacer, then 10 frequent that
    weren't already listed. A side-by-side diagram shows the drill-in
    and its "Reveal … in tree" first row. Picking a folder reveals it and
    never re-roots (Recent folders… is still the project switcher). A
    "use" is a file opened in a NEW tab, or a folder picked there.
  - The "No file open" placeholder with its two links.
  - Where it's stored: `.ced/history.bytdb` (search history too),
    `.ced/.gitignore` covers only `history.bytdb*` (checked in
    `internal/history/db.go`'s `ignoreLine`, so a committed
    `.ced/format.json` is unaffected), per repository, two windows add
    rather than overwrite, repositories only (`ced ~` leaves nothing),
    and the "(not saved)" label when it can't be written.
- **A Features bullet** linking to the section.
- **An `Esc B` row** in the leader-key table.

Every claim was checked against `recentfiles.go`, `recentlocations.go`,
`emptyeditor.go`, `internal/history/db.go` and the ≡ Nav rows in
`app.go`.

### What was noticed and left alone

The leader-key table is short on more than `Esc B`. A diff of leader.go's
keys against the README found about fifteen top-level leaders with no
row. That's the N-003 standing problem again, so it's raised as N-036
rather than fixed under N-029.

## Next

Closed: N-029. Declined: None. Raised: N-036.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
