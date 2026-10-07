// =============================================================================
// File: internal/app/copypaste.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-11
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Copy / paste for files and folders. Copy arms an internal "file
// clipboard" (just the source path — nothing is read until paste);
// Paste duplicates the source into a destination folder under a
// collision-free name ("name copy.ext", "name copy 2.ext", …), so the
// operation is never destructive.
//
// Surfaces, per the house rule that every file action must be
// reachable from the ≡ menu first:
//
//   • ≡ → "Copy file" / "Copy folder (sub/)"  — active tab / active folder
//   • ≡ → "Paste <name>"                      — into the active folder
//   • tree right-click → "Copy" / "Paste"     — redundant shortcut
//   • Cmd+C / Cmd+V                           — where the terminal delivers
//     them (kitty keyboard protocol → tcell ModMeta); terminals that
//     swallow Cmd still have the menu paths above.
//
// Cmd+C/Cmd+V share the key with text copy/paste, so App.clipKind
// tracks which clipboard (text selection vs file) was armed most
// recently and Cmd+V pastes that one — the same last-write-wins
// behavior a system clipboard has.
//
// Big folders can take seconds to copy, so the paste itself runs in a
// goroutine and posts a pasteDoneEvent back to the main loop — the
// same pattern as zips, formatters, and custom actions.
//
// Paste can only land in a folder the tree shows. Copy to… (copyto.go)
// is the door to anywhere else on disk; it skips the clipboard but runs
// the SAME plan and goroutine (planCopyInto → runCopyPlan), so the two
// verbs share their naming, refusals and failure cleanup:
//
//	Copy ─▶ fileClipPaths ─▶ Paste ──┐
//	                                 ├─▶ planCopyInto ─▶ runCopyPlan ─▶ pasteDoneEvent
//	Copy to… ─▶ prompt (folder) ─────┘     (main loop)     (goroutine)    (main loop)
//
// Both read an open tab's unsaved BUFFER rather than its stale file
// (dirtyBufferOverlay), so a copy holds what the user is looking at.

package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// clipboardKind identifies which internal clipboard was armed most
// recently, so a Cmd+V can route to the right paste action.
type clipboardKind int

const (
	clipNone clipboardKind = iota
	clipText               // copySelection / cutSelection wrote clipBuf
	clipFile               // copyToFileClip armed fileClipPaths
)

// pasteDoneEvent is posted by the paste goroutine when the copy is
// finished (or failed). Carries the destination so the success flash
// can name the file or folder that appeared, plus how many items the
// paste covered — a set's flash says "Pasted 4 items", and the first
// destination alone would under-report it.
//
// Copy to… (copyto.go) runs through the same goroutine and posts the
// same event; into is what tells the two apart on the main loop.
type pasteDoneEvent struct {
	when  time.Time
	dest  string
	count int
	err   error

	// into is the destination FOLDER when the run came from Copy to…,
	// empty for a paste. A paste lands in a folder the tree is showing,
	// so its flash only has to name the new item; a Copy to… usually
	// lands somewhere off screen, so its flash is the only receipt and
	// has to say where.
	into string
	// src is the source behind dest, so a one-item Copy to… can say when
	// the copy had to take another name ("as main copy.go").
	src string
	// buffered counts files written from an open tab's unsaved buffer
	// rather than from disk (copyTreeWith), so the flash can say so.
	buffered int
}

