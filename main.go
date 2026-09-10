// =============================================================================
// File: main.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// Portions copyright 2026 Rohan Allison.
// =============================================================================

// Command ced (Cats Editor) is an opinionated, mouse-first terminal code
// editor.
// It is designed for the SSH-into-a-box workflow: a single static binary,
// drop it on the remote host, run it inside tmux/zellij, and you get a
// VS-Code-shaped UI (file tree, tabs, syntax highlighting, status bar) you
// can drive almost entirely with the mouse.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"

	"github.com/rohanthewiz/ced/internal/app"
	"github.com/rohanthewiz/ced/internal/favorites"
	"github.com/rohanthewiz/ced/internal/remote"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
	"github.com/rohanthewiz/ced/internal/version"
)

// cliAction is the high-level decision the arg parser hands back: edit
// (start the editor), version (print and exit), or done (the command
// line was fully served by the parser itself — help, or one of the
// `fav` management verbs — and there is nothing left for main to do).
//
// Pulling this out of the parser keeps arg resolution reportable and
// testable without dragging in tcell: a test runs the whole real command
// line through parseArgs and reads back what the editor WOULD have done.
type cliAction string

const (
	actionEdit    cliAction = "edit"
	actionVersion cliAction = "version"
	// actionDone is the ZERO-ish value on purpose. urfave/cli serves
	// --help and `help` itself, without ever calling an Action, so the
	// result has to default to "handled, nothing more to do" — anything
	// else would make a help request fall through into launching an
	// editor.
	actionDone cliAction = "done"
)

// cliResult bundles everything the parser hands back: which top-level
// action to run, where to root the editor, which file (if any) to open
// in the first tab, which path (if any) to reveal in the file tree once
// it's up, and any user-facing error to surface before exit.
type cliResult struct {
	Action   cliAction
	RootDir  string
	OpenFile string // empty when no file was named (or for non-edit actions)
	Reveal   string // absolute path to reveal in the tree (`ced fav <name>`)
	Last     bool   // --last: root at the most recently opened folder
	Remote   bool   // --remote: hand the file to a running instance if there is one
	Wait     bool   // --wait: don't return until the editor is done with the file
	Err      error
}

// helpText is the whole `ced --help` output, and it is installed as
// urfave/cli's app help TEMPLATE rather than printed by a function of
// our own. That is what keeps one help surface: `ced --help`, `ced help`
// and a usage error all reach the same words, instead of a hand-rolled
// printer that drifts from what the parser actually accepts. It contains
// no `{{` actions, so the template engine prints it verbatim.
//
// Kept brief on purpose: the editor is itself the help — once running,
// the ≡ menu lists every action.
const helpText = `ced (Cats Editor) — opinionated mouse-first terminal code editor.

Usage:
  ced                     Open the current directory.
  ced <directory>         Open a project directory.
  ced <file>              Open a file (its parent becomes the project root).
  ced --last              Reopen the most recently edited folder.
  ced --root <dir> <file> Open the file with <dir> as the project root,
                          instead of the file's own parent directory.
  ced --remote <file>     Open the file in a ced already running on that
                          project, then exit. Starts a normal editor when
                          nothing is running.
  ced --wait <file>       The same handoff, but block until the editor is
                          done with the file. Use it as $EDITOR:
                            git config --global core.editor "ced --wait"
  ced --version           Print the version and exit.
  ced --help              Print this help and exit.

Favorites — named, project-relative locations:
  ced fav <name>          Open the project and reveal that location in the
                          file tree. The name is resolved by walking up
                          from the current directory, so it works from
                          anywhere inside the project.
  ced fav                 List the favorites visible from here.
  ced fav add plans ai_docs/plans      Bind a name (all projects).
  ced fav add --project plans docs/plans   Bind it for this project only.
  ced fav rm <name>       Unbind a name.
  ced fav path <name>     Print the resolved path, for shell use:
                            cd "$(ced fav path plans)"

Folders remember the tabs you had open; ≡ → File → Open folder switches
projects without leaving the editor.

Once running, click ≡ (top-left), right-click anywhere, or double-tap Esc
for the action menu. See https://github.com/rohanthewiz/ced for
hotkeys and the full feature list.
`

