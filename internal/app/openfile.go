// =============================================================================
// File: internal/app/openfile.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// openfile.go is "Open file…": open a file by TYPING its path — anywhere
// on disk, not just inside the project the tree and the finder index.
//
// WHY A SECOND OPEN. Every other door to a file is project-scoped: a tree
// click, the fuzzy finder (Esc-p / ⌘P) over the root's index, Recent
// files. A file outside the root — ~/.zshrc, a config under /etc, a
// sibling project's README — had no way in short of Open folder (which
// re-roots, i.e. tears the workspace down) or quitting to the CLI. The
// doors are ⌘O (metakeys.go) and ≡ File "Open file…", its
// never-the-only-path twin. No Esc leader: the flat table is out of
// mnemonic letters, and the palette reaches the row for free.
//
// THE PROMPT. Pre-filled with the active file's folder (else the root)
// plus a trailing /, so the common case — something near what is on
// screen — is an append rather than a retype, and Tab can list it at
// once. Paths are spelled as Open folder spells them: absolute, ~/…, or
// relative to the ROOT, never the process cwd (the terminal panel's `cd`
// moves that; the root is in the title bar).
//
// TAB COMPLETION (completeOpenPath). Shell-style, one segment at a time,
// on the prompt's generic `complete` hook (modals.go):
//
//	value "~/projs/go/ce"   →  dir part "~/projs/go/"   prefix "ce"
//	                               │
//	          ReadDir(expanded) ───┴── names starting with the prefix
//	               ├─ none  → value unchanged, note "no match"
//	               ├─ one   → dir part + name  (+ "/" for a folder)
//	               └─ many  → dir part + their longest common prefix,
//	                          note "N matches: a.go  b/  c.go"
//
// Only the LAST segment is ever rewritten, so the user's own spelling of
// the directory (~, relative) survives — completing never turns "~/x"
// into "/Users/me/x" under the caret. Dotfiles are offered only when the
// prefix starts with "." (the shell's rule; otherwise every Tab in ~ is a
// page of caches). Exact-case matches win; only when there are none does
// a case-folded match count, because macOS volumes are usually
// case-insensitive and "readme" should still find README.md.
//
// WHAT IT OPENS. openFile, the single entry point every other open uses,
// so the tab is wired, announced to LSP/plugins, recorded in nav history
// and in the recent-files ring (which stores an outside-root path
// absolute, so ⌘E brings it back). One difference, for a file OUTSIDE the
// root: the active folder stays where it was. openFile makes the opened
// file's folder active so the next New file lands beside it; for a
// folder the tree cannot show, that would arm the ≡ folder verbs
// (Rename / Delete / Copy folder) on, say, the home directory.
//
// A MISTAKE KEEPS THE TYPING. A missing path, a folder, or a non-regular
// file re-opens the prompt holding the same value with the reason under
// the field — a long path that is one segment wrong should be an edit,
// not a retype. A folder gets its trailing / so the next Tab lists it.
// Esc still leaves.

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// openPathHint is the prompt's subtitle. Kept under 50 columns: the
// prompt modal is 54 wide and clips its hint at the border.
const openPathHint = "~/path, /abs, or root-relative · tab completes"

// -----------------------------------------------------------------------------
// Doors
// -----------------------------------------------------------------------------

// menuOpenFileByPath is ≡ File "Open file…" and ⌘O: prompt for a path,
// seeded with the folder of what the user is looking at.
func (a *App) menuOpenFileByPath() {
	a.closeMenu()
	a.promptOpenPath(a.openPathSeed(), "")
}

// promptOpenPath opens the path prompt holding initial, with note
// already under the field — empty for a fresh ask, the refusal's reason
// when a submit bounced (openPathTyped).
func (a *App) promptOpenPath(initial, note string) {
	a.openModal(&promptModal{
		title:        "Open file",
		hint:         openPathHint,
		field:        newTextField(initial),
		callback:     func(app *App, v string) { app.openPathTyped(v) },
		complete:     func(app *App, v string) (string, string) { return app.completeOpenPath(v) },
		completeNote: note,
	})
}

// openPathSeed is the prompt's initial value: the active file's folder,
// else the project root, in display form (~ for home) with a trailing /
// so typing a name — or pressing Tab — continues the path.
func (a *App) openPathSeed() string {
	dir := a.copyToRoot()
	if t := a.activeTabPtr(); t != nil && t.Path != "" {
		dir = filepath.Dir(t.Path)
	}
	s := displayPath(dir)
	if !strings.HasSuffix(s, "/") { // "/" itself already ends in one
		s += "/"
	}
	return s
}

// -----------------------------------------------------------------------------
// Submit
// -----------------------------------------------------------------------------

// openPathTyped is the prompt's callback: resolve, then open — or bounce
// back to the prompt with the value intact and the reason under it.
func (a *App) openPathTyped(value string) {
	path, isDir, err := a.resolveOpenPath(value)
	if err != nil {
		retry := strings.TrimSpace(value)
		if isDir && !strings.HasSuffix(retry, "/") {
			retry += "/"
		}
		a.promptOpenPath(retry, err.Error())
		return
	}
	a.openFileAnywhere(path)
}