// When satisfies the tcell.Event interface.
func (e *pasteDoneEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Backend: pure functions, no App state.
// -----------------------------------------------------------------------------

// copyFileContents copies the regular file src to a new file dst with
// the given permissions. dst is opened O_EXCL — the caller picks a
// collision-free name via uniquePastePath, and this keeps the same
// never-clobber contract as createEmptyFile / renameFile even if
// something races us to the name.
func copyFileContents(src, dst string, perm os.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// writeNewFile writes data to a new file dst with the given
// permissions — copyFileContents for bytes already in hand (an open
// tab's unsaved buffer). Same O_EXCL never-clobber contract.
func writeNewFile(dst string, data []byte, perm os.FileMode) (err error) {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = out.Write(data)
	return err
}

// copyTree recursively copies the file or directory at src to dst,
// which must not exist yet. Symlinks are recreated as links (not
// followed) — following could loop, or silently pull in content from
// outside the tree being copied — and sockets/devices are skipped,
// mirroring writeZipEntry's rules for the same reasons. On failure the
// caller is expected to remove the partial dst (startPaste does).
func copyTree(src, dst string) error {
	_, err := copyTreeWith(src, dst, nil)
	return err
}

// copyTreeWith is copyTree with an OVERLAY: regular files whose clean
// path is a key of overlay are written from those bytes instead of from
// disk. The overlay is the unsaved content of open tabs
// (dirtyBufferOverlay), so a copy holds what the user is looking at —
// the house rule that any text sent somewhere reads the open tab's
// buffer before the disk. Only files the walk FINDS on disk are
// overlaid: a tab whose file was deleted is not resurrected into the
// copy. buffered counts the overlaid files, for the flash.
//
// The file's mode still comes from disk — the buffer has none, and a
// script copied without its +x would be a quiet loss.
func copyTreeWith(src, dst string, overlay map[string][]byte) (buffered int, err error) {
	info, err := os.Lstat(src)
	if err != nil {
		return 0, err
	}
	switch {
	case info.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
			return 0, err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			n, err := copyTreeWith(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), overlay)
			buffered += n
			if err != nil {
				return buffered, err
			}
		}
		return buffered, nil
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return 0, err
		}
		return 0, os.Symlink(target, dst)
	case info.Mode().IsRegular():
		if data, ok := overlay[filepath.Clean(src)]; ok {
			return 1, writeNewFile(dst, data, info.Mode().Perm())
		}
		return 0, copyFileContents(src, dst, info.Mode().Perm())
	default:
		return 0, nil
	}
}

// uniquePastePath picks a collision-free destination for pasting base
// into dir. The base name itself is used when free; otherwise a
// Finder-style " copy" / " copy N" is inserted before the extension
// ("main.go" → "main copy.go"). Directories and dotfiles get the
// suffix appended whole — splitting ".gitignore" on its "extension"
// would leave an empty stem, and "my.dir copy" reads better than
// "my copy.dir" for a folder.
func uniquePastePath(dir, base string, isDir bool) string {
	return uniquePastePathExcept(dir, base, isDir, nil)
}

