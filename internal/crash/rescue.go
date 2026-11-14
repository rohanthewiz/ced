// =============================================================================
// File: internal/crash/rescue.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Rescue folders: where unsaved buffers go when the editor cannot ask.
//
// A panic, a SIGTERM, or a SIGHUP from a killed tmux pane ends the
// editor with nobody to answer "save or discard?". Writing a dirty buffer
// over the user's real file in that moment is the wrong default — after
// a panic the buffer may be the very thing that is broken, and with
// auto-save off the user has said "never write behind my back" — so the
// bytes go to a rescue folder instead, one folder per rescue:
//
//	~/.config/ced/recover/
//	└── 20261113-142501-ced/          <stamp>-<project>
//	    ├── INDEX.txt                 why, when, and rescued ← original
//	    ├── internal/app/app.go       files under the root keep their
//	    ├── _outside/etc/hosts        root-relative path; others their
//	    └── untitled-1.txt            absolute one under _outside/
//
// Root-relative paths rather than basenames: two dirty main.go files are
// ordinary, and the layout lets `diff -r` against the project answer
// "what did I lose?" in one command. INDEX.txt names the originals so a
// rescue made by a hung-up pane (whose exit note nobody saw) still
// explains itself.
//
// The folder lives under the user's config directory, not in the
// repository: a crash must never add untracked files to someone's
// checkout, and one home is one place to look. Folders older than KeepFor
// are pruned when the NEXT rescue is written — nothing runs at startup.
//
// Everything here is plain file IO with no editor types, because it runs
// in the worst possible state: a panicking process, possibly off the main
// loop, about to exit.
package crash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KeepFor is how long a rescue folder is kept before a later rescue
// prunes it. Two weeks covers "it crashed on Friday, I looked on the
// Monday after next" without letting the directory grow forever.
const KeepFor = 14 * 24 * time.Hour

// stampLayout names each rescue folder. Sortable, second resolution, no
// characters any filesystem objects to. Prune parses it back, so it is
// also the marker that a directory entry is OURS to delete.
const stampLayout = "20060102-150405"

// IndexName is the file in every rescue folder that explains it.
const IndexName = "INDEX.txt"

// Buffer is one unsaved buffer to rescue.
type Buffer struct {
	// Path is the absolute path of the file the buffer belongs to, or ""
	// for an untitled tab.
	Path string
	// Data is what Save would have written: the file's own encoding,
	// line endings and BOM.
	Data []byte
	// Note, when set, is printed under the buffer's line in INDEX.txt —
	// for when Data is NOT what Save would have written (a Windows-1252
	// file holding a character the code page can't, rescued as UTF-8).
	Note string
}

// Rescued names one written file: where it came from and where it went.
type Rescued struct {
	Orig string // Buffer.Path ("" = untitled)
	File string // absolute path of the rescued copy
}

// Rescue writes bufs into a new folder under dir and returns the folder
// and what was written. root is the project root (it decides the
// relative layout and names the folder); reason is one line saying why
// the editor could not save normally.
//
// It never stops at the first failure: every buffer is attempted, and
// the error joins whatever went wrong. A partial rescue is still a
// rescue, and the caller reports what DID land.
func Rescue(dir, root, reason string, now time.Time, bufs []Buffer) (folder string, out []Rescued, err error) {
	if dir == "" {
		return "", nil, errors.New("no config directory to rescue into (set XDG_CONFIG_HOME or HOME)")
	}
	if len(bufs) == 0 {
		return "", nil, nil
	}
	// 0700: the files are the user's source, possibly secrets, and the
	// config directory is not always private (a shared dotfiles setup).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	// Prune before creating, so this rescue is never a candidate. Errors
	// are ignored: housekeeping must not cost the rescue itself.
	_ = Prune(dir, now, KeepFor)

	folder, err = newRescueFolder(dir, now, projectLabel(root))
	if err != nil {
		return "", nil, err
	}
	var errs []error
	untitled := 0
	var index strings.Builder
	for _, b := range bufs {
		rel := rescueRel(root, b.Path, &untitled)
		dest, werr := writeExclusive(folder, rel, b.Data)
		if werr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", origLabel(b.Path), werr))
			continue
		}
		out = append(out, Rescued{Orig: b.Path, File: dest})
		shown, _ := filepath.Rel(folder, dest)
		fmt.Fprintf(&index, "  %s  <-  %s\n", shown, origLabel(b.Path))
		if b.Note != "" {
			fmt.Fprintf(&index, "        note: %s\n", b.Note)
		}
	}
	// The index goes last so it lists only what actually landed.
	if len(out) > 0 {
		if ierr := os.WriteFile(filepath.Join(folder, IndexName), []byte(indexText(now, reason, root, len(out), index.String())), 0o600); ierr != nil {
			errs = append(errs, ierr)
		}
	}
	return folder, out, errors.Join(errs...)
}

