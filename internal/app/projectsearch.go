// =============================================================================
// File: internal/app/projectsearch.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// projectsearch.go is "Find in project": every occurrence of a query
// across the whole tree, listed in the SAME panel Find-all uses.
//
// Reusing that panel rather than building a list surface is the entire
// reason this feature is small. findall.go already had everything a
// cross-file result list needs — a two-column row, a fixed-height strip
// that displaces the editor instead of covering it, a right-dock mode,
// scrolling, an Esc contract, and a mouse story. All this adds is a path
// on each row and a different verb behind Enter.
//
// Where project mode DELIBERATELY differs from the in-file list:
//
//   - Walking rows does not preview. In-file, moving the highlight moves
//     the cursor live, because the buffer is already open and moving the
//     cursor costs nothing. Across files it would mean opening a file per
//     keystroke — and an open is not free: it fires the LSP's didOpen,
//     Copilot's didOpen, every plugin's "open" hook, and a syntax pass,
//     and leaves a tab behind for every row the user merely scrolled
//     past. So the row IS the preview (it carries the whole line with the
//     hit lit), and Enter or a double-click opens. That is what every
//     grep integration does, and it is the honest reading of the cost.
//   - Esc therefore restores nothing, because nothing moved.
//
// The search itself is internal/search: pure Go, over the FINDER'S index
// so the project's own ignore rules apply, run off the main loop and
// delivered as an event like every other background result in this
// codebase.
//
// "Find all in folder" is the same search with a SCOPE: the index is
// narrowed to the paths under one directory before the walk starts, and
// everything after that — the event, the list, ⟳, the tints — is this
// file's pipeline unchanged. The scope rides the event and the modal
// (scopeDir) so the title can name it and ⟳ re-asks the same question:
//
//	tree folder ▸ "Find all in folder…" ─┐
//	≡ Find "Find all in folder (sub/)" ──┼─▶ promptFindInFolder(dir)
//	                                     │        │
//	≡ Find "Find in project" / Esc-P ────┼────────┼─▶ startFolderSearch(q, opts, dir)
//	⟳ on a finished list ────────────────┘        │      dir "" = whole project
//	                                              ▼
//	                       scopeSearchPaths(index, dir) ─▶ search.Project ─▶ event
package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/history"
	"github.com/rohanthewiz/ced/internal/search"
)

// projectSearchEvent carries a finished search back to the main loop.
// Generation-checked like every other async result: a search launched
// before the user changed their mind must not open a list over the top
// of whatever they are doing now.
type projectSearchEvent struct {
	when      time.Time
	seq       int
	dir       string // absolute folder the search was scoped to; "" = project
	query     string
	opts      editor.FindOptions
	hits      []search.Hit
	truncated bool
}

// When satisfies the tcell.Event interface.
func (e *projectSearchEvent) When() time.Time { return e.when }

// menuFindInProject is the ≡ Search row and the Esc-P leader: ask what
// to look for across the whole project, with the box already holding
// the best answer ced has.
//
// Unlike the in-file list, a selection does NOT search silently here.
// Find-all's rule is that a single-line selection is the user pointing
// at the exact text, so asking could only be answered "yes, that" — and
// in a single open buffer the search costs nothing, so a wrong guess is
// a free retry. A project search is a whole-tree walk: the user reaches
// for it with a selection that is usually the RIGHT NEIGHBOURHOOD of
// the query rather than the query itself (a call site to trim to the
// identifier, a message to cut down to its stable part), and the only
// moment to adjust it is before the walk starts. So the selection SEEDS
// the prompt — Enter runs it unchanged, one keystroke, and typing
// replaces it — the way the find bar opens holding it (find.go).
func (a *App) menuFindInProject() {
	a.closeMenu()
	// Captured before openPrompt for the reason openFindAll documents:
	// opening a modal clears the find bar that may be seeding this.
	seed := a.projectSearchSeed()
	a.openSearchPrompt("Find in project", "searches every file", seed, history.SearchFind, func(app *App, v string) {
		// The toggles are read when the search RUNS, not when the prompt
		// opened — the same moment the query itself is final.
		app.startProjectSearch(v, app.findOptions())
	})
}

// projectSearchSeed is what pre-fills the Find in project prompt, in
// priority order: a single-line selection (the user named the text),
// then everything findAllPromptSeed already ranks — the find bar's
// query, then the word under the cursor. The selection goes through
// findAllSelectionQuery rather than a second reading of the tab, so
// "what counts as a selectable search term" (single line, non-empty)
// has one spelling across every find verb.
func (a *App) projectSearchSeed() string {
	if q := a.findAllSelectionQuery(); q != "" {
		return q
	}
	return a.findAllPromptSeed()
}

