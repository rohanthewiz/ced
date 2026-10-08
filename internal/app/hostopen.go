// =============================================================================
// File: internal/app/hostopen.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// "Open in Finder" — show a tree entry in the desktop's file manager, from
// the tree's right-click menu or the ≡ File row. Also home to the one
// host-exec seam the "open something outside ced" verbs share
// (openterminal.go runs tmux and user terminal commands through it).
//
// House rules:
//
//   - **A FILE IS REVEALED, A FOLDER IS OPENED.** On macOS `open -R` opens
//     the enclosing folder with the file SELECTED, which is what "show me
//     this in Finder" means; `open` on a file would launch its default
//     app instead — a different verb entirely. A folder is simply opened.
//     xdg-open has no select-the-file form, so on Linux a file opens its
//     parent folder: the nearest honest equivalent.
//
//   - **THE ROW NAMES THE TOOL** where it can: "Open in Finder" on macOS,
//     "Open file manager" elsewhere (Nautilus, Dolphin, Thunar… are all
//     behind xdg-open and ced cannot know which).
//
//   - **NO DESKTOP IS A REASON, NOT A GATE.** ced is built for SSH-into-
//     tmux, so a Linux box with no $DISPLAY / $WAYLAND_DISPLAY is a normal
//     place to be. The row is still offered (a row that vanishes over SSH
//     reads as a missing feature) and clicking it says why nothing can
//     open. macOS over SSH still runs `open` — it works, but the window
//     lands on the Mac's own screen, so the flash says so rather than
//     letting the user wonder where it went.
//
//   - **FIRE AND REPORT.** The command runs on a goroutine; success is the
//     window appearing, so only a FAILURE is posted back (as a notice
//     event — goroutines never touch App state).

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// hostEnv is the environment lookup for the host-integration verbs
// ($DISPLAY, $TMUX, $SSH_CONNECTION). A seam for the editorEnv reason:
// tests state an environment without t.Setenv leaking across helpers, and
// newTestApp pins it empty so a developer's tmux session cannot change
// which branch a test takes.
var hostEnv = os.Getenv

// hostGOOS is runtime.GOOS behind a seam, so the Linux and macOS spellings
// are both testable on whichever machine runs the suite.
var hostGOOS = runtime.GOOS

// hostRun runs argv to completion from dir and returns its combined output.
// The ONE place these verbs touch the machine, so newTestApp can pin it to
// a refusal. stdin is left nil (/dev/null): a child that reads the tty
// would fight tcell for the user's keystrokes.
//
// Setsid puts the child in its own session, off ced's controlling
// terminal. Without it a GUI terminal launched from here (`kitty
// --directory …`) sits in ced's process group, and closing the tmux pane
// ced runs in would SIGHUP the new window too — the opposite of "open a
// terminal over there". POSIX-only, like the rest of ced (grsh already
// rules out a Windows build).
var hostRun = func(dir string, argv []string) ([]byte, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.CombinedOutput()
}

// hostFailWindow is how long after the click a failure is still news.
// Some commands only return when their window closes (`alacritty
// --working-directory …` runs for as long as the terminal does), and a
// non-zero exit an hour later is not an answer to anything the user is
// waiting on — flashing it then would be a message from nowhere.
const hostFailWindow = 10 * time.Second

// hostRunAsync runs argv off the main loop and posts a notice only when it
// fails (promptly — see hostFailWindow). what names the attempt for that
// notice ("Open in Finder"). The first line of the command's own output
// rides along, because "exit status 1" alone never says what to fix and
// the tool's stderr usually does.
func (a *App) hostRunAsync(dir string, argv []string, what string) {
	// hostRun is read HERE, on the main loop, not inside the goroutine:
	// a test's cleanup restores the seam while an earlier verb's goroutine
	// may still be starting, and a read there would race that write.
	scr, run := a.screen, hostRun
	go func() {
		start := time.Now()
		out, err := run(dir, argv)
		if err == nil || time.Since(start) > hostFailWindow {
			return
		}
		msg := what + " failed: " + err.Error()
		if t := strings.TrimSpace(string(out)); t != "" {
			msg += " — " + firstLineOf(t)
		}
		catsPostNotice(scr, msg)
	}()
}

// fileManagerLabel is the row's label on this platform (see the header).
// Both spellings fit the tree popup, whose label room is fixed-width.
func fileManagerLabel() string {
	if hostGOOS == "darwin" {
		return "Open in Finder"
	}
	return "Open file manager"
}

// fileManagerArgv composes the command that shows path in the file
// manager, or returns a reason it cannot run here. Pure (environment comes
// in through hostEnv/hostGOOS), so both platforms' shapes are testable.
func fileManagerArgv(path string, isDir bool) (argv []string, reason string) {
	switch hostGOOS {
	case "darwin":
		if isDir {
			return []string{"open", path}, ""
		}
		// -R: reveal — open the parent with the file selected.
		return []string{"open", "-R", path}, ""
	}
	if hostEnv("DISPLAY") == "" && hostEnv("WAYLAND_DISPLAY") == "" {
		return nil, "No desktop session here ($DISPLAY is unset) — a file manager has nowhere to open"
	}
	if !isDir {
		path = filepath.Dir(path)
	}
	return []string{"xdg-open", path}, ""
}

// menuRevealInFileManager is the ≡ File row: the active file, else the
// project root — openInEditorTarget's rule, and for the same reason: "show
// me the project in Finder" is a real request with no file open.
func (a *App) menuRevealInFileManager() {
	a.closeMenu()
	a.revealInFileManager(a.openInEditorTarget())
}

// ctxRevealInFileManager is the tree's right-click row: the clicked node,
// file or folder, root included.
func ctxRevealInFileManager(a *App, n *filetree.Node) {
	a.closeModal()
	a.revealInFileManager(n.Path)
}

// revealInFileManager shows path in the desktop file manager. The node's
// kind is re-read from disk rather than trusted from the tree, so a path
// replaced since the last refresh still takes the right branch — and a
// path that is gone refuses instead of handing `open` a dead name.
func (a *App) revealInFileManager(path string) {
	if path == "" {
		a.flash("Nothing to open")
		return
	}
	abs := absolutePathFor(path)
	info, err := os.Stat(abs)
	if err != nil {
		a.flash(filepath.Base(abs) + " no longer exists")
		return
	}
	argv, reason := fileManagerArgv(abs, info.IsDir())
	if reason != "" {
		a.flash(reason)
		return
	}
	label := fileManagerLabel()
	a.hostRunAsync(filepath.Dir(abs), argv, label)
	switch {
	case hostGOOS == "darwin" && hostEnv("SSH_CONNECTION") != "":
		a.flash("Opened " + filepath.Base(abs) + " in Finder on this Mac's own screen (you're over SSH)")
	case hostGOOS == "darwin":
		a.flash("Revealed " + filepath.Base(abs) + " in Finder")
	default:
		a.flash("Opened " + filepath.Base(argv[len(argv)-1]) + " in the file manager")
	}
}
