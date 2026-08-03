// =============================================================================
// File: internal/app/plugindeco.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// plugindeco.go is the ambient half of the plugin system: the hooks
// that fire on editor events, and the decoration providers whose output
// gets painted over the code. Together they're what makes a plugin
// something that keeps up with you rather than something you go and
// click.
//
// House rules:
//
//   - **Decorations are a DecorationSource, not a Render branch.** The
//     overlay system exists so every "paint something over the code"
//     feature merges through one path; a plugin is emphatically not the
//     exception. Sources are asked per frame, so the source only READS
//     the cached map — the commands run on events, off the main loop.
//   - **Precedence is git < plugin < LSP.** A plugin mark outranks the
//     git change bar because the user installed it deliberately and the
//     git bar is ambient on every line they touch; it loses to gopls
//     because a real compile error is the more urgent thing to know,
//     and there is only one gutter cell to say it in.
//   - **Marks are keyed by (file, provider).** A re-run replaces its
//     own findings and touches nobody else's — which is what lets three
//     providers watch one file without erasing each other on every save.
//   - **A provider's exit status is ignored.** Linters exit non-zero
//     precisely when they have something to say, so the output is
//     parsed either way; a provider whose binary isn't installed prints
//     nothing parseable and therefore contributes nothing, which is the
//     silent-degradation contract falling out for free rather than
//     being special-cased.
//   - **The edit timer is armed only while something listens.** The
//     event loop is idle-driven, so a standing timer would wake a
//     resting editor forever — the same constraint the caret blink is
//     built around.

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/plugins"
	"github.com/rohanthewiz/ced/internal/theme"
)

// pluginEditDebounce is how long the editor waits after the last buffer
// change before re-running "edit" providers. Longer than the LSP's
// 300ms didChange debounce on purpose: this spawns a PROCESS, and a
// linter restarted every third keystroke would be a space heater.
const pluginEditDebounce = 800 * time.Millisecond

// pluginDecoMark is the gutter glyph for every plugin diagnostic;
// severity is carried by color, matching how the LSP source works.
// Deliberately not the LSP's ● — when both have something to say about
// a file, being able to tell which is which at a glance is the point.
const pluginDecoMark = '◆'

// pluginDecoEvent carries one provider's parsed findings for one file.
type pluginDecoEvent struct {
	when  time.Time
	seq   int
	path  string
	key   string
	diags []plugins.Diagnostic
}

// When implements tcell.Event.
func (e *pluginDecoEvent) When() time.Time { return e.when }

// pluginHookDoneEvent carries a finished hook back to the main loop.
type pluginHookDoneEvent struct {
	when   time.Time
	seq    int
	label  string
	path   string
	output plugins.OutputMode
	stdout []byte
	stderr []byte
	err    error
}

// When implements tcell.Event.
func (e *pluginHookDoneEvent) When() time.Time { return e.when }

// pluginEditEvent fires when the post-edit debounce expires.
type pluginEditEvent struct {
	when time.Time
	seq  int
}