// Prune removes rescue folders under dir older than maxAge. Only entries
// whose name parses as a rescue stamp are considered — anything else in
// the directory (a file the user parked there, a folder they renamed to
// keep) is left alone.
func Prune(dir string, now time.Time, maxAge time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || len(e.Name()) < len(stampLayout) {
			continue
		}
		at, perr := time.ParseInLocation(stampLayout, e.Name()[:len(stampLayout)], now.Location())
		if perr != nil {
			continue
		}
		if now.Sub(at) > maxAge {
			if rerr := os.RemoveAll(filepath.Join(dir, e.Name())); rerr != nil {
				errs = append(errs, rerr)
			}
		}
	}
	return errors.Join(errs...)
}

// newRescueFolder creates <dir>/<stamp>-<label>, adding -2, -3, … when
// that exists already: two instances of one project hung up by the same
// tmux kill rescue in the same second, and neither may write into the
// other's folder. Mkdir (not MkdirAll) is what makes the claim atomic.
func newRescueFolder(dir string, now time.Time, label string) (string, error) {
	base := now.Format(stampLayout) + "-" + label
	for n := 1; n < 100; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		p := filepath.Join(dir, name)
		err := os.Mkdir(p, 0o700)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not claim a rescue folder under %s", dir)
}

// rescueRel decides where inside the rescue folder a buffer goes:
// root-relative for files in the project, _outside/<absolute path> for
// files elsewhere (an LSP jump into GOROOT, an Open file… of /etc/hosts),
// untitled-N.txt for buffers that never had a name. untitled counts the
// untitled buffers seen so far.
func rescueRel(root, path string, untitled *int) string {
	if path == "" {
		*untitled++
		return fmt.Sprintf("untitled-%d.txt", *untitled)
	}
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return rel
		}
	}
	// Strip the volume/leading separator so Join keeps it INSIDE the
	// folder rather than treating it as absolute.
	clean := filepath.Clean(path)
	clean = strings.TrimPrefix(clean, filepath.VolumeName(clean))
	clean = strings.TrimLeft(clean, string(filepath.Separator))
	return filepath.Join("_outside", clean)
}

// writeExclusive writes data at folder/rel, creating parent folders, and
// never over an existing file: a clash (a project that really has an
// `_outside` folder, say) gets a numeric suffix instead. Returns the
// path actually written.
func writeExclusive(folder, rel string, data []byte) (string, error) {
	dest := filepath.Join(folder, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	for n := 1; n < 100; n++ {
		p := dest
		if n > 1 {
			p = fmt.Sprintf("%s.%d", dest, n)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		// Sync: the next thing this process does is exit, possibly on a
		// machine that is shutting down (SIGTERM from launchd/systemd).
		serr := f.Sync()
		cerr := f.Close()
		if err := errors.Join(werr, serr, cerr); err != nil {
			return "", err
		}
		return p, nil
	}
	return "", fmt.Errorf("could not claim a name for %s", rel)
}

// projectLabel is the folder-name-safe form of the project's basename.
// Anything outside a conservative set becomes '_' — the name is a hint
// for a human scanning the directory, the stamp is what makes it unique.
func projectLabel(root string) string {
	name := filepath.Base(root)
	if root == "" || name == "." || name == string(filepath.Separator) {
		return "ced"
	}
	b := []byte(name)
	for i, c := range b {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
		if !ok {
			b[i] = '_'
		}
	}
	return string(b)
}

// origLabel is how INDEX.txt spells a buffer's origin.
func origLabel(path string) string {
	if path == "" {
		return "(untitled)"
	}
	return path
}

// indexText is INDEX.txt's body: what happened, then one line per
// rescued buffer (already formatted by Rescue), then what to do next.
func indexText(now time.Time, reason, root string, n int, lines string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ced rescued %d unsaved buffer(s) into this folder.\n\n", n)
	fmt.Fprintf(&b, "When:    %s\n", now.Format("2006-01-02 15:04:05 -0700"))
	fmt.Fprintf(&b, "Why:     %s\n", reason)
	fmt.Fprintf(&b, "Project: %s\n\n", root)
	b.WriteString(lines)
	b.WriteString("\nEach file holds the buffer as Save would have written it. Compare it\n")
	b.WriteString("with the original before copying it back: the original may have\n")
	b.WriteString("changed since. This folder is removed when ced writes a rescue more\n")
	fmt.Fprintf(&b, "than %d days after this one.\n", int(KeepFor/(24*time.Hour)))
	return b.String()
}