// favSubcommands are the words `ced fav` reserves for its own management
// verbs. They are listed here rather than only being declared as
// Commands because `fav add` has to REFUSE binding a favorite to one of
// them: urfave resolves a subcommand before falling through to the
// open-a-favorite action, so a name like "list" would be written to the
// file happily and then be permanently unreachable. Refusing at write
// time is the only moment the user can still pick another word.
var favSubcommands = []string{"add", "rm", "remove", "list", "ls", "path", "help", "h"}

// resolveTarget turns a single path argument into a (root, file) pair —
// the one piece of the CLI that is genuinely about the filesystem rather
// than about flags, which is why it stayed a plain function when the
// rest moved onto urfave/cli.
//
//   - a directory  → use as the editor's root, no file opened
//   - a file       → root at its parent dir, open the file in a tab
//   - a missing path → assume "ced foo.go" means "create foo.go" — the
//     same intuition as `vim foo.go` on a non-existent file.
//
// A real IO error (permissions, EIO) is surfaced rather than swallowed
// into a "directory not found" several seconds later.
func resolveTarget(target string) (root, openFile string, err error) {
	info, statErr := os.Stat(target)
	switch {
	case statErr == nil && info.IsDir():
		return target, "", nil
	case statErr == nil, os.IsNotExist(statErr):
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return dir, target, nil
	default:
		return "", "", statErr
	}
}

// newCLI builds the command-line surface. It writes into out, and fills
// *res with what the caller should do next rather than acting itself —
// that split is what lets a test drive the real parser (real flags, real
// subcommand resolution, real error messages) without a tcell screen or
// an os.Exit in the middle of it.
//
// Bare `ced` opens the CURRENT directory, and deliberately does not
// reopen the last folder: `cd myproj && ced` is the gesture this editor
// is launched with, and silently landing somewhere else would make that
// reflex a lie. What you get instead is the same folder's TABS back
// (session restore, see internal/app/folder.go), and --last for the
// times you really did mean "wherever I was".
func newCLI(out io.Writer, res *cliResult) *cli.App {
	globals := []cli.Flag{
		&cli.BoolFlag{Name: "last", Aliases: []string{"l"}, Usage: "root at the most recently opened folder"},
		&cli.StringFlag{Name: "root", Usage: "project root, overriding the file's own parent directory"},
		&cli.BoolFlag{Name: "remote", Aliases: []string{"r"}, Usage: "hand the file to a ced already running on that project"},
		&cli.BoolFlag{Name: "wait", Aliases: []string{"w"}, Usage: "block until the editor is done with the file ($EDITOR)"},
		// nvim's spelling of the pair; accepting it costs one flag and
		// saves anyone porting an $EDITOR line.
		&cli.BoolFlag{Name: "remote-wait", Usage: "--remote and --wait together"},
		&cli.BoolFlag{Name: "version", Aliases: []string{"v", "V"}, Usage: "print the version and exit"},
	}

	a := &cli.App{
		Name:                   "ced",
		Usage:                  "opinionated mouse-first terminal code editor",
		Version:                version.Version,
		Writer:                 out,
		ErrWriter:              out,
		HideVersion:            true, // replaced by the --version flag above
		CustomAppHelpTemplate:  helpText,
		UseShortOptionHandling: true,
		Flags:                  globals,
		// Usage errors print the message and nothing else. urfave's
		// default dumps the whole help block after it, which buries the
		// one line that says what was actually wrong.
		OnUsageError: func(_ *cli.Context, err error, _ bool) error { return err },
		// A returned error must never reach urfave's exit handling: this
		// process decides its own exit code in main, and a test driving
		// the parser must not be able to kill the test binary.
		ExitErrHandler: func(*cli.Context, error) {},
		Action:         func(c *cli.Context) error { return runEdit(c, res) },
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "print the version and exit",
				Action: func(*cli.Context) error {
					res.Action = actionVersion
					return nil
				},
			},
			{
				Name:  "last",
				Usage: "reopen the most recently edited folder",
				Action: func(c *cli.Context) error {
					res.Action, res.RootDir, res.Last = actionEdit, ".", true
					return nil
				},
			},
			favCommand(out, res),
		},
	}
	return a
}

