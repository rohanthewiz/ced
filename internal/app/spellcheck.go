// =============================================================================
// File: internal/app/spellcheck.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// spellcheck.go is the struct-tag spell check's passive half: misspelled
// words in the NAMES of Go struct tags are underlined as you read.
//
//	Name    string `json:"usrNmae,omitempty"`
//	                         ~~~~  (DiagWarning ink, underlined)
//
// A typo in a tag compiles cleanly and breaks a wire format silently —
// the JSON key, the column, the env var is simply never found — which is
// why this one corner of the code gets a dictionary when nothing else
// does. The judgement (which tags, which pieces of a name, what counts as
// a word) is all in internal/spell; this file is what the editor does with
// it. The active half — the picker that fixes or accepts a word — is
// spellfix.go.
//
// House rules:
//
//   - **A DecorationSource, like every other overlay** (editor/
//     decoration.go). Registered in wireTab right after the git sources:
//     git < spell < validate < plugin < LSP, so a real diagnostic on the
//     same cells wins the colour. No gutter mark — the mark cell belongs
//     to things that are broken, and a spelling is a remark.
//   - **NOT a diagnostic.** Findings stay out of diagsFor: they would
//     fill the Problems panel, answer the gutter tooltip by line, and make
//     the change-bar popup refuse its own cell on every line with a typo.
//     The underline and the picker are the whole surface.
//   - **Per frame, visible lines only.** A tag sits on one line in gofmt'd
//     code, so the check is a line scan (spell.TagNames) over the window
//     Render asks about; a line without a backtick costs one byte search.
//     Nothing is cached, so nothing can go stale — the findings are
//     always about the text on screen.
//   - **The word being typed is left alone.** A piece with the caret at
//     its END is not underlined, so "nam" does not flash red on its way
//     to "name". Clicking INTO a word keeps its mark — only typing
//     position is exempt, which is narrower than cats-todo's rule because
//     ced's caret is also how you point at things.
//   - **The dictionary loads lazily, off the loop.** Building it gunzips
//     ninety thousand words, a visible stall on the main loop, and most
//     sessions never open a struct. spellAfterEvent launches the load the
//     first time a .go tab is active with the check on; the goroutine
//     builds a fresh Dictionary (no UI state) and posts it back,
//     seq-checked. A lost answer (tcell drops PostEvent on a full queue)
//     is relaunched after spellLoadRetry, gitsnapshot's rule.
//   - **Degrade, never nag.** An unreadable user dictionary costs its own
//     words only (spell.Load returns the rest); the error is HELD and
//     shown when the user opens the picker, never flashed unprompted.

package app

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/crash"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/spell"
	"github.com/rohanthewiz/ced/internal/theme"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// spellDictFileName is the user's word list, one word per line, in both
// homes: ~/.config/ced/ for words that are yours everywhere, and the
// project's .ced/ (beside format.json, committed) for its jargon.
const spellDictFileName = "dictionary.txt"

// spellLoadRetry is how long a dictionary load may be in flight before the
// next active Go tab launches another. The load takes milliseconds; the
// only way to reach this is an answer tcell dropped on a full queue.
const spellLoadRetry = 30 * time.Second

// spellDictPathFn resolves the user's global word list. A package var for
// the reason every config seam is one: newTestApp points it at a
// throwaway directory, so no test reads or grows the developer's own.
var spellDictPathFn = userconfig.DictionaryPath

// spellConfigPathFn resolves config.json for the "spellcheck" toggle's
// write. A seam so a test that flips the toggle never rewrites the
// developer's config (themeConfigPathFn's reason).
var spellConfigPathFn = userconfig.DefaultPath

// spellLoadFn builds the dictionary from the embedded lists plus the user
// files. A seam so newTestApp can swap in a cheap stub: a 90k-word gunzip
// in every test that happens to open a .go file would dominate the suite.
var spellLoadFn = spell.Load

// spellState is the per-app state of the check.
type spellState struct {
	// on mirrors the persisted "spellcheck" preference; setSpellcheck is
	// its single write path. Zero (off) in a directly built App, so tests
	// of other features never paint spelling marks unasked.
	on bool

	// dict is the loaded word list, nil until the first load lands. Read
	// and Added to on the main loop only; a reload replaces it whole.
	dict *spell.Dictionary

	// loadErr is the HELD error from the last load: a user dictionary
	// that exists and could not be read. Shown when the picker opens.
	loadErr error

	// seq names the latest load launch; an answer carrying any other
	// value is stale (a reload superseded it) and dropped.
	seq int

	// loading is true while the launch named by seq has not answered;
	// startedAt is when it went out, for the lost-answer relaunch.
	loading   bool
	startedAt time.Time

	// dead latches a load that came back with NO dictionary — the
	// embedded lists failed, which only a corrupt binary can do. It stops
	// spellAfterEvent relaunching on every event; turning the check back
	// on from the ≡ row (setSpellcheck) is the retry.
	dead bool
}

