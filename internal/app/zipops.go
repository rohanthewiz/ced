// =============================================================================
// File: internal/app/zipops.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Zip-a-file / zip-a-folder. The archive is built with the stdlib's
// archive/zip — no shelling out to a `zip` binary (which macOS and
// minimal Linux boxes disagree about) and no CGO, keeping the
// single-static-binary promise.
//
// Surfaces, per the house rule that every file action must be
// reachable from the ≡ menu first:
//
//   • ≡ → "Zip file"            — the active tab's file
//   • ≡ → "Zip folder (sub/)"   — the active folder (project root allowed)
//   • tree right-click → "Zip"  — redundant shortcut for either kind
//
// The archive lands next to the source (<name>.zip). Zipping the
// project root would put a sibling zip *outside* the tree — invisible
// and confusing — so that one case writes inside the root instead,
// and the walk skips the in-progress archive so it never eats itself.
//
// Compression runs in a goroutine (a big folder can take seconds) and
// posts a zipDoneEvent back to the main loop — the same pattern as
// custom actions and formatters, so UI state stays main-loop-only.

package app

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// zipDoneEvent is posted by the zip goroutine when the archive is
// finished (or failed). Carries the destination so the success flash
// can name the file that appeared.
type zipDoneEvent struct {
	when time.Time
	dest string
	err  error
}

// When satisfies the tcell.Event interface.
func (e *zipDoneEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Backend: pure functions, no App state.
// -----------------------------------------------------------------------------

// zipDest returns where the archive for src should be written: a
// sibling <name>.zip in the common case. When src IS the project
// root, the sibling would land outside the tree where the user can't
// see (or maybe even write) it, so the archive goes inside the root
// instead — createZip skips it during the walk.
func zipDest(src, root string) string {
	if filepath.Clean(src) == filepath.Clean(root) {
		return filepath.Join(src, filepath.Base(src)+".zip")
	}
	return src + ".zip"
}

// createZip archives src (file or directory) into a new zip at dest.
// Directory entries are rooted at src's basename (zipping /proj/sub
// yields sub/…), which is what every desktop archiver produces and
// what users expect on extraction — bare top-level entries "explode"
// into the extraction directory.
//
// The destination is opened O_EXCL so an existing archive is never
// clobbered (same refusal contract as createEmptyFile / renameFile),
// and a partial archive is removed on failure so an interrupted zip
// can't leave a corrupt file that a later O_EXCL then refuses to
// replace.
func createZip(src, dest string) (err error) {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			// Best-effort cleanup — the error the user sees is the
			// one that broke the archive, not the unlink.
			_ = os.Remove(dest)
		}
	}()

	zw := zip.NewWriter(out)
	if err = addZipSource(zw, src, filepath.Base(src), dest, info); err != nil {
		// Close the writer so the file handle is released before the
		// deferred cleanup unlinks the partial archive.
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// createZipMulti archives every path in srcs into ONE archive at dest —
// the multi-selection's zip verb (app/treemarks.go). It is the same
// archive createZip writes, with one difference that matters:
//
// **Entry names are relative to base**, not each source's own basename.
// A set can hold two files with the same name in different folders
// (internal/app/main.go beside cmd/main.go); rooted at their basenames
// both would be stored as "main.go", and extraction would silently
// clobber one with the other. Rooted at the set's common parent they
// become "app/main.go" and "cmd/main.go" — which is also what a desktop
// archiver produces for a multi-file selection.
//
// The O_EXCL refusal, the partial-archive cleanup and the never-archive-
// the-archive skip are all createZip's, reached through the same helper.
func createZipMulti(srcs []string, base, dest string) (err error) {
	if len(srcs) == 0 {
		return os.ErrInvalid
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dest)
		}
	}()

	zw := zip.NewWriter(out)
	for _, src := range srcs {
		info, statErr := os.Lstat(src)
		if statErr != nil {
			// One vanished source costs the whole archive rather than
			// producing a quietly incomplete one: an archive that is
			// missing a file it was asked to hold is the one wrong
			// answer a backup can give (planWorkspaceEdit's rule).
			_ = zw.Close()
			return statErr
		}
		name := zipEntryName(src, base)
		if err = addZipSource(zw, src, name, dest, info); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

// zipEntryName is the archive-internal name for src: its path relative
// to base, forward-slashed per the zip spec. Falls back to the basename
// when src somehow sits outside base — a name is always better than an
// error here, since the alternative is refusing to archive a file the
// user pointed at.
func zipEntryName(src, base string) string {
	rel, err := filepath.Rel(base, src)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return filepath.Base(src)
	}
	return filepath.ToSlash(rel)
}