// runEdit is the default action: everything that isn't a subcommand.
// Order matters — the flags that print and exit are answered before any
// path is stat'ed, so `ced --version` in a directory that no longer
// exists still prints a version.
func runEdit(c *cli.Context, res *cliResult) error {
	if c.Bool("version") {
		res.Action = actionVersion
		return nil
	}

	remoteFlag := c.Bool("remote") || c.Bool("remote-wait")
	waitFlag := c.Bool("wait") || c.Bool("remote-wait")

	root, openFile := ".", ""
	if target := c.Args().First(); target != "" {
		var err error
		root, openFile, err = resolveTarget(target)
		if err != nil {
			return err
		}
	}

	// --root answers a question a file path cannot: `ced internal/app/x.go`
	// roots at internal/app, which is the right guess for a human typing
	// it and the wrong one for a program reproducing a workspace. The
	// cats split (internal/app/catssplit.go) spawns a sibling editor on
	// the file you are looking at, and that sibling has to open the
	// project you are IN, not the directory the file happens to sit in.
	if override := c.String("root"); override != "" {
		// Checked rather than trusted: a bad --root would otherwise
		// surface as a file tree rooted in a directory that isn't there,
		// several seconds after the mistake.
		info, err := os.Stat(override)
		switch {
		case err != nil:
			return fmt.Errorf("--root %s: %w", override, err)
		case !info.IsDir():
			return fmt.Errorf("--root %s is not a directory", override)
		}
		root = override
	}

	// --remote and --wait COMPOSE rather than being two modes: --remote
	// asks a running instance to open the file, --wait says don't return
	// until the editor is finished with it. `EDITOR="ced --wait"` is the
	// pairing that matters — a bare --wait still hands off when an
	// instance is there, and blocks the way any terminal editor does when
	// it isn't, so git commit behaves identically either way.
	if (remoteFlag || waitFlag) && openFile == "" {
		if c.Args().Len() == 0 {
			return errors.New("--remote / --wait need a file to open")
		}
		// A directory can be opened, but there is nothing to wait on and
		// nothing a running instance could usefully be handed.
		return errors.New("--remote / --wait need a file, not a directory")
	}

	res.Action = actionEdit
	res.RootDir = root
	res.OpenFile = openFile
	res.Remote, res.Wait = remoteFlag, waitFlag
	res.Last = c.Bool("last")
	return nil
}

// -----------------------------------------------------------------------------
// ced fav — named, project-relative locations
// -----------------------------------------------------------------------------
//
// `ced fav plans` opens the project and reveals ai_docs/plans in the file
// tree. It deliberately does NOT re-root the editor at that folder —
// `ced ai_docs/plans` already does that, and pays for it by throwing the
// project away (git status, gopls's rootUri, the finder index and every
// plugin's working directory are all derived from the root). The verb
// this adds is the one the plain path spelling cannot express: keep the
// project, move the view. See internal/app/favorites.go.