// resolveOpenPath turns what the user typed into an absolute path to an
// existing regular file, or explains why it isn't one. isDir reports the
// one refusal the caller treats specially (a folder: re-prompt with a
// trailing / so Tab lists its contents).
//
// Non-regular files (fifos, sockets, devices) are refused here rather
// than left to editor.NewTab: a read of a fifo with no writer blocks the
// main loop forever, and that is not a mistake the user should be able
// to make by typing /dev/stdin.
func (a *App) resolveOpenPath(value string) (path string, isDir bool, err error) {
	p := strings.TrimSpace(value)
	if p == "" {
		return "", false, errors.New("no file given")
	}
	p = a.expandOpenPath(p)
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, fmt.Errorf("no such file: %s", displayPath(p))
	case err != nil:
		return "", false, fmt.Errorf("cannot open %s: %v", displayPath(p), err)
	case info.IsDir():
		return p, true, fmt.Errorf("%s is a folder — type a file name, or tab to list it", displayPath(p))
	case !info.Mode().IsRegular():
		return "", false, fmt.Errorf("%s is not a regular file", displayPath(p))
	}
	return p, false, nil
}

// expandOpenPath is the one spelling rule shared by submit and Tab: ~
// expanded, relative paths joined to the project root (never the process
// cwd — see the file comment), cleaned. The trailing separator of a
// "dir/" spelling is dropped by Clean, which ReadDir and Stat don't mind.
func (a *App) expandOpenPath(p string) string {
	p = expandHome(p)
	if !filepath.IsAbs(p) {
		// copyToRoot rather than a.rootDir for its own reason: it is the
		// tree's absolute root, never a verbatim "." launch argument.
		p = filepath.Join(a.copyToRoot(), p)
	}
	return filepath.Clean(p)
}

// openFileAnywhere opens path through openFile, keeping the active folder
// when the file lies outside the project (the file comment's WHAT IT
// OPENS). The inside test resolves symlinks on BOTH sides — the house
// confinement rule — so a temp root reached through /var → /private/var
// still counts its own files as inside.
func (a *App) openFileAnywhere(path string) {
	keep := a.activeFolder
	a.openFile(path)
	if !pathInside(evalSymlinksOr(path), evalSymlinksOr(a.copyToRoot())) {
		a.setActiveFolder(keep)
	}
}

// evalSymlinksOr resolves p's symlinks, falling back to p itself when it
// can't (a path that vanished between the open and this check). The
// fallback errs toward "outside", which only ever leaves the active
// folder alone — the harmless direction.
func evalSymlinksOr(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// -----------------------------------------------------------------------------
// Tab completion
// -----------------------------------------------------------------------------

// completeOpenPath is the prompt's Tab: it returns the value to put back
// in the field and a note for the row under it. See the file comment's
// diagram for the three outcomes.
func (a *App) completeOpenPath(value string) (next, note string) {
	// A bare "~" is the home folder itself, not a name beginning with ~:
	// give it its slash, the same as completing any folder.
	if value == "~" {
		return "~/", ""
	}
	// Split after the last separator: everything up to it is the folder
	// AS THE USER SPELLED IT, the rest is the name being completed. No
	// separator at all means a name in the root.
	cut := strings.LastIndex(value, "/") + 1
	dirPart, prefix := value[:cut], value[cut:]
	dir := a.copyToRoot()
	if dirPart != "" {
		dir = a.expandOpenPath(dirPart)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return value, "can't list " + displayPath(dir)
	}
	names := openPathMatches(dir, entries, prefix)
	switch len(names) {
	case 0:
		return value, "no match"
	case 1:
		return dirPart + names[0], ""
	}
	// Several: extend to what they all share, but only ever EXTEND. A
	// case-folded set can share less than the prefix the user typed
	// ("read" → README.md, Readme.txt share only "R"), and Tab must never
	// delete letters.
	next = value
	if lcp := commonRunePrefix(names); len([]rune(lcp)) > len([]rune(prefix)) {
		next = dirPart + lcp
	}
	// The count leads so that the draw's clip at the field's width cuts
	// names, never the number that says how many there are.
	return next, fmt.Sprintf("%d matches: %s", len(names), strings.Join(names, "  "))
}

// openPathMatches lists dir's entries that complete prefix, in ReadDir's
// (sorted) order, folders suffixed with "/". Exact-case matches win; the
// case-folded set is the fallback when there are none (file comment).
func openPathMatches(dir string, entries []os.DirEntry, prefix string) []string {
	showDot := strings.HasPrefix(prefix, ".")
	lower := strings.ToLower(prefix)
	var exact, folded []string
	for _, e := range entries {
		name := e.Name()
		if !showDot && strings.HasPrefix(name, ".") {
			continue
		}
		var into *[]string
		switch {
		case strings.HasPrefix(name, prefix):
			into = &exact
		case strings.HasPrefix(strings.ToLower(name), lower):
			into = &folded
		default:
			continue
		}
		if entryIsDir(dir, e) {
			name += "/"
		}
		*into = append(*into, name)
	}
	if len(exact) > 0 {
		return exact
	}
	return folded
}

// entryIsDir reports whether e is a folder, FOLLOWING a symlink — a link
// to a folder must complete with a "/" so the next Tab descends into it,
// and DirEntry.IsDir answers for the link itself.
func entryIsDir(dir string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && info.IsDir()
}

// commonRunePrefix is the longest prefix every string in ss shares,
// compared by rune so a multi-byte name is never cut mid-character.
func commonRunePrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	first := []rune(ss[0])
	n := len(first)
	for _, s := range ss[1:] {
		r := []rune(s)
		i := 0
		for i < n && i < len(r) && r[i] == first[i] {
			i++
		}
		n = i
	}
	return string(first[:n])
}
