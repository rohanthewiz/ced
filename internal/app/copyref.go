// =============================================================================
// File: internal/app/copyref.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-06
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Copy reference: put "<absolute path>:<line>" on the system clipboard.
//
// The form every compiler, grep, test runner and agent prompt already
// speaks — paste it into a chat, a terminal (`ced --remote`, `vim +`),
// an issue or a code review and it names one line of one file. The
// absolute path (not root-relative) on purpose: the reference is meant
// to leave ced, and the reader's cwd is unknown, so the one spelling
// that resolves anywhere on this machine wins. The line is 1-based,
// as people and tools count, not the buffer's 0-based index.
//
// Three doors, one verb (copyReferenceFor):
//
//   - editor right-click — the click already moved the caret to the
//     clicked line (contextmenu.go), so the row copies THAT line;
//   - tab right-click — the clicked tab's caret line, in place, like the
//     copy-path rows beside it (the tab is not brought forward);
//   - ≡ File "Copy reference (path:line)" — the active tab's caret line,
//     for terminals that swallow right-click (and the palette for free).
//
// No column: "path:line" is what was asked for and what every consumer
// accepts; a column would make the reference fussier to read for no gain
// in the common "look at this line" hand-off.

package app

import (
	"strconv"

	"github.com/rohanthewiz/ced/internal/editor"
)

// copyReferenceLabel is the row label shared by all three doors, so the
// README pins and the menus can never spell it differently.
const copyReferenceLabel = "Copy reference (path:line)"

// fileReference formats path and a 0-based buffer line as
// "<absolute path>:<1-based line>". A negative line (never expected from
// a live caret) clamps to line 1 rather than emitting ":0" or ":-1",
// which no consumer would resolve.
func fileReference(path string, line int) string {
	if line < 0 {
		line = 0
	}
	return absolutePathFor(path) + ":" + strconv.Itoa(line+1)
}

// copyReferenceFor copies t's reference at its caret line onto the host
// clipboard (OSC 52, tmux passthrough) and flashes what was copied. An
// untitled tab or a closed one has no path to name, so it is a silent
// no-op — the rows that call it are dimmed or absent in that case, and
// keyboard activation racing that dimming must not crash.
func (a *App) copyReferenceFor(t *editor.Tab) {
	if t == nil || t.Path == "" {
		return
	}
	a.copyPathToSystemClipboard(fileReference(t.Path, t.Cursor.Line), "reference")
}

// menuCopyReference is the ≡ File row: the active tab's reference.
func (a *App) menuCopyReference() {
	a.closeMenu()
	a.copyReferenceFor(a.activeTabPtr())
}