// favCommand builds the `fav` subtree. The bare form takes a NAME, so
// the management verbs are subcommands and any other first argument
// falls through to favOpen — which is exactly how urfave resolves a
// command that has both Subcommands and an Action of its own.
func favCommand(out io.Writer, res *cliResult) *cli.Command {
	scopeFlags := []cli.Flag{
		&cli.BoolFlag{Name: "project", Aliases: []string{"p"}, Usage: "scope to this project only"},
	}
	return &cli.Command{
		Name:      "fav",
		Usage:     "open a named, project-relative location",
		ArgsUsage: "[name] [dir]",
		Description: "With a name, opens the project owning that favorite and reveals it in\n" +
			"the file tree. With no name, lists the favorites visible from here.\n" +
			"Names resolve by walking up from the current directory (or [dir]), so\n" +
			"they work from anywhere inside a project.",
		Action: func(c *cli.Context) error { return favOpen(c, out, res) },
		Subcommands: []*cli.Command{
			{
				Name:      "add",
				Usage:     "bind a name to a project-relative path",
				ArgsUsage: "<name> <relative-path> [dir]",
				Flags:     scopeFlags,
				Action:    func(c *cli.Context) error { return favAdd(c, out) },
			},
			{
				Name:      "rm",
				Aliases:   []string{"remove"},
				Usage:     "unbind a name",
				ArgsUsage: "<name> [dir]",
				Flags: append([]cli.Flag{
					&cli.BoolFlag{Name: "global", Aliases: []string{"g"}, Usage: "remove the global entry, not this project's override"},
				}, scopeFlags...),
				Action: func(c *cli.Context) error { return favRemove(c, out) },
			},
			{
				Name:      "list",
				Aliases:   []string{"ls"},
				Usage:     "list the favorites visible from here",
				ArgsUsage: "[dir]",
				Action:    func(c *cli.Context) error { return favList(c, out) },
			},
			{
				Name:      "path",
				Usage:     "print a favorite's resolved absolute path",
				ArgsUsage: "<name> [dir]",
				Action:    func(c *cli.Context) error { return favPath(c, out) },
			},
		},
	}
}

// loadFavorites reads favorites.json. A missing file is not an error
// (most users have none), but an unreadable or malformed one is: a file
// somebody wrote and ced silently ignored is worse than a message,
// because they would go on believing the name was bound.
func loadFavorites() (*favorites.Set, string, error) {
	path := userconfig.FavoritesPath()
	if path == "" {
		return nil, "", errors.New("no config location — set XDG_CONFIG_HOME or HOME")
	}
	set, err := favorites.Load(path)
	if err != nil {
		return nil, path, err
	}
	return set, path, nil
}

// favStartDir picks the directory a favorite is resolved from: the
// optional positional argument, else the global --root, else the working
// directory. One ladder, in specificity order, so the two spellings can
// never disagree about which project is being asked.
func favStartDir(c *cli.Context, argIndex int) string {
	if dir := c.Args().Get(argIndex); dir != "" {
		return dir
	}
	if root := c.String("root"); root != "" {
		return root
	}
	return "."
}

// favOpen is the bare `ced fav <name>` — the reason the feature exists.
// With no name it lists instead of erroring: a user who has forgotten
// the word is better served by the list than by usage text.
func favOpen(c *cli.Context, out io.Writer, res *cliResult) error {
	name := c.Args().First()
	if name == "" {
		return favList(c, out)
	}
	// The bare form's first argument is a NAME, so a path handed to it
	// would otherwise fail as "no favorite named /home/me/proj" — an
	// error about the wrong thing entirely. CleanName's message says
	// what the argument is, and the hint says where a directory goes.
	if _, err := favorites.CleanName(name); err != nil {
		return fmt.Errorf("%w (to list a project's favorites: ced fav list <dir>)", err)
	}
	set, _, err := loadFavorites()
	if err != nil {
		return err
	}
	start := favStartDir(c, 1)
	found, err := set.Resolve(start, name)
	if err != nil {
		return favResolveError(set, start, name, err)
	}
	res.Action = actionEdit
	res.RootDir = found.Root
	res.Reveal = found.Abs
	return nil
}

// favResolveError turns a resolution failure into something actionable.
// An unbound name is answered with the names that ARE bound — the user
// mistyped or forgot, and a bare "no such favorite" makes them go and
// read a config file to find out. Everything else (a bound name whose
// directory is missing, an entry that climbs out of the project) already
// carries its own specifics from internal/favorites.
func favResolveError(set *favorites.Set, start, name string, err error) error {
	if !errors.Is(err, favorites.ErrNotFound) {
		return err
	}
	known := set.List(projectRootFor(start))
	if len(known) == 0 {
		return fmt.Errorf("no favorite named %q — add one with: ced fav add %s <relative-path>", name, name)
	}
	names := make([]string, 0, len(known))
	for _, e := range known {
		names = append(names, e.Name)
	}
	return fmt.Errorf("no favorite named %q — known: %s", name, strings.Join(names, ", "))
}

