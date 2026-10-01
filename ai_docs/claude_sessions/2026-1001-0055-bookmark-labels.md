# Session: bookmark-labels

Session ID: 0eda66cb-5309-4a2b-b080-db915d3262c4
Date: 2026-10-01

Follows `2026-0930-2056-bookmarks`.

## 1. The ask

From the cats-todo backlog, next-list item **N-044**:

> Bookmark extras left out of the first cut:
> - labels / names on a bookmark (the picker shows the line's text)
> - off-screen bookmarks in the overflow markers (`▴`/`▾` colour or popup)
> - parked bookmarks are re-keyed on rename only; a tree cut/paste MOVE
>   of a closed file was not checked and would leave them "(missing)"
> - a hand check in a real terminal that `⚑` is one cell wide there

## 2. What was done

### Labels

- `editor.Bookmark` gains `Label`. It is PAYLOAD, never a key — the line
  text still re-anchors. Every path that rebuilds a bookmark carries it:
  `remapBookmarks`, `SetBookmarks`, and `normalizeBookmarks`' merge (when
  two bookmarks collapse onto one line, an unnamed survivor takes the
  other's name).
- New Tab methods: `BookmarkAt(line)`, `SetBookmarkLabel(line, label)` —
  the latter adds the bookmark if the line has none.
- `history.Bookmark` gains `Label` (`json:"label,omitempty"`), so unnamed
  sets encode byte-for-byte as before (no rewrite of every row after an
  upgrade). `history.MaxBookmarkLabel` = 60 runes, clipped on a rune
  boundary in `encodeMarks` (`clipLabel`). MaxBookmarks comment updated:
  worst case ~220KB, still under `compactAbove` (256KB).
- App (`app/bookmarks.go`): `bookmarkRef.label`; `menuLabelBookmark`
  (≡ Nav "Label bookmark…", under Toggle bookmark) prompts with the
  current name, captures tab + line at open; `labelBookmarkAt` trims,
  flattens tabs/newlines, clips, re-checks the tab is still open, writes
  through. Cap enforced only when the label would ADD a bookmark.
- Clearing a name: `promptModal.submit` silently drops an empty value, so
  "empty clears" was impossible — the prompt instead carries a
  `[Clear name]` extra with an `alt+c` chord, only when there is a name.
- Picker row: `path:line  [label]  text` (`bookmarkPickerLabel`) — the
  filter matches names. Next/Previous flash appends `· label`.
- Editor right-click: "Label bookmark…" row only on a bookmarked line.

### Off-screen bookmarks

- `offscreen.bookmarks` counted in `editorOffscreen` (via
  `t.Bookmarks()`, skipping on-screen lines). POPUP ONLY: listed last in
  `detail()` ("1 error · 3 bookmarks"), no `offscreenKind`, never colours
  the marker — Accent already means "the caret is out there", and any
  rank would either hide an error or appear inconsistently.

### Tree cut/paste move — premise lapsed

ced has no move verb. The file clipboard only copies (`startPaste` →
`copyTree`); a copy rightly gets no bookmarks. The only in-editor move is
rename, which `bookmarksRenamed` already handles. Moves outside ced
(shell `mv`, `git mv`) still leave "(missing)" rows, by design.

### `⚑` width

U+2691 BLACK FLAG: East Asian Width Neutral, not Emoji_Presentation;
macOS libc `wcwidth` = 1; uniseg = 1 (2 only with VS16, never emitted).
The real binary in a PTY (run-ced capture) lines `⚑   7` up with
`    8`; the picker showed `demo.txt:7  [entry point]  line 7 of the
file` and the `▴` popup read "6 lines above / 1 bookmark". Not checked:
a GUI terminal's font fallback drawing the glyph wider than its cell —
layout follows wcwidth, so that would be overdraw, not a column shift.

### Pins and docs

- Menu pins: 162 group actions / 179 rows / height 185 / dividers
  `[2, 5, 182]` (app_test.go + CLAUDE.md).
- CLAUDE.md Bookmarks + Overflow sections note labels and popup-only
  counts. README Bookmarks section: "Name one", list shows names, the
  overflow popup count.

## 3. Tests added

- editor: `TestSetBookmarkLabel_NamesAddsAndClears`,
  `TestBookmarkLabel_RidesEditsAndRestore`, `TestBookmarks_MergeKeepsTheName`.
- history: `TestBookmarks_LabelRoundTripsAndOldRowsReadBack`,
  `TestEncodeMarks_ClipsLongLabels`.
- app: `TestMenuLabelBookmark_NamesAndShowsTheName`,
  `TestMenuLabelBookmark_AddsClearsAndRefuses`,
  `TestBookmarkLabel_ParkedPersistedAndRestored`,
  `TestEditorContext_LabelRowOnlyOnABookmarkedLine`,
  `TestEditorOffscreen_CountsBookmarksWithoutColoring`.

`make test` (race) green.

## Next

Closed: N-044. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