// hasProjectSearch gates the menu row: the finder's index is the file
// list, so there is nothing to search until it exists.
func (a *App) hasProjectSearch() bool {
	return a.finder != nil && a.rootDir != ""
}

// startProjectSearch kicks the scan off on a goroutine and posts the
// result. Off-loop because a cold cache over a large tree is hundreds of
// milliseconds of IO, and the editor must stay responsive through it —
// the same contract git status, diffs, and plugin commands all keep.
//
// opts is a parameter rather than read from the App here because ⟳ on a
// finished list must re-ask ITS question (the snapshot on the modal),
// while fresh searches pass a.findOptions().
func (a *App) startProjectSearch(query string, opts editor.FindOptions) {
	a.startFolderSearch(query, opts, "")
}

// startFolderSearch is startProjectSearch narrowed to the files under
// dir (absolute). dir "" — or the project root itself, which is what a
// right-click on the tree's root row hands over — is the whole project,
// so the two verbs are one pipeline and the root case reads exactly like
// Find in project rather than as a folder that happens to be everything.
func (a *App) startFolderSearch(query string, opts editor.FindOptions, dir string) {
	if query == "" || a.screen == nil {
		return
	}
	dir = a.searchScopeDir(dir)
	verb := a.projectSearchVerb(dir)
	if a.finder == nil {
		a.flash(verb + ": no file index yet")
		return
	}
	paths := a.finder.Paths()
	if len(paths) == 0 {
		// The index builds asynchronously at startup and after workspace
		// mutations. Saying so beats "no results", which would be a lie.
		a.flash(verb + ": still indexing — try again in a moment")
		return
	}
	if dir != "" {
		paths = a.scopeSearchPaths(paths, dir)
		if len(paths) == 0 {
			// Distinct from "no occurrences": nothing was searched at
			// all. The usual cause is a folder the project ignores
			// (build output, vendor) — the index skips it, so the search
			// does too, and the user deserves to know which it was.
			a.flash(verb + ": no indexed files there (ignored by the project?)")
			return
		}
	}

	// Remembered once the walk is really going to run — not for a query
	// refused because the index is still building, which the user will
	// simply retry (searchhistory.go).
	a.recordSearch(history.SearchFind, query)
	a.projectSearchSeq++
	seq := a.projectSearchSeq
	// Marked busy here and cleared on arrival, so "the editor is working"
	// covers the whole scan rather than the instant it started.
	a.projectSearchActive++
	scr, root := a.screen, a.rootDir
	a.flash(fmt.Sprintf("Searching %d files for %q%s…", len(paths), query, findOptionsNote(opts)))
	go func() {
		hits, truncated := search.Project(root, paths, search.Options{Query: query, FindOptions: opts})
		_ = scr.PostEvent(&projectSearchEvent{
			when: time.Now(), seq: seq, dir: dir, query: query, opts: opts,
			hits: hits, truncated: truncated,
		})
	}()
}

// handleProjectSearch opens the result list on the main loop.
//
// A stale generation is dropped, and so is a result that arrives while a
// modal or the menu owns the screen — the user asked for something else
// in the meantime, and stealing the slot from a prompt they are typing
// into would be worse than losing a search they can repeat.
func (a *App) handleProjectSearch(e *projectSearchEvent) {
	// Before the stale-generation drop below: a superseded scan finished
	// too, and a count that only decremented on the paths that USE the
	// result would climb forever as the user retyped their query.
	if a.projectSearchActive > 0 {
		a.projectSearchActive--
	}
	if e.seq != a.projectSearchSeq {
		return
	}
	verb := a.projectSearchVerb(e.dir)
	if len(e.hits) == 0 {
		a.flash(fmt.Sprintf("%s: no occurrences of %q%s", verb, e.query, findOptionsNote(e.opts)))
		return
	}
	if a.modal != nil || a.menuOpen {
		a.flash(fmt.Sprintf("%s: %d hits for %q — run it again", verb, len(e.hits), e.query))
		return
	}
	m := &findAllModal{
		query:     e.query,
		tabIdx:    -1, // no single tab behind this list
		project:   true,
		scopeDir:  e.dir,
		truncated: e.truncated,
		opts:      e.opts,
		rows:      a.projectSearchRows(e.hits),
		// Same pre-fill as the in-file list: the box arrives holding the
		// expression, inert until edited (findall.go's seed rule). It
		// matters more here — narrowing a whole-tree answer in the box
		// costs nothing, re-running the walk costs a walk.
		filter: newTextField(e.query),
		seed:   e.query,
	}
	if e.dir != "" {
		m.scopeName = a.relativeFolderLabel(e.dir)
	}
	m.rebuildView()
	// Same replacement rule as showFindAll: a fresh list supersedes a
	// pinned survivor from an earlier search.
	a.dropFindAllPin()
	a.openModal(m)
	m.ensureRowVisible(a)
}