// addZipSource writes one source — a file or a whole directory tree —
// into an open archive under the entry name `name`. It is the shared
// spine of createZip and createZipMulti; a second copy of the walk would
// drift, and the two flavours would then disagree about symlinks, empty
// folders, or the dest-skip below.
//
// dest is passed only so the walk can refuse to archive the archive:
// when it lives inside src (project-root zips) the walk would otherwise
// find the half-written zip and recurse into reading it.
func addZipSource(zw *zip.Writer, src, name, dest string, info os.FileInfo) error {
	if !info.IsDir() {
		return writeZipEntry(zw, name, src, info)
	}
	destClean := filepath.Clean(dest)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filepath.Clean(path) == destClean {
			return nil
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		entry := name
		if rel != "." {
			// Zip entry names are always forward-slashed per spec,
			// regardless of the host OS separator.
			entry = name + "/" + filepath.ToSlash(rel)
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		return writeZipEntry(zw, entry, path, fi)
	})
}

// writeZipEntry appends one filesystem object to the archive under
// the given entry name. Directories become explicit "name/" entries
// so empty folders survive a round-trip; symlinks are stored as
// links (target path as content, link mode preserved) rather than
// followed — following could loop, or silently inline a file from
// outside the tree being zipped. Sockets/devices are skipped: they
// have no meaningful archive representation.
func writeZipEntry(zw *zip.Writer, name, path string, fi os.FileInfo) error {
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	hdr.Name = name

	switch {
	case fi.IsDir():
		hdr.Name += "/"
		_, err = zw.CreateHeader(hdr)
		return err
	case fi.Mode()&os.ModeSymlink != 0:
		target, readErr := os.Readlink(path)
		if readErr != nil {
			return readErr
		}
		hdr.Method = zip.Store
		w, createErr := zw.CreateHeader(hdr)
		if createErr != nil {
			return createErr
		}
		_, err = w.Write([]byte(target))
		return err
	case fi.Mode().IsRegular():
		hdr.Method = zip.Deflate
		w, createErr := zw.CreateHeader(hdr)
		if createErr != nil {
			return createErr
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	default:
		return nil
	}
}

// -----------------------------------------------------------------------------
// App glue: async run + main-loop completion.
// -----------------------------------------------------------------------------

// startZip validates the destination and kicks off the archive
// goroutine. The exists-check happens here on the main loop (not
// inside createZip's O_EXCL, which also guards it) so the user gets
// an immediate, specific refusal instead of an async failure flash.
func (a *App) startZip(src string) {
	root := a.rootDir
	if a.tree != nil && a.tree.Root != nil {
		root = a.tree.Root.Path
	}
	dest := zipDest(src, root)
	if _, err := os.Stat(dest); err == nil {
		a.flash(fmt.Sprintf("Zip failed: %s already exists", filepath.Base(dest)))
		return
	}
	a.flash("Zipping " + filepath.Base(src) + "…")
	scr := a.screen
	go func() {
		err := createZip(src, dest)
		_ = scr.PostEvent(&zipDoneEvent{when: time.Now(), dest: dest, err: err})
	}()
}

// startZipSet archives a whole multi-selection into one archive and is
// the tree marks' zip verb. A single-item set is routed to startZip
// instead, so ticking one file and zipping it produces exactly the
// sibling `<name>.zip` the context menu's Zip row always has — one
// gesture, one result, however the user got there.
//
// The archive lands INSIDE the set's common parent rather than beside it
// (zipDest's sibling rule): the parent may be the project root, whose
// sibling is outside the tree where the user can neither see nor
// necessarily write. Naming it after that parent is what makes a second
// run refuse loudly (O_EXCL) instead of silently producing
// selection-2.zip nobody asked for.
func (a *App) startZipSet(srcs []string) {
	if len(srcs) == 0 {
		a.flash("Nothing to zip")
		return
	}
	if len(srcs) == 1 {
		a.startZip(srcs[0])
		return
	}
	base := commonParentDir(srcs)
	if base == "" {
		a.flash("Zip failed: no common folder for the selection")
		return
	}
	dest := filepath.Join(base, filepath.Base(base)+"-selection.zip")
	if _, err := os.Stat(dest); err == nil {
		a.flash(fmt.Sprintf("Zip failed: %s already exists", filepath.Base(dest)))
		return
	}
	a.flash(fmt.Sprintf("Zipping %d items…", len(srcs)))
	scr := a.screen
	paths := append([]string(nil), srcs...)
	go func() {
		err := createZipMulti(paths, base, dest)
		_ = scr.PostEvent(&zipDoneEvent{when: time.Now(), dest: dest, err: err})
	}()
}

// commonParentDir returns the deepest directory containing every path in
// paths — the root a multi-source archive's entry names are measured
// against, and the folder the archive itself is written into.
//
// It works on cleaned path SEGMENTS rather than on the strings, because a
// common string prefix is not a common directory: /a/foo and /a/foobar
// share the prefix "/a/foo" and nothing below /a.
func commonParentDir(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	segs := strings.Split(filepath.Dir(filepath.Clean(paths[0])), string(filepath.Separator))
	for _, p := range paths[1:] {
		other := strings.Split(filepath.Dir(filepath.Clean(p)), string(filepath.Separator))
		if len(other) < len(segs) {
			segs = segs[:len(other)]
		}
		for i := range segs {
			if segs[i] != other[i] {
				segs = segs[:i]
				break
			}
		}
	}
	out := strings.Join(segs, string(filepath.Separator))
	if out == "" {
		// Every path was at the filesystem root — an absolute join
		// collapsed to nothing by the leading empty segment.
		return string(filepath.Separator)
	}
	return out
}

// handleZipDone lands the goroutine's result on the main loop: flash
// the outcome and re-sync the workspace so the new .zip appears in
// the tree and finder without waiting for the 10-second tick.
func (a *App) handleZipDone(e *zipDoneEvent) {
	if e == nil {
		return
	}
	if e.err != nil {
		a.flash("Zip failed: " + e.err.Error())
		return
	}
	a.workspaceChanged()
	a.flash("Created " + filepath.Base(e.dest))
}

// menuZipFile archives the active tab's file to a sibling .zip.
func (a *App) menuZipFile() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	a.startZip(tab.Path)
}

