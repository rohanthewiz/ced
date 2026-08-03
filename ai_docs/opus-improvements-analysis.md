# ced — improvements analysis and work plan

Date: 2026-08-03
Scope: a survey of remaining low-hanging fruit in ced across features,
robustness, and performance, plus the staged plan for working through it.

Everything below was measured or read out of the tree at commit `d18c194`;
line numbers are from that commit and will drift.

---

## Executive summary

The editor's *feature* surface is unusually complete for its age — LSP, an
ACP chat panel, MCP, skills, plugins, themes, git panels, an embedded
shell. What has not kept pace is the layer underneath: the text buffer's
performance under ordinary typing, the guards around what gets loaded into
it, and the durability of what gets written back out.

The three highest-value items are therefore not features:

1. Highlighting re-lexes the whole file on every keystroke (70 ms / 36 MB
   in ced's own `app.go`).
2. Nothing guards what `NewTab` will read — no size cap, no binary sniff.
3. Saves are non-atomic and CRLF files round-trip incorrectly.

After those, the largest *usability* gap is that tabs have no keyboard
path and no overflow handling, and the largest missing *verb* set is
replace / go-to-line / project search.

---

## 1. Performance: full re-lex on every keystroke

`Tab.Render` (`internal/editor/tab.go:683-685`):

```go
if t.StyleStale {
    t.Styles = Highlight(t.Path, t.Buffer.String(), th)
    t.StyleStale = false
}
```

`StyleStale` is set by every buffer mutator (`tab.go:285, 339, 368, 396,
431, 467`), so a single typed rune costs a full `strings.Join` of the
buffer, a full Chroma tokenise, and a freshly allocated per-rune style
grid (`highlight.go` builds `[][]tcell.Style` sized to the source).

Measured (Darwin 25.2, `Highlight` called directly, 5+ iterations):

| file | lines | KB | ms per re-lex | alloc per re-lex |
|---|---|---|---|---|
| `internal/app/app.go` | 3,831 | 134 | **69.95** | 36 MB |
| `internal/app/copilot_chat.go` | 1,898 | 65 | 36.61 | 18 MB |
| `internal/editor/tab.go` | 968 | 32 | 18.33 | 9 MB |
| synthetic 25k-line `.go` | 25,000 | 1,586 | **1757.23** | 854 MB |

Typing in ced's own largest source file runs at roughly 14 fps and
produces 36 MB of garbage per character, before any SSH latency.

**Fix (staged):**

- Debounce the re-lex the way `lspAfterEvent` debounces didChange: paint
  from the previous grid while a burst is in flight, and re-lex once the
  typing stops.
- Patch the edited line's styles synchronously so the line under the
  caret never shows visibly stale colors.
- Avoid the `Buffer.String()` round trip; cache the joined source or lex
  from the line slice.
- Threshold above which highlighting is simply off, announced in the
  status bar. Every mature editor does this.

Chroma has no incremental API, which is why the answer is debounce +
threshold rather than incremental lexing.

## 2. Robustness: nothing guards what gets opened

`NewTab` (`internal/editor/tab.go:~147`) calls `os.ReadFile` with no size
cap and no binary sniff. A misclick in the file tree, or a fuzzy-finder
hit on a vendored artifact, loads the whole thing, splits it into lines,
and hands it to Chroma. A 300 MB log or a `.so` is enough to wedge the
editor.

**Fix:** stat before read. Over a threshold → read-only large-file mode
(no highlighting, no LSP, no diff). NUL byte in the first 8 KB → refuse
with a flash, or open a preview. ~40 lines.

## 3. Robustness: no CRLF handling

`NewBuffer` splits on `"\n"` only (`internal/editor/buffer.go:36-41`), so
every line of a CRLF file retains a trailing `\r`. Consequences: a stray
cell at end of line, End landing one column past the text, find matching
oddly at line ends, and a save that writes mixed endings — old lines with
CRLF, newly typed lines with bare LF.

**Fix:** detect the dominant ending on load, strip on read, record it on
the Tab, restore on write.

## 4. Robustness: saves are not atomic

`Tab.Save` (`internal/editor/tab.go:230`) is a bare `os.WriteFile`, which
truncates in place. A full disk, a killed process, or a stalled network
mount mid-write leaves a truncated file — and the undo history that could
have recovered it died with the process.

**Fix:** write a temp file in the same directory, fsync, `os.Rename` over
the target, preserving the existing mode from a pre-stat. ~15 lines.

## 5. Tabs: no keyboard path, no overflow

`layoutTabs` (`internal/app/app.go:3370`) lays tabs out left to right
without bound; `drawTabBar` clips whatever runs past the band edge. Open
a dozen files and the active tab can be off-screen and unclickable.

There is also **no tab-switching key binding at all** — no next/prev, no
`Esc 1..9`. For an editor whose own documentation notes that macOS
Terminal can swallow clicks, that is the largest keyboard gap present.

**Fix:** scroll the strip so the active tab is always visible, add an
overflow affordance, add prev/next leaders, and add a "Switch tab…"
picker through `openPicker` (which scales past nine tabs and follows the
existing modal house rule).

## 6. Missing verbs

- **Replace** — `internal/app/find.go` and `internal/editor/find.go` have
  no replace path at all.
- **Find options** — `editor/find.go:38` lowercases unconditionally: no
  case toggle, no whole-word, no regex. `wordhl.go` already contains a
  case-sensitive whole-word scanner that find could borrow.
- **Go to line** — absent. It is how a user acts on a compiler error read
  in the terminal panel.
- **Find in project** — the finder is filename-fuzzy only. The natural
  build is `rg`/`grep` shelled into the **find-all list that already
  exists** (`internal/app/findall.go` is already a two-column,
  live-preview, dockable, Esc-restoring list). Extending its rows to
  carry a path yields project search for a fraction of the usual cost.
- **Save as** — `internal/app/app.go:2695` reads *"Saving untitled tabs
  is not supported yet"*.
- **Session restore** — nothing persists open tabs. A tmux-resident
  editor is killed and restarted constantly.

## 7. LSP: the client is built; two verbs are wired

Only definition and hover are exposed. With `internal/lsp` already in
place, the following are mostly request plumbing plus a picker that
already exists:

- **Document symbols → `openPicker`** ("Go to symbol in file"). Best
  value per line on this list.
- References, rename, code actions (a quick fix on the diagnostic already
  under the gutter dot), signature help.
- **LSP completion** — a user without Copilot currently has no completion
  at all, and `internal/editor/ghost.go`'s overlay is plausibly reusable.

## 8. Multiplexer-native ideas

- **Clickable terminal output.** `internal/plugins/diag.go` already parses
  `path:line:col: severity: message`. Running it over the terminal
  panel's scrollback turns `go build` output into double-clickable jumps
  and closes the build→fix loop inside one pane.
- **`ced --wait` / `ced --remote`.** `--wait <file>` makes ced usable as
  `$EDITOR` for `git commit` and `kubectl edit`. `--remote <file>` opens
  into an already-running instance over a unix socket rather than nesting
  a second editor inside the first. The most tmux-native item here.
- **Undo memory cap.** `internal/editor/undo.go` keeps 500 full
  `[]string` snapshots per tab. On a 25k-line file that is ~200 MB of
  slice headers per tab before the strings themselves. Cap by total
  bytes, not entry count.
- **Verify, don't assume:** suspend/resume (`Ctrl-Z` → `fg`) screen
  restore, and how the 35-key derived palette degrades under
  `TERM=screen` or a 16-color terminal. No signal handling exists in
  `internal/app`; tcell may cover both.

## 9. Requested features

### File↔file and file↔clipboard comparison

Two unified-diff rendering surfaces already exist and are both fed by
`[]string` (`gitlog.go`'s detail pane, `gitpanel.go`'s diff pane), and
`git diff --no-index --no-color -- a b` supplies file↔file even outside a
repository.

The constraint worth naming: ced deliberately cannot *read* the system
clipboard (`internal/clipboard/clipboard.go` is OSC 52 write-only, and
that is correct for an SSH-first editor). So "compare with clipboard"
means the *internal* clipboard. The more broadly useful framing is
**"Compare with pasted text"** — a scratch buffer filled by bracketed
paste — which covers text from any source and makes file↔file the same
code path with a different left side.

Home: a third occupant of the bottom strip under the existing
single-occupancy rule, mirroring `gitlog.go`'s two-pane shape. Side-by-side
rendering is the expensive version; unified in the existing pane is the
low-hanging one.

### Open folder

`rootDir` itself is touched in about six places (`app.go:614, 874, 1027,
1038, 2354, 3600`). What is not cheap is everything derived from it: the
finder index, the tree, git status, gopls's `rootUri` (fixed at
initialize — it needs a server restart), the ACP session cwd, MCP's
`roots/list`, and plugin working directories.