// favList prints every favorite visible from the given directory, with
// the ones that don't resolve here marked. Marking rather than hiding is
// the point: a global default that this project doesn't follow is still
// worth seeing, and "missing" is the answer to the question the user is
// about to ask.
func favList(c *cli.Context, out io.Writer) error {
	set, path, err := loadFavorites()
	if err != nil {
		return err
	}
	start := favStartDir(c, 0)
	root := projectRootFor(start)
	entries := set.List(root)
	if len(entries) == 0 {
		fmt.Fprintf(out, "No favorites yet (%s).\nAdd one with: ced fav add plans ai_docs/plans\n", path)
		return nil
	}

	nameW, pathW := 0, 0
	for _, e := range entries {
		if n := len(e.Name); n > nameW {
			nameW = n
		}
		if n := len(e.Path); n > pathW {
			pathW = n
		}
	}
	fmt.Fprintf(out, "Favorites from %s:\n", root)
	for _, e := range entries {
		note := string(e.Scope)
		if _, rerr := set.Resolve(start, e.Name); rerr != nil {
			note += ", missing here"
		}
		fmt.Fprintf(out, "  %-*s  %-*s  (%s)\n", nameW, e.Name, pathW, e.Path, note)
	}
	return nil
}

// favPath prints the resolved absolute path and nothing else, so it
// composes: `cd "$(ced fav path plans)"`. It is the one fav verb whose
// output is meant for another program, which is why it carries no label.
func favPath(c *cli.Context, out io.Writer) error {
	name := c.Args().First()
	if name == "" {
		return errors.New("ced fav path needs a favorite's name")
	}
	set, _, err := loadFavorites()
	if err != nil {
		return err
	}
	start := favStartDir(c, 1)
	found, err := set.Resolve(start, name)
	if err != nil {
		return favResolveError(set, start, name, err)
	}
	fmt.Fprintln(out, found.Abs)
	return nil
}

// favAdd writes a binding back to favorites.json. Global by default,
// because a favorite earns its name by repeating across projects;
// --project is for the repo that spells the same idea differently.
//
// The path is NOT required to exist. A favorite is a convention, and
// binding one before creating the directory (or on a machine where that
// checkout isn't cloned yet) is ordinary — Resolve reports a missing
// directory clearly when the name is used, which is the moment it
// matters.
func favAdd(c *cli.Context, out io.Writer) error {
	name, rel := c.Args().Get(0), c.Args().Get(1)
	if name == "" || rel == "" {
		return errors.New("usage: ced fav add [--project] <name> <relative-path>")
	}
	for _, reserved := range favSubcommands {
		if name == reserved {
			return fmt.Errorf("%q is a `ced fav` subcommand — pick another name, or it could never be opened", name)
		}
	}
	set, path, err := loadFavorites()
	if err != nil {
		return err
	}

	root := ""
	if c.Bool("project") {
		root = projectRootFor(favStartDir(c, 2))
	}
	entry, err := set.Add(root, name, rel)
	if err != nil {
		return err
	}
	if err := set.Save(path); err != nil {
		return err
	}
	if entry.Scope == favorites.ScopeProject {
		fmt.Fprintf(out, "Added %s → %s (project: %s)\n", entry.Name, entry.Path, root)
	} else {
		fmt.Fprintf(out, "Added %s → %s (all projects)\n", entry.Name, entry.Path)
	}
	return nil
}