// uniquePastePathExcept is uniquePastePath with a set of names already
// RESERVED by this same paste — the multi-source case.
//
// It exists because "is this name free?" is answered against the
// filesystem, and a set is planned before any of it is written: two
// selected files both called same.txt would each find dest/same.txt
// unoccupied and both claim it, and the second copy would then fail on
// O_EXCL after the first had already landed. Planning is the only place
// that can see the collision, so the reservation has to live here rather
// than in the copy.
func uniquePastePathExcept(dir, base string, isDir bool, taken map[string]bool) string {
	cand := filepath.Join(dir, base)
	if _, err := os.Lstat(cand); err != nil && !taken[cand] {
		return cand
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if isDir || stem == "" {
		stem, ext = base, ""
	}
	for n := 1; ; n++ {
		name := stem + " copy" + ext
		if n > 1 {
			name = fmt.Sprintf("%s copy %d%s", stem, n, ext)
		}
		cand = filepath.Join(dir, name)
		if _, err := os.Lstat(cand); err != nil && !taken[cand] {
			return cand
		}
	}
}

// pasteIntoOwnSubtree reports whether destDir is src itself or lives
// inside it. Pasting a folder into its own subtree would make the copy
// walk chase the destination it is writing — the classic infinite
// duplication bug — so startPaste refuses it. The trailing separator
// keeps /proj/foo from matching /proj/foobar, same as tabPathRemoved.
func pasteIntoOwnSubtree(src, destDir string) bool {
	src = filepath.Clean(src)
	destDir = filepath.Clean(destDir)
	if destDir == src {
		return true
	}
	return strings.HasPrefix(destDir, src+string(filepath.Separator))
}

// copyIntoOwnSubtree is pasteIntoOwnSubtree asked twice: of the paths as
// spelled, and again after resolving symlinks on BOTH sides (the house
// confinement rule). A paste only ever targets a tree folder, but Copy
// to… takes a typed path, and "~/link" pointing into the folder being
// copied is the same infinite walk wearing a different name — as is
// macOS's /tmp vs /private/tmp. resolveExisting copes with a destination
// that Copy to… has not created yet.
func copyIntoOwnSubtree(src, destDir string) bool {
	if pasteIntoOwnSubtree(src, destDir) {
		return true
	}
	return pasteIntoOwnSubtree(resolveExisting(src), resolveExisting(destDir))
}

// resolveExisting is filepath.EvalSymlinks for a path whose tail may not
// exist yet: the deepest ancestor that DOES exist is resolved and the
// missing remainder appended as typed. EvalSymlinks alone fails on the
// whole path the moment one component is missing, which would let a
// to-be-created destination under a symlink slip past the guard.
func resolveExisting(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Nothing on the way up resolved (a relative path in a
			// vanished cwd): the spelling as given is all there is.
			return p
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// errCopyIntoSelf is planCopyInto's one refusal: a folder copied into
// its own subtree. The callers word it ("paste" / "copy").
var errCopyIntoSelf = errors.New("a folder can't be copied into itself")

// copyPlanItem is one source and the collision-free path its copy will
// take.
type copyPlanItem struct{ src, dest string }

// planCopyInto plans copying every source in srcs into destDir, BEFORE
// anything is written — the way a workspace edit validates before it
// writes, so every refusal is a main-loop answer the user gets
// immediately, and a set is never half-copied because its third item was
// a folder copied into itself. Names are reserved in order
// (uniquePastePathExcept), so two items with the same basename can't
// both claim "name copy.ext".
//
// Sources that no longer exist are returned in missing (by basename)
// rather than refusing the set; the caller decides how to report them.
// destDir need not exist yet: every name in a missing folder is free.
func planCopyInto(srcs []string, destDir string) (plan []copyPlanItem, missing []string, err error) {
	taken := map[string]bool{}
	for _, src := range srcs {
		info, lerr := os.Lstat(src)
		if lerr != nil {
			missing = append(missing, filepath.Base(src))
			continue
		}
		if info.IsDir() && copyIntoOwnSubtree(src, destDir) {
			return nil, nil, errCopyIntoSelf
		}
		dest := uniquePastePathExcept(destDir, filepath.Base(src), info.IsDir(), taken)
		taken[dest] = true
		plan = append(plan, copyPlanItem{src: src, dest: dest})
	}
	return plan, missing, nil
}

// bufferedNote is the flash suffix that owns up to a copy made from
// unsaved buffers: the copy holds edits the original file on disk does
// not, and a user comparing the two deserves to know why.
func bufferedNote(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return " (with unsaved edits)"
	default:
		return fmt.Sprintf(" (%d with unsaved edits)", n)
	}
}

// -----------------------------------------------------------------------------
// App glue: clipboard state, async paste, main-loop completion.
// -----------------------------------------------------------------------------

// copyToFileClip arms the file clipboard with path. Nothing is copied
// yet — paste reads the source at paste time, so edits made between
// Copy and Paste are included, matching how Finder's Cmd+C behaves.
func (a *App) copyToFileClip(path string) {
	a.copyPathsToFileClip([]string{path})
}

// copyPathsToFileClip is the single write path for the file clipboard,
// arming it with a whole set (the tree's multi-selection) or with one
// path via copyToFileClip above.
//
// Sources that no longer exist are dropped rather than refusing the
// whole gesture: a set the user ticked minutes ago can legitimately have
// lost a file to a git checkout or a shell command, and copying the four
// that are still there beats copying nothing. A refusal is reserved for
// the case where NOTHING survived, which is the only one the user has to
// act on. Existence is re-checked at paste time regardless — this pass
// is about what the flash can honestly claim.
func (a *App) copyPathsToFileClip(paths []string) {
	live := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		switch len(paths) {
		case 0:
			a.flash("Nothing to copy")
		case 1:
			a.flash(fmt.Sprintf("Copy failed: %s no longer exists", filepath.Base(paths[0])))
		default:
			a.flash("Copy failed: none of the selected items still exist")
		}
		return
	}
	a.fileClipPaths = live
	a.clipKind = clipFile
	a.flash(fmt.Sprintf("Copied %s — Paste to duplicate", fileClipLabel(live)))
}

// fileClipLabel names a clipboard set for a flash or a menu row: the
// basename for one item, a count otherwise. One helper so "Copied
// main.go" and "Copied 4 items" cannot drift into two phrasings (the
// git panel's gitPanelTargetLabel rule).
func fileClipLabel(paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return itoa(len(paths)) + " items"
}

// hasFileClip is the menu predicate for the Paste row: true when a
// file or folder has been copied this session. Staleness (source
// deleted after Copy) is caught at paste time with a specific flash
// rather than silently disabling the row.
func (a *App) hasFileClip() bool { return len(a.fileClipPaths) > 0 }

// pasteTargetDir resolves where a menu / Cmd+V paste should land: the
// active folder when it still exists, else the project root. Resolved
// through the tree root (always absolute) rather than a.rootDir, which
// keeps the user's verbatim argument ("." in the common case) — the
// self-subtree guard compares clean absolute paths.
func (a *App) pasteTargetDir() string {
	root := a.rootDir
	if a.tree != nil && a.tree.Root != nil {
		root = a.tree.Root.Path
	}
	folder := a.activeFolder
	if folder == "" {
		return root
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return root
	}
	return folder
}

// startPaste validates the pending file-clipboard paste into destDir
// and kicks off the copy goroutine. All refusals happen here on the
// main loop so the user gets an immediate, specific flash instead of
// an async failure.
func (a *App) startPaste(destDir string) {
	if len(a.fileClipPaths) == 0 {
		a.flash("Nothing to paste — Copy a file or folder first")
		return
	}
	if dinfo, derr := os.Stat(destDir); derr != nil || !dinfo.IsDir() {
		a.flash("Paste failed: destination folder no longer exists")
		return
	}

	// Plan the whole set BEFORE copying anything, the way a workspace
	// edit validates before it writes: every refusal below is a main-loop
	// answer the user gets immediately, and a set that is half-pasted
	// because the third item was a folder pasted into itself is the
	// failure nobody notices. planCopyInto is shared with Copy to…
	// (copyto.go), so the two verbs can never disagree about names.
	plan, missing, perr := planCopyInto(a.fileClipPaths, destDir)
	if perr != nil {
		a.flash("Can't paste a folder into itself")
		return
	}
	if len(plan) == 0 {
		// Every copied source vanished; disarm so the Paste row doesn't
		// keep offering a paste that can never succeed.
		a.fileClipPaths = nil
		if a.clipKind == clipFile {
			a.clipKind = clipNone
		}
		a.flash(fmt.Sprintf("Paste failed: %s no longer exists", strings.Join(missing, ", ")))
		return
	}
	if len(missing) > 0 {
		// Partial sets are reported rather than silently narrowed — the
		// count in the success flash would otherwise be the only clue.
		a.flash(fmt.Sprintf("Pasting %d items (%d no longer exist)…", len(plan), len(missing)))
	} else {
		a.flash("Pasting " + fileClipLabel(a.fileClipPaths) + "…")
	}
	a.runCopyPlan(plan, a.dirtyBufferOverlay(a.fileClipPaths), "")
}

// dirtyBufferOverlay snapshots, ON THE MAIN LOOP, the unsaved content of
// every open tab whose file is one of srcs or lives under one of them —
// the overlay copyTreeWith writes in place of the disk bytes. Nil when no
// such tab is dirty, which is the common case and costs nothing.
//
// Snapshotted rather than saved: a save would run format-on-save and the
// plugin hooks, and leave the original tab clean — side effects nobody
// asked a copy for. The original stays exactly as dirty as it was.
func (a *App) dirtyBufferOverlay(srcs []string) map[string][]byte {
	var out map[string][]byte
	for _, t := range a.tabs {
		if t == nil || !t.Dirty || t.Path == "" {
			continue
		}
		for _, s := range srcs {
			// tabPathRemoved is "is this tab at or under that path" —
			// the delete verbs' question, and exactly this one too.
			if tabPathRemoved(filepath.Clean(t.Path), filepath.Clean(s)) {
				if out == nil {
					out = map[string][]byte{}
				}
				out[filepath.Clean(t.Path)] = t.EncodedBytes()
				break
			}
		}
	}
	return out
}

// runCopyPlan copies plan off the main loop and posts ONE pasteDoneEvent
// when it ends — the copy engine shared by Paste and Copy to…. into is
// empty for a paste and the destination folder for Copy to…, carried
// through so handlePasteDone knows which receipt to flash.
//
// overlay is read by the goroutine only; the caller built it on the main
// loop and never touches it again, so no lock is needed.
func (a *App) runCopyPlan(plan []copyPlanItem, overlay map[string][]byte, into string) {
	scr := a.screen
	go func() {
		var err error
		done, buffered := 0, 0
		last, lastSrc := "", ""
		for _, p := range plan {
			n, cerr := copyTreeWith(p.src, p.dest, overlay)
			if cerr != nil {
				// Remove the partial copy so a failed paste can't leave
				// a half-populated folder wearing the destination's
				// name. Safe: uniquePastePath guaranteed dest didn't
				// exist before. Earlier items STAY — they are complete
				// copies, and unwinding them would destroy files the
				// user can see appearing in the tree.
				_ = os.RemoveAll(p.dest)
				err = cerr
				break
			}
			done++
			buffered += n
			last, lastSrc = p.dest, p.src
		}
		_ = scr.PostEvent(&pasteDoneEvent{
			when: time.Now(), dest: last, count: done, err: err,
			into: into, src: lastSrc, buffered: buffered,
		})
	}()
}

// handlePasteDone lands the goroutine's result on the main loop: flash
// the outcome and re-sync the workspace so the new file or folder
// appears in the tree and finder without waiting for the 10-second tick.
func (a *App) handlePasteDone(e *pasteDoneEvent) {
	if e == nil {
		return
	}
	if e.into != "" {
		// A Copy to… run: its receipt names the destination (copyto.go).
		a.handleCopyToDone(e)
		return
	}
	if e.err != nil {
		// A set that failed part-way still put files on disk, so the
		// workspace is re-synced either way — the tree must not show a
		// state the filesystem left behind.
		if e.count > 0 {
			a.workspaceChanged()
		}
		a.flash("Paste failed: " + e.err.Error())
		return
	}
	a.workspaceChanged()
	if e.count > 1 {
		a.flash(fmt.Sprintf("Pasted %d items", e.count) + bufferedNote(e.buffered))
		return
	}
	a.flash("Pasted " + filepath.Base(e.dest) + bufferedNote(e.buffered))
}

// -----------------------------------------------------------------------------
// Main menu actions.
// -----------------------------------------------------------------------------

// menuCopyFile arms the file clipboard with the active tab's file.
func (a *App) menuCopyFile() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	a.copyToFileClip(tab.Path)
}