// searchScopeDir normalizes a folder scope: "" and the project root
// both mean "the whole project" and come back as "", so a scoped search
// of the root is indistinguishable from Find in project — same title,
// same flashes, same ⟳. The tree's root may be spelled differently from
// rootDir (symlinked checkout), so both are compared.
func (a *App) searchScopeDir(dir string) string {
	if dir == "" || dir == a.rootDir {
		return ""
	}
	if a.tree != nil && a.tree.Root != nil && dir == a.tree.Root.Path {
		return ""
	}
	return dir
}

// projectSearchVerb is how flashes and the list title name a search:
// "Find in project" for the whole tree, "Find in internal/app/" for a
// folder — the folder spelled project-relative, the way the ≡ folder
// rows spell it (relativeFolderLabel), so the reader sees exactly which
// subtree the answer covers.
func (a *App) projectSearchVerb(dir string) string {
	if dir == "" {
		return "Find in project"
	}
	return "Find in " + a.relativeFolderLabel(dir)
}

// scopeSearchPaths keeps the index entries under dir. The index holds
// ROOT-RELATIVE paths, so the folder is made root-relative too and
// compared as a path-segment prefix (with the trailing separator) —
// "internal/app" must not admit "internal/application/x.go". Slashes are
// normalized on both sides because `git ls-files` always answers with
// "/" while the fallback walk uses the OS separator.
//
// A folder outside the root (a stale node after an external move)
// answers nil, which the caller reports as "nothing indexed there".
func (a *App) scopeSearchPaths(paths []string, dir string) []string {
	rel, err := filepath.Rel(a.rootDir, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	if rel == "." {
		return paths
	}
	prefix := filepath.ToSlash(rel) + "/"
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(filepath.ToSlash(p), prefix) {
			out = append(out, p)
		}
	}
	return out
}

// promptFindInFolder asks what to look for under dir, then runs the
// scoped search. The prompt is Find in project's (menuFindInProject):
// same seed ranking, same search-history kind — a query typed here is
// one the next Find in project should offer, and vice versa — and the
// toggles are read when the search runs, not when the prompt opened.
//
// The root goes through here too (the tree root's row): searchScopeDir
// folds it into a project search, and the prompt says "every file" so
// it doesn't promise a narrower walk than it will do.
func (a *App) promptFindInFolder(dir string) {
	seed := a.projectSearchSeed()
	dir = a.searchScopeDir(dir)
	title, hint := "Find all in folder", "searches "+a.relativeFolderLabel(dir)
	if dir == "" {
		title, hint = "Find in project", "searches every file"
	}
	a.openSearchPrompt(title, hint, seed, history.SearchFind, func(app *App, v string) {
		app.startFolderSearch(v, app.findOptions(), dir)
	})
}

// ctxFindInFolder is the tree's folder row "Find all in folder…" (and
// the root's "Find in project…"): a text search scoped to the clicked
// directory. Folders only — openTreeContext never offers it on a file.
func ctxFindInFolder(a *App, n *filetree.Node) {
	if !n.IsDir {
		return
	}
	a.promptFindInFolder(n.Path)
}

// findInFolderContextLabel names the tree row for n: the root's row is
// the whole project and says so, every other folder gets the folder verb.
func (a *App) findInFolderContextLabel(n *filetree.Node) string {
	if a.tree != nil && n == a.tree.Root {
		return "Find in project…"
	}
	return "Find all in folder…"
}

// menuFindInFolder is the ≡ Find twin of the tree's folder row (every
// file action also lives in ≡ — a terminal that eats right-clicks must
// not lose the verb). It acts on the ACTIVE folder, the one the ≡ folder
// rows (New file, Copy folder…) already target.
//
// With no subfolder active the row stays clickable and explains itself
// rather than quietly becoming a second Find in project: the user asked
// for a narrower search and should learn how to name the folder.
func (a *App) menuFindInFolder() {
	a.closeMenu()
	if !a.hasActiveSubfolder() {
		a.flash("Find all in folder: click a folder in the tree first — Find in project searches everything")
		return
	}
	a.promptFindInFolder(a.activeFolder)
}

