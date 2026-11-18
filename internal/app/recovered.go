// =============================================================================
// File: internal/app/recovered.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-18
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// recovered.go is the in-editor door to rescued edits (N-144). When ced
// cannot ask "save or discard?" — a panic, SIGTERM, a killed tmux pane —
// crashsafety.go writes the dirty buffers to
// ~/.config/ced/recover/<stamp>-<project>/ and says where in a line
// printed after the terminal is restored. Nobody sees that line when the
// pane was killed, and nothing read the folder back.
//
// ≡ File "Recovered edits…" lists this project's rescued files, newest
// first (crash.List reads each folder's INDEX.txt). Its label carries a
// count when there are any, so the row itself is the notice — nothing is
// flashed at startup ("startup notes are HELD for a ≡ label"). A pick
// offers three things to do with that file:
//
//	Open the rescued copy        — read it as a tab
//	Compare with <file>          — the ORIGINAL opens as a tab and is
//	                               compared against the rescued copy; the
//	                               compare panel's [↺] chips then restore
//	                               rescued hunks into it one at a time
//	Read the rescue's INDEX.txt  — why and when it was written
//
// Nothing here writes: the rescued text reaches the user's file only
// through their own edits (or a [↺] revert, one undo step each).
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rohanthewiz/ced/internal/crash"
)

// recoveredCacheFor is how long the label's count is reused. The menu
// redraws its labels every frame while open; reading the recover folder
// each time would be wasted IO for a number that changes only when ced
// itself crashes.
const recoveredCacheFor = 5 * time.Second

// recoveredCache holds the last listing for the label.
type recoveredCache struct {
	at      time.Time
	folders []crash.Folder
}

// recoveredList reads this project's rescues, through the label cache
// unless fresh is set.
func (a *App) recoveredList(fresh bool) []crash.Folder {
	if !fresh && time.Since(a.recovered.at) < recoveredCacheFor {
		return a.recovered.folders
	}
	folders, _ := crash.List(recoverDirFn(), a.rootDir)
	a.recovered = recoveredCache{at: time.Now(), folders: folders}
	return folders
}

// recoveredFileCount totals the rescued files across folders.
func recoveredFileCount(folders []crash.Folder) int {
	n := 0
	for _, f := range folders {
		n += len(f.Files)
	}
	return n
}

// recoveredEditsLabel names the ≡ row — with the count of rescued files
// when there are any, which is how the user learns a rescue happened.
func (a *App) recoveredEditsLabel() string {
	if n := recoveredFileCount(a.recoveredList(false)); n > 0 {
		return fmt.Sprintf("Recovered edits (%d)…", n)
	}
	return "Recovered edits…"
}

// menuRecoveredEdits lists this project's rescued files, newest first.
// With none, it says so and where rescues would be — the row explains
// itself rather than dimming.
func (a *App) menuRecoveredEdits() {
	a.closeMenu()
	folders := a.recoveredList(true)
	n := recoveredFileCount(folders)
	if n == 0 {
		a.flash("No recovered edits for this project — ced writes them to " + recoverDirFn() + " when it can't save")
		return
	}
	var items []paletteItem
	for _, f := range folders {
		for _, r := range f.Files {
			name := "(untitled)"
			if r.Orig != "" {
				name = a.relativePathFor(r.Orig)
			}
			label := f.When.Format("2006-01-02 15:04") + "  " + name
			if f.Why != "" {
				label += "  · " + f.Why
			}
			folder, file := f, r
			items = append(items, paletteItem{label: label, run: func(app *App) {
				app.recoveredFileActions(folder, file)
			}})
		}
	}
	a.openPicker(fmt.Sprintf("Recovered edits — %d files, newest first", n), items)
}

// recoveredFileActions offers what can be done with one rescued file.
// "Compare" is offered only when the original still exists.
func (a *App) recoveredFileActions(f crash.Folder, r crash.Rescued) {
	items := []paletteItem{{label: "Open the rescued copy", run: func(app *App) {
		app.openFile(r.File)
	}}}
	if r.Orig != "" {
		if _, err := os.Stat(r.Orig); err == nil {
			items = append(items, paletteItem{label: "Compare with " + a.relativePathFor(r.Orig), run: func(app *App) {
				app.openFile(r.Orig)
				if t := app.activeTabPtr(); t != nil && t.Path == r.Orig {
					app.compareWithFile(r.File)
				}
			}})
		}
	}
	items = append(items, paletteItem{label: "Read the rescue's " + crash.IndexName, run: func(app *App) {
		app.openFile(filepath.Join(f.Path, crash.IndexName))
	}})
	a.openPicker("Recovered: "+filepath.Base(r.File), items)
}
