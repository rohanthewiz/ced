# ⌘↑ / ⌘↓ file motion, ⌘L Go to line, ⌘G git panel (+ cats forwarding)

Session: `0c933100-d751-4dfe-80e8-50c1b1365e2d`

## Ask

From the cats-todo backlog: "Use CMD+[up-arrow] to take us to the top of
a file". Then: it didn't work in cats, so change cats as needed. Then:
check that ⌘L comes through cats. The user chose ⌘L = Go to line, moved
⌘G to "Show git panel", and had cats forward ⌘L everywhere (Chrome
included).

## What shipped — ced (uncommitted until this wrap)

### ⌘↑ / ⌘↓ (app.go editing switch, markdown.go)

- `KeyUp` / `KeyDown` + ModMeta, behind the same `metaLineMotion` gate as
  ⌘←/⌘→ (kitty-protocol emulator or cats Tier 1). ⌘↑ →
  `MoveCursorTo(Position{})`, ⌘↓ → `MoveCursorTo(Buffer.EndPos())`.
  Line AND column, the Mac text-view meaning.
- `MoveCursorTo`, not a motion: it is a JUMP, so secondary carets drop;
  ⌘⇧ keeps the anchor and selects.
- The meta check runs BEFORE the Alt branch; the gate keeps an
  Option-as-Meta host's Alt+Up on `MoveLines`.
- Markdown preview: ⌘↑ / ⌘↓ set `MDScroll` to 0 / `MDMaxScroll`, the
  same meaning as in the source view (Home/End's twin there).
- Guaranteed twins (no ≡ row was added — that would move the menu pins):
  `Esc j` (go to line clamps: 1 = top, a big number = last line) and
  PgUp/PgDn. Recorded in `metaLineMotion`'s doc comment.
- Verified tcell's `calcModifier` reads `CSI 1;9A` as KeyUp + ModMeta.

### ⌘L / ⌘G (metakeys.go table)

- `{key: 'l', menuGoToLine}` (GoLand's keycap) and
  `{key: 'g', menuToggleGitPanel}` (Esc-g's verb, the ≡ Git "Show git
  panel" row — a toggle, so a second ⌘G hides it).
- File comment: ⌘L removed from the "stays the browser's" host list,
  with the date cats started forwarding it.

### Docs + tests

- README ⌘ paragraph (⌘↑/⌘↓, ⌘L, ⌘G) and the Go to line sentence (⌘L);
  CLAUDE.md's ⌘ bullet names both arrow pairs as editing-switch chords.
- metakeys_test.go: `TestMetaFileMotion*` (arrows, extend, drops carets,
  silent on unknown host, doesn't shadow Alt+Up), `TestMetaAccelGoesToLine`,
  `TestMetaAccelTogglesGitPanel`. markdown_test.go:
  `TestMarkdownView_MetaArrowsReachTheEnds`. `make test` green.

## What shipped — cats `9409f6c`

- Why ⌘↑ was dead: `CMD_TO_PANE` (cmd/catway/web/js/20-keys.js) had no
  ArrowUp, so the browser kept the chord. ⌘L was deliberately excluded
  (address bar).
- `ArrowUp`, `ArrowDown`, `KeyL` added. Arrows go down VERBATIM as
  super+arrow (unlike ⌘←/⌘→ → Home/End): no bare key means "top of
  document" to every terminal app, so a legacy pane keeps the browser's
  chord via the kitty gate. ⌘L is the one entry that costs the browser
  something; reasoning in the comment. The set stays ONE line —
  `sliceConst` in jstest/testutil.mjs only lifts single-line consts.
- Help row (32-help.js) lists ⌘L ⌘↑ ⌘↓.
- Encoder pins: super ArrowUp `\x1b[1;9A`, ArrowDown `\x1b[1;9B`,
  shift+super ArrowUp `\x1b[1;10A`, super l `\x1b[108;9u`.
- jstest/cmdarrows.test.mjs lifts the REAL `CMD_TO_PANE` +
  `cmdGoesToPane` + `mods`: kitty pane → sent with mods 8 (9 with
  Shift); legacy pane → not prevented, nothing sent; ⌘← still Home:0.
  Confirmed it fails with the arrows removed.
- `make test`, `make test-ghostty`, `make jstest` green; `make binaries`
  built. NOT installed: the running Cats is /Applications/Cats.app and
  `make macapp` replaces that bundle — left for the user.

## Next

Closed: None. Declined: None. Raised: N-088, N-089. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