// findInFolderLabel is the ≡ row's dynamic label, naming the folder it
// will search the way copyFolderLabel does — the suffix front-truncated
// to maxLabelSuffix so the deep end of the path (the part that tells two
// folders apart) survives.
func (a *App) findInFolderLabel() string {
	const base = "Find all in folder"
	if !a.hasActiveSubfolder() {
		return base
	}
	rel := a.relativeFolderLabel(a.activeFolder)
	suffix := " (" + rel + ")"
	if runeLen(suffix) > maxLabelSuffix {
		keep := max(maxLabelSuffix-runeLen(" (…)"), 4)
		if r := []rune(rel); keep < len(r) {
			rel = "…" + string(r[len(r)-keep:])
		}
		suffix = " (" + rel + ")"
	}
	return base + suffix
}

// projectSearchRows compacts each hit into a display row, reusing the
// in-file compaction so the two lists render code identically — leading
// indentation off, interior tabs as one space, which keeps the display
// text aligned rune-for-rune with the buffer.
func (a *App) projectSearchRows(hits []search.Hit) []findAllRow {
	rows := make([]findAllRow, 0, len(hits))
	for _, h := range hits {
		text, trimmed := compactLine(h.Text)
		n := runeLen(text)
		hit, hitW := h.Col-trimmed, h.Width
		if hit < 0 {
			hitW += hit
			hit = 0
		}
		if hit > n {
			hit, hitW = n, 0
		}
		if hit+hitW > n {
			hitW = n - hit
		}
		hitW = max(hitW, 0)
		rows = append(rows, findAllRow{
			line: h.Line, col: h.Col, width: h.Width,
			text: text, hit: hit, hitW: hitW,
			path:  h.Path,
			label: fmt.Sprintf("%s:%d", a.projectSearchLabel(h.Path), h.Line+1),
		})
	}
	return rows
}