So implement it as teardown → `New(newRoot)`, not as a field reassignment.
Two things ride along in the same change: a **recent folders** list
persisted in config.json, and **bare `ced` opening the last folder**.

---

## Work plan

Stages are ordered by value per unit of risk. Each stage ends in its own
commit, with tests, per the project's testing convention.

| # | Stage | Items | Status |
|---|---|---|---|
| 0 | Analysis + plan | this document | done |
| 1 | Highlight debounce | §1 | **done** — 70.17 ms → 0.0015 ms per keystroke |
| 2 | Load/save durability | §2, §3, §4 | **done** — see the note below |
| 3 | Tab switching + overflow | §5 | **done** — see the leader-key note |
| 4 | Project search | §6 (find in project) | **done** — see the preview note |
| 5 | Find verbs | replace, case/whole-word, go to line | **done** |
| 6 | Compare | file↔file, compare with pasted text | **done** — pure-Go differ, see the note below |
| 7 | Open folder | recent folders, bare-`ced` restore | pending |
| 8 | Session restore | open tabs + cursors per root | pending |
| 9 | LSP verbs | document symbols first, then references/rename/actions | pending |
| 10 | Terminal diagnostics | scrollback → `diag.go` → clickable jumps | pending |
| 11 | `--wait` / `--remote` | `$EDITOR` integration, single-instance open | pending |
| 12 | Undo memory cap | byte-budget the snapshot stack | pending |