// When implements tcell.Event.
func (e *pluginEditEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Event dispatch
// -----------------------------------------------------------------------------

// pluginsOnEvent runs every hook and decoration provider that matches
// ev for tab's file. Each match gets its own goroutine and posts its
// own event — one slow linter must not delay a fast one, and neither
// may touch App state.
//
// The generation is stamped now, not read later: a reload or a flip of
// the kill switch while these are in flight has to invalidate them, and
// the only honest answer to "which inventory was this computed under?"
// is the one that was live when it started.
func (a *App) pluginsOnEvent(ev plugins.Event, tab *editor.Tab) {
	if !a.pluginsOn() || tab == nil || tab.Path == "" {
		return
	}
	base := filepath.Base(tab.Path)
	vars := a.pluginVars(tab)
	seq := a.plugins.seq
	scr := a.screen
	dir := a.rootDir
	path := tab.Path

	for _, p := range a.plugins.list {
		env := pluginEnvFor(p, vars)
		for _, h := range p.Hooks {
			if !h.Matches(ev, base) {
				continue
			}
			go func() {
				stdout, stderr, err := pluginShell(dir, h.Command, env, "")
				_ = scr.PostEvent(&pluginHookDoneEvent{
					when: time.Now(), seq: seq,
					label: p.Name, path: path, output: h.Output,
					stdout: stdout, stderr: stderr, err: err,
				})
			}()
		}
		for _, d := range p.Decorations {
			if !d.Matches(ev, base) {
				continue
			}
			key := providerKey(p, d)
			go func() {
				stdout, stderr, _ := pluginShell(dir, d.Command, env, "")
				// stdout AND stderr: `go vet` and a good half of the Go
				// toolchain report findings on stderr, so a provider
				// that only read stdout would silently see nothing from
				// the most obvious thing a user would wire up first.
				// The exit status is ignored — see the file comment.
				text := string(stdout)
				if len(stderr) > 0 {
					text += "\n" + string(stderr)
				}
				_ = scr.PostEvent(&pluginDecoEvent{
					when: time.Now(), seq: seq, path: path, key: key,
					diags: filterDiagsForFile(plugins.ParseDiagnostics(text), path, dir),
				})
			}()
		}
	}
}

// handlePluginDeco files one provider's findings against one file,
// replacing whatever that provider said last time. An empty result
// still replaces — that's how findings DISAPPEAR once the user fixes
// them, and a provider that goes quiet must not leave its last marks
// stuck on the screen.
func (a *App) handlePluginDeco(e *pluginDecoEvent) {
	if e == nil || e.seq != a.plugins.seq {
		// Reloaded or disabled since this run started — the provider
		// that produced these may no longer exist.
		return
	}
	if a.plugins.decos == nil {
		a.plugins.decos = make(map[string]map[string][]plugins.Diagnostic)
	}
	byKey := a.plugins.decos[e.path]
	if byKey == nil {
		byKey = make(map[string][]plugins.Diagnostic)
		a.plugins.decos[e.path] = byKey
	}
	if len(e.diags) == 0 {
		delete(byKey, e.key)
		if len(byKey) == 0 {
			delete(a.plugins.decos, e.path)
		}
		return
	}
	byKey[e.key] = e.diags
}

// handlePluginHookDone surfaces a finished hook. Hooks are ambient —
// they run on every save, unasked — so success is silent unless the
// hook chose otherwise, and only a real failure is allowed to interrupt
// with a modal.
func (a *App) handlePluginHookDone(e *pluginHookDoneEvent) {
	if e == nil || e.seq != a.plugins.seq {
		return
	}
	if e.err != nil {
		a.flash(fmt.Sprintf("%s hook failed: %v", e.label, e.err))
		return
	}
	switch e.output {
	case plugins.OutputFlash:
		a.flash(e.label + ": " + firstLineOf(string(e.stdout)))
	case plugins.OutputInfo:
		lines := strings.Split(strings.TrimRight(string(e.stdout), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			return // nothing to say — don't open an empty modal on save
		}
		a.openInfo(e.label, lines)
	case plugins.OutputReload:
		a.reloadPluginTarget(e.path, e.label)
	}
}

// pluginsAfterEvent runs on the dispatch tail and (re-)arms the
// post-edit debounce whenever a buffer actually changed. Mirrors
// autoSaveAfterEvent's summed-EditRev signature: edits arrive through
// far too many paths (keys, paste, modals, undo, a plugin's own
// replace) to hook each one, and this is a handful of integer adds
// when idle.
func (a *App) pluginsAfterEvent() {
	if !a.pluginsOn() || !a.pluginsWantEdit() {
		return
	}
	sig := 0
	for _, t := range a.tabs {
		sig += t.EditRev
	}
	if sig == a.plugins.editRev {
		return
	}
	a.plugins.editRev = sig
	a.armPluginEditTimer()
}

// pluginsWantEdit reports whether anything in the inventory listens for
// the edit event. Without this check an editor with three save-only
// plugins would still wake itself 800ms after every keystroke to
// discover it had nothing to do.
func (a *App) pluginsWantEdit() bool {
	for _, p := range a.plugins.list {
		for _, h := range p.Hooks {
			if h.Matches(plugins.EventEdit, anyFileProbe) {
				return true
			}
		}
		for _, d := range p.Decorations {
			if d.Matches(plugins.EventEdit, anyFileProbe) {
				return true
			}
		}
	}
	return false
}

// anyFileProbe is the filename pluginsWantEdit tests globs against. The
// question it asks is "could an edit hook ever fire?", not "would it
// fire for this file" — a glob that excludes this probe but matches the
// user's actual files would otherwise disable the timer entirely, so
// the check deliberately errs toward arming.
const anyFileProbe = "*"

// armPluginEditTimer restarts the debounce countdown.
func (a *App) armPluginEditTimer() {
	a.stopPluginEditTimer()
	scr := a.screen
	seq := a.plugins.seq
	a.plugins.editTimer = time.AfterFunc(pluginEditDebounce, func() {
		_ = scr.PostEvent(&pluginEditEvent{when: time.Now(), seq: seq})
	})
}

// stopPluginEditTimer disarms a pending countdown. Called when plugins
// are switched off or reloaded so a timer armed under the old inventory
// can't fire into the new one.
func (a *App) stopPluginEditTimer() {
	if a.plugins.editTimer != nil {
		a.plugins.editTimer.Stop()
		a.plugins.editTimer = nil
	}
}

// handlePluginEditTick fires the edit event for the active tab once the
// user has stopped typing.
func (a *App) handlePluginEditTick(e *pluginEditEvent) {
	if e == nil || e.seq != a.plugins.seq {
		return
	}
	a.plugins.editTimer = nil
	a.pluginsOnEvent(plugins.EventEdit, a.activeTabPtr())
}

// filterDiagsForFile drops findings that name a different file.
//
// Providers are given $FILE and are expected to report on it, but plenty
// of tools print a path anyway (and some, like `go vet`, insist on
// reporting the whole package). Painting another file's errors on this
// one is worse than dropping them, so the decoration layer stays
// strictly per-file. Three shapes count as "this file": no path at all
// (grep -n), a path that resolves to it against the project root, and a
// bare base-name match — the last is loose enough to collide between
// two same-named files in different packages, which is the deliberate
// trade for tools that print paths relative to their own working
// directory.
func filterDiagsForFile(diags []plugins.Diagnostic, target, root string) []plugins.Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	targetBase := filepath.Base(target)
	out := diags[:0]
	for _, d := range diags {
		switch {
		case d.Path == "":
		case resolveDiagPath(d.Path, root) == target:
		case filepath.Base(d.Path) == targetBase:
		default:
			continue
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveDiagPath makes a diagnostic's printed path absolute, relative
// to the project root (which is also the working directory every plugin
// command runs in, so a relative path means the same thing to both).
func resolveDiagPath(p, root string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, p)
}

// -----------------------------------------------------------------------------
// The decoration source
// -----------------------------------------------------------------------------

// pluginDecoSource paints the cached provider findings. Registered per
// tab in openFile between the git and LSP sources — see the precedence
// note in the file comment.
type pluginDecoSource struct{ app *App }

// Decorations converts the cached diagnostics for this tab into spans
// and gutter marks, culled to the visible window. A pure read: the
// commands that produce this data ran on events, so a frame costs a map
// lookup and a walk of however many findings the file has.
func (s pluginDecoSource) Decorations(t *editor.Tab, th theme.Theme, firstLine, lastLine int) ([]editor.Span, []editor.GutterMark) {
	if s.app == nil || !s.app.plugins.enabled {
		return nil, nil
	}
	byKey := s.app.plugins.decos[t.Path]
	if len(byKey) == 0 {
		return nil, nil
	}
	var spans []editor.Span
	// worst tracks the highest severity per line: the gutter has one
	// cell, so when three providers land on a line the loudest wins —
	// the same rule overlapping spans follow.
	worst := make(map[int]plugins.Severity, len(byKey))
	for _, diags := range byKey {
		for _, d := range diags {
			if d.Line < firstLine || d.Line > lastLine {
				continue
			}
			if sev, seen := worst[d.Line]; !seen || d.Severity > sev {
				worst[d.Line] = d.Severity
			}
			start, end := pluginDiagRange(t, d)
			if start.Col >= end.Col {
				continue
			}
			spans = append(spans, editor.Span{
				Start: start,
				End:   end,
				Delta: editor.StyleDelta{
					Underline: true,
					SetFG:     true,
					FG:        pluginSeverityColor(th, d.Severity),
				},
			})
		}
	}
	marks := make([]editor.GutterMark, 0, len(worst))
	for line, sev := range worst {
		marks = append(marks, editor.GutterMark{
			Line:  line,
			Glyph: pluginDecoMark,
			FG:    pluginSeverityColor(th, sev),
		})
	}
	return spans, marks
}

// pluginDiagRange decides what a finding underlines. A tool that gave a
// column gets the word starting there — a single-cell underline is
// invisible in practice, and the word is what the tool is almost always
// pointing at. A tool that gave no column (grep -n, and anything
// line-oriented) underlines the line's text, skipping its indentation
// so the mark tracks the code rather than the whitespace.
func pluginDiagRange(t *editor.Tab, d plugins.Diagnostic) (start, end editor.Position) {
	runes := t.Buffer.LineRunes(d.Line)
	if d.Col < 0 {
		first := 0
		for first < len(runes) && (runes[first] == ' ' || runes[first] == '\t') {
			first++
		}
		return editor.Position{Line: d.Line, Col: first},
			editor.Position{Line: d.Line, Col: len(runes)}
	}
	col := min(d.Col, len(runes))
	stop := col
	for stop < len(runes) && isWordChar(runes[stop]) {
		stop++
	}
	if stop == col {
		// Not sitting on a word (punctuation, or past the line's end) —
		// mark one cell so the finding still has somewhere to show.
		stop = min(col+1, len(runes))
	}
	return editor.Position{Line: d.Line, Col: col}, editor.Position{Line: d.Line, Col: stop}
}

// pluginSeverityColor maps a severity onto the theme's diagnostic
// colors — the same three a theme already has to define for the LSP, so
// a plugin's marks are legible in every shipped palette without themes
// gaining a plugin-specific key.
func pluginSeverityColor(th theme.Theme, sev plugins.Severity) tcell.Color {
	switch sev {
	case plugins.SevError:
		return th.DiagError
	case plugins.SevWarn:
		return th.DiagWarning
	default:
		return th.DiagInfo
	}
}