// menuCopyFolder arms the file clipboard with the editor's active
// folder — the same target the Rename / Delete / Zip folder rows act
// on. The project root is gated out by hasActiveSubfolder: a root copy
// could never be pasted anywhere (every destination is inside it).
func (a *App) menuCopyFolder() {
	a.closeMenu()
	if !a.hasActiveSubfolder() {
		return
	}
	a.copyToFileClip(a.activeFolder)
}

// copyFolderLabel is the dynamic label hook for the Copy Folder menu
// row. Same shape as zipFolderLabel: bare label when no subfolder is
// active, "(subdir/)" suffix otherwise so the user sees the target
// before clicking.
func (a *App) copyFolderLabel() string {
	folder := a.activeFolder
	if folder == "" || folder == a.rootDir {
		return "Copy folder"
	}
	rel := a.relativeFolderLabel(folder)
	const maxLen = maxLabelSuffix
	suffix := " (" + rel + ")"
	if runeLen(suffix) > maxLen {
		keep := maxLen - len(" (…)")
		if keep < 4 {
			keep = 4
		}
		if keep < len(rel) {
			rel = "…" + rel[len(rel)-keep:]
		}
		suffix = " (" + rel + ")"
	}
	return "Copy folder" + suffix
}

// menuPasteItem pastes the file clipboard into the active folder (or
// the project root when none is active).
func (a *App) menuPasteItem() {
	a.closeMenu()
	a.startPaste(a.pasteTargetDir())
}

