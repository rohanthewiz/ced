// =============================================================================
// File: main.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Command ced (Cats Editor) is an opinionated, mouse-first terminal code
// editor.
// It is designed for the SSH-into-a-box workflow: a single static binary,
// drop it on the remote host, run it inside tmux/zellij, and you get a
// VS-Code-shaped UI (file tree, tabs, syntax highlighting, status bar) you
// can drive almost entirely with the mouse.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/app"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
	"github.com/rohanthewiz/ced/internal/version"
)

// cliAction is the high-level decision the arg parser hands back: edit
// (start the editor), version (print and exit), or help (print and exit).
// Pulling this out of main keeps the arg-resolution pure and testable
// without dragging in tcell.
type cliAction string

const (
	actionEdit    cliAction = "edit"
	actionVersion cliAction = "version"
	actionHelp    cliAction = "help"
)

// cliResult bundles everything resolveArgs hands back: which top-level
// action to run, where to root the editor, which file (if any) to open
// in the first tab, and any user-facing error to surface before exit.
type cliResult struct {
	Action   cliAction
	RootDir  string
	OpenFile string // empty when no file was named (or for non-edit actions)
	Last     bool   // --last: root at the most recently opened folder
	Err      error
}

// resolveArgs parses the editor's tiny CLI surface. The argument can be:
//
//   - a flag (--version / -v / --help / -h) → print-and-exit action
//   - --last / -l → reopen the most recently edited folder
//   - a directory path → use as the editor's root
//   - a file path → root at the file's parent dir, open the file in a tab
//   - a missing path → assume "ced foo.go" means "create foo.go" —
//     same intuition as `vim foo.go` on a non-existent file.
//
// Bare `ced` opens the CURRENT directory, and deliberately does not
// reopen the last folder: `cd myproj && ced` is the gesture this editor
// is launched with, and silently landing somewhere else would make that
// reflex a lie. What you get instead is the same folder's TABS back
// (session restore, see internal/app/folder.go), and --last for the
// times you really did mean "wherever I was".
//
// Pure function; no IO beyond os.Stat. Returns a result the caller acts
// on — keeps main() short and lets tests pin behavior without launching
// a real tcell screen.
func resolveArgs(args []string) cliResult {
	if len(args) == 0 {
		return cliResult{Action: actionEdit, RootDir: "."}
	}
	switch args[0] {
	case "--version", "-v", "-V", "version":
		return cliResult{Action: actionVersion}
	case "--help", "-h", "help":
		return cliResult{Action: actionHelp}
	case "--last", "-l", "last":
		return cliResult{Action: actionEdit, RootDir: ".", Last: true}
	}

	target := args[0]
	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		return cliResult{Action: actionEdit, RootDir: target}
	case err == nil:
		// Existing file — root at its parent so the file tree shows
		// useful context, then open the file as the first tab.
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return cliResult{Action: actionEdit, RootDir: dir, OpenFile: target}
	case os.IsNotExist(err):
		// Missing path — treat as a "new file" intent (same as vim does).
		// The Tab buffer starts empty and is written to disk on first save.
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return cliResult{Action: actionEdit, RootDir: dir, OpenFile: target}
	default:
		// Real IO error (permissions, EIO, etc.) — surface it instead of
		// silently swallowing it into a "directory not found" later.
		return cliResult{Err: err}
	}
}

// printHelp writes a short usage block to stdout. Kept brief on purpose:
// the editor is itself the help — once running, the ≡ menu lists every
// action.
func printHelp() {
	fmt.Println(`ced (Cats Editor) — opinionated mouse-first terminal code editor.

Usage:
  ced                     Open the current directory.
  ced <directory>         Open a project directory.
  ced <file>              Open a file (its parent becomes the project root).
  ced --last              Reopen the most recently edited folder.
  ced --version           Print the version and exit.
  ced --help              Print this help and exit.

Folders remember the tabs you had open; ≡ → File → Open folder switches
projects without leaving the editor.

Once running, click ≡ (top-left), right-click anywhere, or double-tap Esc
for the action menu. See https://github.com/rohanthewiz/ced for
hotkeys and the full feature list.`)
}

// main routes to the action resolveArgs picked. Edit is by far the
// common path; the print-and-exit branches stay tiny and side-effect
// free so a sanity script or CI check can call --version without
// initialising a tcell screen.
func main() {
	res := resolveArgs(os.Args[1:])
	if res.Err != nil {
		fmt.Fprintln(os.Stderr, "ced:", res.Err)
		os.Exit(1)
	}

	switch res.Action {
	case actionVersion:
		fmt.Println("ced", version.Version)
		return
	case actionHelp:
		printHelp()
		return
	}

	root := res.RootDir
	if res.Last {
		if last := lastFolder(); last != "" {
			root = last
		} else {
			fmt.Fprintln(os.Stderr, "ced: no recent folder recorded — opening the current directory")
		}
	}

	// The editor runs in a loop rather than once because "Open folder"
	// is implemented as a restart: the App parks the new root on
	// NextRoot and asks Run to return, and we build a fresh one. Every
	// subsystem derived from rootDir — tree, finder index, git, gopls's
	// rootUri, the ACP session cwd, MCP roots, plugin working dirs — is
	// then constructed by the same code that constructs it at startup,
	// instead of by a second re-derivation path nobody exercises. See
	// internal/app/folder.go.
	for openFile := res.OpenFile; ; openFile = "" {
		a, err := app.New(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ced: failed to start:", err)
			os.Exit(1)
		}
		if openFile != "" {
			a.OpenFile(openFile)
		}
		runErr := a.Run()
		next := a.NextRoot()
		// Close explicitly, not deferred: a deferred Close would only
		// fire when main returns, so a folder switch would leave the old
		// screen, its goroutines and its language servers alive
		// underneath the new App.
		a.Close()
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "ced:", runErr)
			os.Exit(1)
		}
		if next == "" {
			return
		}
		root = next
	}
}

// lastFolder resolves --last: the most recently opened folder from the
// workspace state file, or "" when there is none (first run, or an
// unreadable state file). Errors are swallowed on purpose — the caller
// falls back to the current directory, which is a better answer than
// refusing to start over a convenience file.
func lastFolder() string {
	st, err := session.Load(userconfig.StatePath())
	if err != nil {
		return ""
	}
	return st.Last()
}