// menuZipFolder archives the editor's active folder — the same
// target the New File / Rename / Delete folder rows act on. Unlike
// Delete, the project root is allowed: zipping the whole project is
// a legitimate ask and the operation is read-only on the source.
func (a *App) menuZipFolder() {
	a.closeMenu()
	// Resolve through the tree root (always absolute) rather than
	// a.rootDir, which keeps the user's verbatim argument ("." in the
	// common case) — zipDest's root comparison needs clean absolutes.
	root := a.rootDir
	if a.tree != nil && a.tree.Root != nil {
		root = a.tree.Root.Path
	}
	folder := a.activeFolder
	if folder == "" {
		folder = root
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		// Active folder vanished externally — fall back to the root
		// rather than flashing a confusing async failure.
		folder = root
	}
	a.startZip(folder)
}

// zipFolderLabel is the dynamic label hook for the Zip Folder menu
// row. Same shape as deleteFolderLabel: bare "Zip folder" at the
// project root (where it means "zip the whole project"), a
// "(subdir/)" suffix otherwise so the user sees the target before
// clicking.
func (a *App) zipFolderLabel() string {
	folder := a.activeFolder
	if folder == "" || folder == a.rootDir {
		return "Zip folder"
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
	return "Zip folder" + suffix
}

// ctxZip archives the file or folder the user right-clicked in the
// tree. Redundant shortcut for the ≡ rows, per the house rule.
func ctxZip(a *App, n *filetree.Node) {
	a.startZip(n.Path)
}

// Compile-time check that zipDoneEvent really is a tcell.Event.
var _ tcell.Event = (*zipDoneEvent)(nil)