// pasteItemLabel is the dynamic label hook for the Paste row. Shows the
// armed source's basename ("Paste main.go") so the row is unambiguous
// next to the text-clipboard "Paste" row in the Clipboard group; falls
// back to the generic label while nothing is armed (the row is disabled
// then anyway, but it still renders dimmed).
func (a *App) pasteItemLabel() string {
	if len(a.fileClipPaths) == 0 {
		return "Paste file/folder"
	}
	if len(a.fileClipPaths) > 1 {
		return "Paste " + fileClipLabel(a.fileClipPaths)
	}
	base := filepath.Base(a.fileClipPaths[0])
	const maxLen = maxLabelSuffix
	if runeLen(base) > maxLen {
		// Keep the tail — the name and extension are the informative part.
		r := []rune(base)
		base = "…" + string(r[len(r)-maxLen+1:])
	}
	return "Paste " + base
}

// -----------------------------------------------------------------------------
// Tree context-menu actions.
// -----------------------------------------------------------------------------

// ctxCopy arms the file clipboard with the node the user right-clicked.
// The project root is gated out at openTreeContext, same as Rename /
// Delete — a root copy has nowhere legal to paste.
func ctxCopy(a *App, n *filetree.Node) {
	a.copyToFileClip(n.Path)
}

// ctxPaste pastes the file clipboard relative to the node the user
// right-clicked: into the node itself for folders (auto-expanding so
// the result is visible, same as ctxNewFile), or into the parent
// folder for files.
func ctxPaste(a *App, n *filetree.Node) {
	dest := n.Path
	if n.IsDir {
		if !n.Expanded {
			a.tree.Toggle(n)
		}
	} else {
		dest = filepath.Dir(n.Path)
	}
	a.startPaste(dest)
}

