// =============================================================================
// File: internal/app/entrycmd.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// "Shell command…" — run a command line of the user's own on a tree entry,
// naming it with a placeholder: right-click `internal/` → Shell command… →
// `ls -la {{DIR_ENTRY}}` → Enter, and ced's terminal panel runs
//
//	ls -la /abs/path/to/internal
//
// The placeholders (a closed set — actionvars.go's argument: a fixed list
// of well-understood names beats a templating language for one-line
// snippets):
//
//	{{DIR_ENTRY}}  the clicked entry, file or folder
//	{{DIR}}        the folder: the entry itself when it is one, else its
//	               parent — `cd {{DIR}} && make` works from a file too
//
// House rules:
//
//   - **SUBSTITUTED PATHS ARE QUOTED FOR THE SHELL, ABSOLUTE.** A file name
//     is untrusted text — `$(rm -rf ~).txt` is a legal name — so it never
//     reaches the shell unquoted. shellArg leaves a plain path bare (the
//     echoed line stays readable) and single-quotes anything else.
//     Absolute because the panel's cwd is wherever grsh's last `cd` left
//     it (the Paths rule). A placeholder the user already wrapped in quotes
//     ("{{DIR_ENTRY}}" or '{{DIR_ENTRY}}') is replaced quotes and all —
//     wrapping a quoted path in a second pair would break it, and quoting
//     placeholders is what people coming from other tools type by habit.
//
//   - **NO PLACEHOLDER MEANS "APPEND IT".** `wc -l` on a file is `wc -l
//     <file>`; xargs' convention, and the reading nobody has to learn.
//
//   - **IT RUNS, IT DOES NOT STAGE.** runexec stages because the user still
//     owes arguments; here the prompt WAS the composing step and Enter was
//     the decision. The panel echoes the expanded line, so what ran is on
//     screen. A busy panel stages instead (runInTermPanel), and says so.
//
//   - **ced's PANEL, AT EVERY TIER.** The output is the point of an
//     `ls`/`du`/`wc`, and the panel keeps it beside the tree with its
//     clickable file:line parsing. A full-screen program wants "Open in
//     terminal" instead, which is the row right above this one.
//
//   - **THE TEMPLATE IS REMEMBERED, NOT THE EXPANSION.** History records
//     `ls -la {{DIR_ENTRY}}` (search kind history.EntryCommands), so the
//     dropdown offers commands that work on the NEXT entry, not replays of
//     the last one. The field is seeded with the most recent; with no
//     history it is seeded with an example, which is the whole tutorial.

package app

import (
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/history"
)

// The placeholders a shell-command template may name.
const (
	placeholderEntry = "{{DIR_ENTRY}}"
	placeholderDir   = "{{DIR}}"
)

// entryCmdExample seeds the prompt when the project has no history yet: a
// harmless, read-only command that shows the placeholder in use.
const entryCmdExample = "ls -la " + placeholderEntry

// entryCmdHint is the prompt's hint row — kept under copyToHint's 34
// columns so the history dropdown's "↑ recent" tip still fits beside it.
const entryCmdHint = placeholderEntry + " = its path"

// entryCmdTitleMax caps the entry's name in the prompt title (copyTo's
// rule: a long name must not push the verb off the frame).
const entryCmdTitleMax = 28

// expandEntryTemplate substitutes the placeholders in tmpl with entry and
// dir, shell-quoted (see the header for why quoting is not optional).
// Quoted placeholders are listed FIRST: strings.Replacer tries its pairs
// in argument order at each position, so `"{{DIR}}"` is consumed whole
// before the bare `{{DIR}}` pair could match inside it.
func expandEntryTemplate(tmpl, entry, dir string) string {
	e, d := shellArg(entry), shellArg(dir)
	return strings.NewReplacer(
		`"`+placeholderEntry+`"`, e,
		`'`+placeholderEntry+`'`, e,
		placeholderEntry, e,
		`"`+placeholderDir+`"`, d,
		`'`+placeholderDir+`'`, d,
		placeholderDir, d,
	).Replace(tmpl)
}

// entryCommandLine is the line the panel runs for tmpl on entry: the
// template expanded, or — with no placeholder in it — the entry appended.
func entryCommandLine(tmpl, entry string, isDir bool) string {
	entry = absolutePathFor(entry)
	dir := entry
	if !isDir {
		dir = filepath.Dir(entry)
	}
	if !strings.Contains(tmpl, placeholderEntry) && !strings.Contains(tmpl, placeholderDir) {
		return tmpl + " " + shellArg(entry)
	}
	return expandEntryTemplate(tmpl, entry, dir)
}

// ctxEntryCommand is the tree's right-click row: prompt for a command to
// run on the clicked node. The node's path and kind are captured by value
// now — the tree can refresh while the prompt is up.
func ctxEntryCommand(a *App, n *filetree.Node) {
	a.closeModal()
	a.promptEntryCommand(n.Path, n.IsDir)
}

// menuEntryCommand is the ≡ File row: the active file, else the project
// root — openInEditorTarget's target, so the three "hand it to something
// outside" rows agree on what "it" is.
func (a *App) menuEntryCommand() {
	a.closeMenu()
	target := a.openInEditorTarget()
	a.promptEntryCommand(target, target == a.rootDir)
}

// entryCommandLabel names what the ≡ row acts on, so the menu says it
// before the prompt does (runExecutableLabel's pattern).
func (a *App) entryCommandLabel() string {
	if tab := a.activeTabPtr(); tab != nil && tab.Path != "" {
		return "Shell command on file…"
	}
	return "Shell command on project…"
}

// promptEntryCommand asks for the command, then runs it. The prompt is a
// history prompt (Up / ▾ recalls earlier templates).
func (a *App) promptEntryCommand(path string, isDir bool) {
	if path == "" {
		a.flash("Nothing to run a command on")
		return
	}
	a.openSearchPrompt(
		"Shell command on "+entryCmdTitleName(path, isDir),
		entryCmdHint,
		a.entryCmdSeed(),
		history.EntryCommands,
		func(app *App, tmpl string) { app.runEntryCommand(tmpl, path, isDir) },
	)
}

// entryCmdTitleName is the entry's name for the prompt title: the base
// name, a trailing / for a folder (copyToWhat's convention), tail-kept
// when long because the extension is the informative end.
func entryCmdTitleName(path string, isDir bool) string {
	name := filepath.Base(path)
	if isDir {
		name += "/"
	}
	if r := []rune(name); len(r) > entryCmdTitleMax {
		name = "…" + string(r[len(r)-entryCmdTitleMax+1:])
	}
	return name
}

// entryCmdSeed is the field's starting text: the newest remembered
// template, else the example.
func (a *App) entryCmdSeed() string {
	if recent := a.searchHistory(history.EntryCommands); len(recent) > 0 {
		return recent[0]
	}
	return entryCmdExample
}

// runEntryCommand expands tmpl for the entry and runs it in ced's panel,
// recording the template (not the expansion) for next time. Recorded
// where it RUNS, the search-history rule — a cancelled prompt teaches
// nothing.
func (a *App) runEntryCommand(tmpl, path string, isDir bool) {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		return
	}
	a.recordSearch(history.EntryCommands, tmpl)
	if a.runInTermPanel(entryCommandLine(tmpl, path, isDir)) {
		a.flash("Ran in ced's terminal")
		return
	}
	a.flash("Terminal is busy — the command is staged; press Enter when it finishes")
}
