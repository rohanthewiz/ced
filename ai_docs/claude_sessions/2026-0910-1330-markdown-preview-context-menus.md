# Session: Preview / Stop Preview in both right-click menus

- Date: 2026-09-10
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `caadb0e9-ec12-4b35-bb86-47d822123f6d`
- Predecessor: `2026-0909-1845-tree-multi-select.md`

## What was asked

Three prompts, each one a small widening of the one before:

> In the filetree if I right click on a markdown file then ensure I have
> the option to "Preview" the markdown in the context menu

> It works. Now I just need an context menu item to go back to "Source View"

> In the context menu change "Show Source" to "Stop Preview" (I think
> that's clearer), then add the Preview and Stop Preview to the editor's
> main window context menu

The markdown viewer itself shipped the day before
(`2026-0909-1341-markdown-viewer.md`). Everything here is doors onto it.
No rendering code was touched.

## Where the viewer could be reached from, before and after

| surface | before | after |
|---|---|---|
| `Esc v` | toggle | toggle |
| ≡ **View** row | toggle | toggle |
| tree right-click | — | **Preview / Stop Preview** |
| editor right-click, source view | — | **Preview** |
| editor right-click, inside a preview | *the code vocabulary, aimed at a caret nobody can see* | **Stop Preview**, alone |

That last row is the one that was actually broken rather than merely
missing — see "The trap" below.

## One row of the pair, never both

The instinct is two rows and a predicate on each. That gives a popup
where one row is always a no-op, in a menu small enough that every row
is read. So each menu carries exactly one, and the label names what the
click will produce:

```go
if !n.IsDir && editor.IsMarkdownPath(n.Path) {
    if a.previewingPath(n.Path) {
        items = append(items, contextItem{label: a.ctxPreviewSourceLabel(n), action: ctxStopMarkdownPreview})
    } else {
        items = append(items, contextItem{label: a.ctxPreviewSourceLabel(n), action: ctxPreviewMarkdown})
    }
}
```

Label and action read the **same tab**, so the popup structurally cannot
offer "Preview" on a document it is already previewing.

### `previewingPath`, not `markdownTab`

`markdownTab()` asks about the **active** tab, which was the existing
predicate for every other surface — and the wrong question here. The
file under the pointer is usually *not* the file in front of you; a user
who previewed a README, went back to `main.go`, and now wants the README
back as source is precisely who the Stop Preview row is for. So:

```go
func (a *App) previewingPath(path string) bool {
	t := a.tabForPath(absolutePathFor(path))
	return t != nil && t.IsMarkdownView()
}
```

and the action focuses the tab on its way (`openFile` after
`SetMarkdownView(false)`), because reaching for a row about a file you
are not looking at implies you want to look at it.

### Preview FORCES on; it does not toggle

`ctxPreviewMarkdown` sets the view on unconditionally. With the label
already chosen by the file's own state, a toggling action could only
ever *disagree* with the label above it. `Esc v` and the ≡ row still
toggle — they act on the file in front of you, where "the other way" is
the only thing they could mean.

It also re-checks the path after opening:

```go
a.openFile(path)
t := a.activeTabPtr()
if t == nil || t.Path != path || !t.MarkdownCapable() {
	return  // openFile refused and flashed its own reason
}
```

Without that, a file `openFile` declined (too big, binary, unreadable)
would silently preview whatever tab happened to be in front of the user.

## The two menus have different conventions, and both were honoured

`contextmenu.go`'s header comment already spells this out and it turned
out to decide the whole placement question:

- The **tree** menu omits rows, because its list changes by node KIND —
  different nouns get different rows.
- The **editor** menu dims rows, because its noun is always "this spot
  in the text" and the user learns row positions.

So Preview is *appended conditionally* in the editor menu, beside the
cats rows, rather than joining the dimmed fixed vocabulary. The
vocabulary above it is things you do to a spot in text — they exist on
every file. Preview exists on six extensions, and would otherwise be a
permanently dead row in every source file anyone right-clicks.

## The trap: right-clicking inside a preview

`tryEditorContextClick` only declined image tabs and nil buffers. A
previewed tab has a real buffer, so a right-click inside a rendered
document opened the full menu — Rename symbol, Code actions, Cut —
every one of them aimed at a caret the reader cannot see, at a position
that has nothing to do with the row they clicked.

The fix is its own popup, built before the hit-test and the caret
placement:

```go
if tab.IsMarkdownView() {
	a.openPreviewContext(x, y)
	return true
}
```

`openPreviewContext` is one row: **Stop Preview**. It is deliberately
built as its own function rather than a branch inside
`editorContextItems`, so no future caller can ask a previewed tab for
LSP rows.

This is also what stops the preview being a **trap**. A preview swallows
every key but navigation, and the whole point of the tree's Preview row
is that it reaches a document without the user ever opening it as source
— so a reader can now arrive in a preview having never met `Esc v`. The
one-row popup is the way out, under the pointer where they are already
clicking.

## "Stop Preview" over "Show source"

The owner's call, and it holds up: the ≡ row can afford "Show markdown
source" because it sits in a labelled View group being read
deliberately. A context row is read in one glance beside Rename and
Delete, where the shortest spelling of the verb wins.

## Files

| file | what |
|---|---|
| `internal/app/markdown.go` | `previewingPath`, `ctxPreviewSourceLabel`, `ctxPreviewMarkdown`, `ctxStopMarkdownPreview` |
| `internal/app/modals.go` | the tree row, conditional on `!n.IsDir && editor.IsMarkdownPath` |
| `internal/app/contextmenu.go` | the Preview append + `openPreviewContext` + the in-preview branch |
| `internal/app/markdown_test.go` | 5 tests |
| `internal/app/contextmenu_test.go` | 3 tests |
| `CLAUDE.md` | the markdown section's surface list + two new house rules |

## Tests

Eight added. The ones that would catch a real regression:

- `TestOpenTreeContext_PreviewRowNamesItsOutcome` — asserts the pair
  while standing in a **different file**, which is the case
  `previewingPath` exists for; a version using `markdownTab` passes
  every other test and fails this one.
- `TestEditorContext_InsidePreviewOffersOnlyTheWayOut` — pins the popup
  at exactly `[Stop Preview]`, so a future row added to
  `editorContextItems` cannot leak into a surface with no caret.
- `TestOpenTreeContext_PreviewOnlyOnMarkdown` — absent on `.go`, absent
  on a directory.

`go test ./...` green throughout.

## Notes for next time

- `tabForPath` (plugincmd.go) is the general "is this file open" helper;
  it already existed and is the right thing to reach for whenever a
  surface acts on a file that is not the active one.
- The editor context menu's dim-vs-omit rule is written down in
  `contextmenu.go`'s header. Read it before adding a row there — it
  answers where the row goes without any further argument.