// -----------------------------------------------------------------------------
// Cmd+C / Cmd+V dispatch.
// -----------------------------------------------------------------------------

// cmdCopy is what Cmd+C means when the terminal delivers it: copy the
// text selection when there is one (the standard reading), otherwise
// arm the file clipboard with the active tab's file, falling back to
// the active folder for tab-less sessions. Copying is harmless either
// way — nothing happens until a paste — so the fallback chain can be
// generous without risking surprise mutations.
func (a *App) cmdCopy() {
	if a.hasSelection() {
		a.copySelection()
		return
	}
	if tab := a.activeTabPtr(); tab != nil && tab.Path != "" {
		a.copyToFileClip(tab.Path)
		return
	}
	if a.hasActiveSubfolder() {
		a.copyToFileClip(a.activeFolder)
		return
	}
	a.flash("Nothing to copy — select text or open a file")
}

// cmdPaste routes Cmd+V by whichever clipboard was armed most
// recently: a copied file/folder pastes into the active folder, text
// pastes at the cursor. pasteClipboard already handles the
// empty-clipboard case with its own hint flash.
func (a *App) cmdPaste() {
	if a.clipKind == clipFile {
		a.startPaste(a.pasteTargetDir())
		return
	}
	a.pasteClipboard()
}

// Compile-time check that pasteDoneEvent really is a tcell.Event.
var _ tcell.Event = (*pasteDoneEvent)(nil)