// projectSearchLabel is the path as the row should name it: relative to
// the project root, since every hit shares that prefix and repeating it
// on every row would push the code off the right edge.
func (a *App) projectSearchLabel(path string) string {
	if rel, err := filepath.Rel(a.rootDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(path)
}

// openSelected is project mode's accept: open the file the highlighted
// row names, put the cursor on the hit, and close the list.
//
// The list closes FIRST, so the editor band is back to full height before
// the cursor is placed — otherwise the centering in jumpToSelected would
// measure against a band the panel is still taking rows out of, and the
// line would land off-centre the moment the panel left.
func (m *findAllModal) openSelected(a *App) {
	// A pinned list is a worklist: the jump commits but the panel stays.
	if !m.pinned {
		a.closeModal()
		// The list is going, so the tint it scattered over files the
		// user merely clicked through goes with it. The accepted file
		// is re-tinted by the jump below, and Esc clears that one.
		a.clearProjectFindTints()
	}
	m.jumpToSelected(a)
}

// jumpToSelected opens the file the highlighted row names and puts the
// cursor on the hit, leaving the list exactly as it is. It is the
// navigation half of openSelected, split out because a single CLICK on
// a row wants it WITHOUT the close: the pointer is already on the list,
// so the user is working the results — look at this one, then that
// one — and a list that vanished under the first click would have to be
// re-run to see the second. That is the in-file list's preview contract
// (a click moves the editor, the list stays), and it is safe here for
// the reason keyboard walking is NOT: a click is one deliberate gesture
// per file opened, not a tab left behind for every row scrolled past.
//
// The band is read AFTER the caller has decided whether the panel is
// still there, so the centering measures against the rows the editor
// will actually have.
func (m *findAllModal) jumpToSelected(a *App) {
	rp := m.selectedRow()
	if rp == nil || rp.path == "" {
		return
	}
	r := *rp
	a.openFile(r.path)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != r.path {
		return // the open failed and already flashed why
	}
	tab.MoveCursorTo(editor.Position{Line: r.line, Col: r.col}, false)
	tab.MarkJump() // an on-screen edge-row hit still gets context
	// Centered rather than merely scrolled into view, for the reason the
	// in-file preview centers: a minimal scroll parks the line on the
	// last row, showing everything before it and nothing after, which is
	// useless when the question is "what is this line doing?".
	if _, _, ew, eh := a.editorRect(); !tab.CursorLineVisible(eh) {
		tab.CenterOnCursor(ew, eh)
	}
	// The hit is left selected so it is visible the instant the file
	// opens — the same courtesy the find bar's current match gets. The
	// tint is recorded so the list's dismissal (or Esc) can take it back.
	a.tintForProjectFind(tab, m.query, m.opts)
}

// projFindTint is one file's entry in App.projFindTints: the query a
// project-mode list lit, and the find query (and the options it ran
// under) the tab held BEFORE the first tint, so taking the tint back
// restores rather than blanks.
type projFindTint struct {
	query     string
	prior     string
	priorOpts editor.FindOptions
}

// tintForProjectFind installs query as tab's find tint, under the list's
// options, and records it. The prior query is captured only on the FIRST
// tint of a path — a second click into the same file must not record the
// list's own tint as the thing to restore.
//
// The options go on the tab because the tint is the list's answer drawn
// in the file: a match-case list that lit every case variant on arrival
// would contradict the row the user just clicked.
func (a *App) tintForProjectFind(tab *editor.Tab, query string, opts editor.FindOptions) {
	if a.projFindTints == nil {
		a.projFindTints = map[string]projFindTint{}
	}
	rec, seen := a.projFindTints[tab.Path]
	if !seen {
		rec.prior = tab.FindQuery
		rec.priorOpts = tab.FindOpts
	}
	rec.query = query
	a.projFindTints[tab.Path] = rec
	tab.SetFindOptions(opts)
	tab.SetFindQuery(query)
}

// clearProjectFindTints takes back every tint a project-mode list left
// behind. A tab whose query has since changed is left alone — the user
// (or the find bar) owns that state now, and writing over it would
// clobber a search they started themselves. Closed tabs simply have
// nothing left to clear.
func (a *App) clearProjectFindTints() {
	if len(a.projFindTints) == 0 {
		return
	}
	for i := range a.tabs {
		tab := a.tabs[i]
		if tab == nil {
			continue
		}
		rec, ok := a.projFindTints[tab.Path]
		if !ok || tab.FindQuery != rec.query {
			continue
		}
		// Options first, so a restored prior query is re-scanned under
		// the options it originally ran under.
		tab.SetFindOptions(rec.priorOpts)
		if rec.prior != "" {
			tab.SetFindQuery(rec.prior)
		} else {
			tab.ClearFind()
		}
	}
	a.projFindTints = nil
}

// titleText names the list and, in project mode, admits when the result
// cap cut it short. A silently truncated list reads as "that's all of
// them", which is the one wrong answer a search tool can give.
//
// The heading is what a project-mode producer that is NOT a text search
// substitutes for "Find in project" — references, today
// (lspreferences.go). The truncation clause stays shared rather than
// duplicated per producer, because the honesty it buys is the part
// neither of them may skip.
//
// Text searches also name any non-default match options: the list is
// often opened with no find bar on screen, and a match-case list that
// didn't say so would read as "these are all the occurrences". Heading
// producers never carry options (their rows aren't a text match), so
// they get no note.
func (m *findAllModal) titleText() string {
	note := ""
	if m.heading == "" {
		note = findOptionsNote(m.opts)
	}
	if !m.project {
		return fmt.Sprintf("Find all %q%s", m.query, note)
	}
	verb := "Find in project"
	if m.scopeName != "" {
		// A folder-scoped search names its folder, so a list that
		// covers one subtree never reads as the whole project's answer.
		verb = "Find in " + m.scopeName
	}
	if m.heading != "" {
		verb = m.heading
	}
	if m.truncated {
		return fmt.Sprintf("%s %q%s — first %d", verb, m.query, note, len(m.rows))
	}
	return fmt.Sprintf("%s %q%s", verb, m.query, note)
}

// footerHints returns the key hints in decreasing width; draw takes the
// widest that fits. Project mode says "open" rather than "accept" (that
// press changes which file you are looking at) and drops "preview", which
// it deliberately does not do.
func (m *findAllModal) footerHints() []string {
	if m.pinned {
		// The pinned panel is mouse-driven — the hint teaches the
		// buttons, not keys the editor now owns.
		return []string{
			" click preview · double-click jump · ✕ dismiss · ◆ unpin ",
			" click preview · ✕ dismiss · ◆ unpin ",
			" ◆ unpins ",
		}
	}
	if m.project {
		return []string{
			" ↑↓ walk · enter open · del dismiss · / filter · p pin · esc close ",
			" ↑↓ walk · enter open · / filter · esc close ",
			" enter open · esc close ",
			" esc close ",
		}
	}
	return []string{
		" ↑↓ preview · enter accept · del dismiss · / filter · p pin · d dock · esc back ",
		" ↑↓ preview · enter accept · / filter · esc back ",
		" enter accept · esc back ",
		" esc back ",
	}
}

// Compile-time check that projectSearchEvent really is a tcell.Event.
var _ tcell.Event = (*projectSearchEvent)(nil)