// favRemove unbinds a name. With no scope flag it removes whichever
// entry the user would actually have hit — the project override first,
// then the global default — and SAYS which, because "rm plans" quietly
// deleting a default shared by every project when the user meant this
// repo's override is a loss with nothing on screen to explain it.
func favRemove(c *cli.Context, out io.Writer) error {
	name := c.Args().First()
	if name == "" {
		return errors.New("usage: ced fav rm [--project|--global] <name>")
	}
	set, path, err := loadFavorites()
	if err != nil {
		return err
	}

	scope := favorites.Scope("")
	switch {
	case c.Bool("project"):
		scope = favorites.ScopeProject
	case c.Bool("global"):
		scope = favorites.ScopeGlobal
	}
	root := projectRootFor(favStartDir(c, 1))
	removed, err := set.Remove(root, scope, name)
	if err != nil {
		return fmt.Errorf("no favorite named %q to remove", name)
	}
	if err := set.Save(path); err != nil {
		return err
	}
	fmt.Fprintf(out, "Removed %s (%s)\n", name, removed)

	// A removed override usually uncovers a global default rather than
	// unbinding the name, and a user who doesn't expect that will think
	// the removal failed.
	if removed == favorites.ScopeProject {
		if rel, _, ok := set.Lookup(root, name); ok {
			fmt.Fprintf(out, "  %s now falls back to the global %s\n", name, rel)
		}
	}
	return nil
}

// projectRootFor answers "which project is this directory in?" for the
// scope of a WRITE, where there is no favorite to resolve and therefore
// nothing to walk up towards. It looks for the nearest ancestor holding
// a .git — the same marker every other tool in this workflow uses — and
// falls back to the directory itself when there is none, since a project
// without a repository is still a project.
//
// Deliberately NOT used for reads: `ced fav plans` walks up until the
// favorite RESOLVES, which is a better answer (it works in a checkout
// with no .git, and it picks the level that actually owns the folder).
func projectRootFor(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for cur := abs; ; {
		if info, err := os.Stat(filepath.Join(cur, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		cur = parent
	}
}

// -----------------------------------------------------------------------------

// parseArgs runs the real command line through the real parser and
// reports what should happen next. Everything reachable from a ced
// invocation that isn't the editor itself — help, version, and the whole
// `fav` management surface — has already happened by the time this
// returns; what comes back is only ever "start an editor like this", or
// an error to print.
func parseArgs(args []string, out io.Writer) cliResult {
	res := cliResult{Action: actionDone}
	a := newCLI(out, &res)
	if err := a.Run(append([]string{"ced"}, args...)); err != nil {
		res.Err = err
	}
	return res
}

// main routes to the action the parser picked. Edit is by far the common
// path; the print-and-exit branches stay tiny and side-effect free so a
// sanity script or CI check can call --version without initialising a
// tcell screen.
func main() {
	res := parseArgs(os.Args[1:], os.Stdout)
	if res.Err != nil {
		fmt.Fprintln(os.Stderr, "ced:", res.Err)
		os.Exit(1)
	}

	switch res.Action {
	case actionVersion:
		fmt.Println("ced", version.Version)
		return
	case actionDone:
		return
	}

	// The handoff is tried BEFORE any screen is initialised: if a running
	// instance takes the file, this process never becomes an editor at
	// all, it just waits (or exits). remote.ErrNoInstance is the ordinary
	// case, not a failure — nothing is listening for this project, so we
	// fall through and start a normal editor below.
	if res.Remote || res.Wait {
		switch err := remote.Open(res.OpenFile, res.Wait); {
		case err == nil:
			return
		case errors.Is(err, remote.ErrNoInstance):
			// fall through to a local editor
		default:
			fmt.Fprintln(os.Stderr, "ced:", err)
			os.Exit(1)
		}
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
	//
	// The reveal rides the same one-shot seam as the file argument: both
	// describe how this INVOCATION started, not a property of the
	// workspace, so a folder switch must not repeat either of them.
	for openFile, reveal := res.OpenFile, res.Reveal; ; openFile, reveal = "", "" {
		a, err := app.New(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ced: failed to start:", err)
			os.Exit(1)
		}
		if openFile != "" {
			a.OpenFile(openFile)
		}
		if reveal != "" {
			a.RevealPath(reveal)
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