// spellDictEvent carries a loaded dictionary from the loader goroutine to
// the main loop.
type spellDictEvent struct {
	when time.Time
	seq  int
	dict *spell.Dictionary
	err  error
}

// When satisfies tcell.Event.
func (e *spellDictEvent) When() time.Time { return e.when }

// isGoPath reports whether path is a Go source file — the only kind whose
// struct tags are checked.
func isGoPath(path string) bool { return filepath.Ext(path) == ".go" }

// spellProjectDictPath is this project's word list,
// <root>/.ced/dictionary.txt, or "" when there is no root.
func (a *App) spellProjectDictPath() string {
	if a.rootDir == "" {
		return ""
	}
	return filepath.Join(a.rootDir, ".ced", spellDictFileName)
}

// spellDictPaths is where the user's word lists would be, in load order
// (global, then project). Missing files are fine; spell.Load skips them.
func (a *App) spellDictPaths() []string {
	var out []string
	for _, p := range []string{spellDictPathFn(), a.spellProjectDictPath()} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// spellAfterEvent is the lazy trigger: the first time the check is on and
// the active tab is Go, the dictionary starts loading. Cheap enough for
// every event — it returns at the first clause on every event after the
// load lands.
func (a *App) spellAfterEvent() {
	if !a.spell.on || a.spell.dict != nil || a.spell.dead {
		return
	}
	t := a.activeTabPtr()
	if t == nil || !isGoPath(t.Path) {
		return
	}
	if a.spell.loading && time.Since(a.spell.startedAt) < spellLoadRetry {
		return
	}
	a.launchSpellLoad()
}

// launchSpellLoad starts one dictionary load. Everything the goroutine
// needs is copied out first; it touches no App state. A directly built
// App with no screen (a unit test) loads inline, the gitSnap rule.
func (a *App) launchSpellLoad() {
	a.spell.seq++
	a.spell.loading = true
	a.spell.startedAt = time.Now()
	seq, paths, scr, load := a.spell.seq, a.spellDictPaths(), a.screen, spellLoadFn
	if scr == nil {
		d, err := load(paths...)
		a.handleSpellDict(&spellDictEvent{when: time.Now(), seq: seq, dict: d, err: err})
		return
	}
	go func() {
		defer crash.Recover()
		d, err := load(paths...)
		_ = scr.PostEvent(&spellDictEvent{when: time.Now(), seq: seq, dict: d, err: err})
	}()
}

// handleSpellDict lands a load. Only the latest launch applies; an older
// one is dropped, since a reload after a dictionary save must not be
// overwritten by the slower answer it replaced.
//
// A nil dictionary means the EMBEDDED lists failed, which only a corrupt
// binary can do: the check stays dark (nothing is painted without a
// dictionary), the error is held like any other, and dead stops the
// relaunch until the ≡ row is picked again.
func (a *App) handleSpellDict(e *spellDictEvent) {
	if e == nil || e.seq != a.spell.seq {
		return
	}
	a.spell.loading = false
	a.spell.loadErr = e.err
	if e.dict == nil {
		a.spell.dead = true
		return
	}
	a.spell.dict = e.dict
}

// spellAfterSave re-reads the word lists when the file just saved is one
// of them — the hand-edit loop: open ~/.config/ced/dictionary.txt in ced,
// add a word, save, and the underline goes. themeAfterSave's twin.
//
// With the check off, the loaded copy is simply dropped so turning it
// back on reads the file fresh; with it on, a reload goes out at once and
// the old list keeps painting until the new one lands.
func (a *App) spellAfterSave(path string) {
	if path == "" {
		return
	}
	clean := filepath.Clean(path)
	for _, p := range a.spellDictPaths() {
		if filepath.Clean(p) != clean {
			continue
		}
		if !a.spell.on {
			a.spell.dict = nil
			return
		}
		if a.spell.dict != nil || a.spell.loading {
			a.launchSpellLoad()
		}
		return
	}
}

// -----------------------------------------------------------------------------
// Finding the misspelled pieces
// -----------------------------------------------------------------------------

// spellHit is one misspelled piece of a struct-tag name on a line: the
// piece's rune columns, and the whole name's (the picker labels its rows
// with the corrected NAME, which is what the user recognises).
type spellHit struct {
	line               int
	start, end         int
	nameStart, nameEnd int
}

// word is the misspelled piece's text in tab's buffer.
func (h spellHit) word(t *editor.Tab) string {
	return string(t.Buffer.LineRunes(h.line)[h.start:h.end])
}

// spellChecks reports whether t's struct tags are checked right now: the
// check is on, the word list has landed, and t is a Go source file.
func (a *App) spellChecks(t *editor.Tab) bool {
	return a.spell.on && a.spell.dict != nil &&
		t != nil && t.Buffer != nil && isGoPath(t.Path)
}

// spellHits returns every misspelled tag-name piece on lines [first,
// last], in order. Empty when the check does not apply to t.
func (a *App) spellHits(t *editor.Tab, first, last int) []spellHit {
	if !a.spellChecks(t) {
		return nil
	}
	first = max(first, 0)
	last = min(last, t.Buffer.LineCount()-1)
	var hits []spellHit
	for ln := first; ln <= last; ln++ {
		// The byte search first: almost every line of a Go file has no
		// backtick, and LineRunes allocates.
		if !strings.Contains(t.Buffer.Lines[ln], "`") {
			continue
		}
		rs := t.Buffer.LineRunes(ln)
		for _, tn := range spell.TagNames(rs) {
			for _, sp := range a.spell.dict.CheckIdent(string(rs[tn.Start:tn.End])) {
				hits = append(hits, spellHit{
					line:      ln,
					start:     tn.Start + sp.Start,
					end:       tn.Start + sp.End,
					nameStart: tn.Start,
					nameEnd:   tn.End,
				})
			}
		}
	}
	return hits
}

// spellHitAt returns the misspelled piece at p — p inside it or just
// past its end, so a caret that has just finished typing a word still
// names it — ignoring the typing exemption the paint applies: a click or
// a menu row is ASKING about the word.
func (a *App) spellHitAt(t *editor.Tab, p editor.Position) (spellHit, bool) {
	for _, h := range a.spellHits(t, p.Line, p.Line) {
		if h.start <= p.Col && p.Col <= h.end {
			return h, true
		}
	}
	return spellHit{}, false
}

// spellHitNear is the ≡ row's target: the piece at p, else the nearest
// one on p's line (by distance from p to the piece), else none.
func (a *App) spellHitNear(t *editor.Tab, p editor.Position) (spellHit, bool) {
	if h, ok := a.spellHitAt(t, p); ok {
		return h, true
	}
	best, found, bestDist := spellHit{}, false, 0
	for _, h := range a.spellHits(t, p.Line, p.Line) {
		d := h.start - p.Col
		if p.Col > h.end {
			d = p.Col - h.end
		}
		if !found || d < bestDist {
			best, found, bestDist = h, true, d
		}
	}
	return best, found
}

// -----------------------------------------------------------------------------
// The decoration source
// -----------------------------------------------------------------------------

// spellSource paints the underlines. A cheap read per frame (see the
// house rules); the dictionary is never consulted beyond lookups here.
type spellSource struct{ app *App }

// Decorations underlines each misspelled piece in the window, minus the
// one being typed (the primary caret at its end).
func (s spellSource) Decorations(t *editor.Tab, th theme.Theme, firstLine, lastLine int) ([]editor.Span, []editor.GutterMark) {
	if s.app == nil {
		return nil, nil
	}
	var spans []editor.Span
	for _, h := range s.app.spellHits(t, firstLine, lastLine) {
		if h.line == t.Cursor.Line && h.end == t.Cursor.Col {
			continue
		}
		spans = append(spans, editor.Span{
			Start: editor.Position{Line: h.line, Col: h.start},
			End:   editor.Position{Line: h.line, Col: h.end},
			Delta: editor.StyleDelta{Underline: true, SetFG: true, FG: th.DiagWarning},
		})
	}
	return spans, nil
}

// -----------------------------------------------------------------------------
// The switch
// -----------------------------------------------------------------------------

// spellOffLabel and spellOnLabel are the ≡ View toggle's two labels, read
// as the action a click performs. The picker's last row reuses the off
// label, so both doors say the same thing.
const (
	spellOffLabel = "Stop spell-checking struct tags"
	spellOnLabel  = "Spell-check struct tags"
)

// spellcheckToggleLabel is the ≡ View row's label for the current state.
func (a *App) spellcheckToggleLabel() string {
	if a.spell.on {
		return spellOffLabel
	}
	return spellOnLabel
}

// menuToggleSpellcheck is the ≡ View row.
func (a *App) menuToggleSpellcheck() {
	a.closeMenu()
	a.setSpellcheck(!a.spell.on)
}

// setSpellcheck turns the check on or off and persists the choice — the
// single write path of a.spell.on (the ≡ row and the picker's last row
// both come here). The load is not started here: spellAfterEvent runs
// after this very event and starts it if a Go tab is active.
func (a *App) setSpellcheck(on bool) {
	a.spell.on = on
	a.spell.dead = false // re-picking the row is the retry for a failed load
	if on {
		a.flash("Struct-tag spell check on")
	} else {
		a.flash("Struct-tag spell check off")
	}
	if err := userconfig.SaveSpellCheck(spellConfigPathFn(), on); err != nil {
		a.flash("config: " + err.Error())
	}
}