Stages 1–4 were the explicitly requested starting order.

### Note from stage 6: the differ is ced's own, and it is patience

`git diff --no-index` was the plan's suggestion and it lost on two counts.
The sources a comparison actually has are BUFFERS — unsaved text, a
pasted block — so handing them to git means temp files, and git itself
isn't guaranteed present (it refuses the job outside a repo anyway, which
would have made the feature repo-only for no reason). `internal/diff` is
~350 lines with no dependency.

Patience rather than Myers or a plain LCS, for readability first: anchors
are lines appearing exactly ONCE on each side, which in code means
signatures and distinct braces, and that is what stops an added function
rendering as "every closing brace moved". A full LCS table is used only
inside small unanchorable regions; the output is byte-compatible with
git's, so `gitPanelDiffStyle` and `diffTargetLine` were both reused as-is.

### Note from stage 5: the find bar owns the keyboard, which buys Alt

`alt+c` / `alt+w` / `alt+a` are safe chords INSIDE the find bar and would
not be anywhere else: handleFindKey runs before handleKey's Alt+rune
leader branch, so tmux's folded "Esc c" lands in the bar rather than
firing a leader. That's what made in-bar option toggles possible at all,
since the ≡ menu is unreachable while a keyboard-owning surface is up.
The toggles still get menu rows (a terminal that eats clicks) and
clickable `Aa` / `|W|` buttons (this project is mouse-first).

### Note from stage 4: project mode does not live-preview

The in-file list previews as you walk it — the highlight moves the
editor's cursor, and Esc puts it back. Project mode deliberately does not,
and this was the one real design decision in the stage.

Previewing across files means opening a file per keystroke. An open is not
free: it fires the LSP's `didOpen`, Copilot's `didOpen`, every plugin's
`open` hook, and a syntax pass — and leaves a tab behind for every row the
user merely scrolled past. So the row is the preview (it already carries
the whole line with the hit lit), and Enter or a double-click opens. Esc
then restores nothing, because nothing moved.

If a preview mode is ever wanted, the honest way in is a real
preview-tab concept (one reusable slot, replaced rather than accumulated),
not a special case inside this modal.

### Note from stage 3: `Esc [` and `Esc ]` are unbindable

The obvious keys for prev/next tab are `[` and `]` — what every editor
with buffers uses. They cannot work here, and the failure is silent:
`\x1b[` is the CSI introducer and `\x1b]` is OSC, so the terminal's own
parser (and tcell's) consumes the pair before the leader table ever sees
it. The binding built, tested green against a synthetic key event, and
did nothing at all in a real terminal.

The shipped bindings are `Esc ,` / `Esc .` — `<` and `>` live on those
keys, so the left/right mnemonic survives, and neither introduces
anything. Worth remembering before adding any future punctuation leader:
`O`, `P`, `N`, `\`, `^`, `_` and `#` are all escape-sequence introducers
too. (`Esc O` is already bound to Go forward and appears to survive,
which is luck rather than design.)

### Note from stage 2: format-on-save still normalises endings

`internal/editor/fileio.go` makes a tab round-trip its own line ending
and BOM, and that holds for any file ced writes itself. It does NOT hold
for a file rewritten by an external formatter: gofmt and goimports always
emit LF, so a CRLF `.go` file saved with format-on-save active comes back
LF and the following reload records it as such.

That is arguably gofmt's call rather than ced's, and it only affects
languages whose formatter has an opinion — but it is worth knowing before
someone files it as a bug against this stage.
