# Git: New branch… and a push receipt that keeps the PR link

Session: `d3563dc1-8574-48eb-8879-44fc18a0107b`

## Asks

1. Add a "New branch" feature to the ≡ Git menu.
2. "When I push give me all of the git message as it may include a link
   to automatically create a PR."
3. Wrap the session and cut a patch release (0.6.5).

## 1. ≡ Git "New branch…" (commit `390c134`)

- `gitcmd.go`: `menuGitNewBranch` opens a prompt titled "New branch"
  with the hint `from <branch>` (`from HEAD` when no label is known),
  plus `· uncommitted changes come along` on a dirty tree. Submit runs
  `git switch --create=<name>` via `runGitCmd`.
- Create AND switch: a branch made but not checked out is the classic
  way to commit to the wrong one. Changes ride along, which is also the
  "I started this on main" rescue.
- Validation is git's own (its refusal lands in the "Git failed" modal);
  the prompt already drops empty submits. The name is glued to the flag
  (`newBranchArgs`) so a leading `-` stays a value. Verified by hand that
  `git switch --create` works on an unborn branch, and that git refuses
  `-x` and `a b` with clear messages.
- Row sits after "Recent branches", gated on `hasGitRepo`.
- Tests: create+switch round-trip through the done-event, invalid name
  → git's refusal with HEAD unchanged, argv guard, hint shapes.

## 2. Push receipt shows everything, link intact

The push receipt (gitpushreceipt.go, sharing the commit receipt's panel)
already existed, but lost the PR link three ways: body capped at 12
lines, lines over 68 cells hard-split by `wrapChatText` (a
`pull/new/feature/...` URL broke across rows), and gone in 7s or on any
key.

- **Content refactor** (gitcommitreceipt.go): new `receiptContent`
  (title, hash, head, lines, pushBranch, link, width) embedded in
  `commitReceiptState`; `openReceiptContent` assigns it whole and
  `closeCommitReceipt` zeroes it whole, so a new field can't leak from
  one receipt into the next. `openGitReceipt` is now a thin wrapper.
  `commitReceiptHasButtons` is the one predicate for height, window and
  draw.
- **All the output**: `pushReceiptLines` (cleaning, split out of the old
  body function) feeds body, width and link. `pushReceiptMaxLines` =
  window height − 8 (floor 3) replaces the 12-line cap for pushes.
  `pushReceiptWidth` widens the panel to the longest line + 4 (min 72,
  max window width) so the URL stays on one row.
- **Link**: `pushReceiptLink` takes the first http(s) URL on a `remote:`
  line, trailing prose punctuation trimmed; the `To <url>` line (the
  remote's clone URL) never counts.
- **Buttons**: `[ Open link ]  [ Copy link ]`, right-aligned on the
  button row, geometry in one place (`commitReceiptLinkBtns`, read by
  draw and `handleMouse`). Click-only, like the commit receipt's Push
  button. Open → `browserArgv` (`open` / `xdg-open`, no-desktop reason
  pointing at Copy link) through `hostRunAsync`; macOS over SSH flashes
  where the window went. Copy → `copyPathToSystemClipboard(link, "link")`
  (OSC 52). The link is read before the close that empties it.
- **Window**: a receipt with any button (push or link) gets
  `commitReceiptPushFor` (20s); the existing hover hold applies.
- **Recovery**: `App.lastPush` remembers the output even when the panel
  declines an occupied screen; new ≡ Git row "Show last push output"
  (after Push…, gated `hasLastPush`) reopens the receipt.
- Tests (gitpushreceipt_test.go): link extraction (GitHub, GitLab,
  punctuation, clone-URL-only, none), width, max lines, a 16+7-line
  output shown uncapped with the URL drawn whole, both buttons through
  `handleMouse` (Open via `recordHostRun` + `withHost`), `browserArgv`
  per platform, the ≡ row reopening a declined receipt. Clipboard in
  tests may fail on `/dev/tty`; the Copy assertion accepts the failure
  flash, as fileops_test.go does.

## Menu pins

Two rows added in total → `TestMenuLayout_NoCustomActions` now 197 rows
(2 + 180 actions + 15 headers), height 203, dividers `[2, 5, 200]`; Git
section 34 rows; tall-window heights 203; custom-actions height 206.
CLAUDE.md's pin line and its Git push-receipt paragraph updated.

## Verification

`make test` (race) green across all packages. Not run in the real
binary — see N-078.

## 3. Release

ced 0.6.5: `version.go` + `cats-plugin.toml` bumped, tagged `v0.6.5`.

## Next

Closed: None. Declined: None. Raised: N-076, N-077, N-078.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
