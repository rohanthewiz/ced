// =============================================================================
// File: main_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/favorites"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// resolveArgs runs a command line through the REAL parser and throws its
// output away. It exists so the arg-resolution tests below read as
// statements about the command line rather than about urfave/cli's
// plumbing — and, more importantly, so they exercise the actual flag
// definitions, the actual subcommand resolution and the actual error
// strings. A hand-rolled second parser here would test itself.
func resolveArgs(args []string) cliResult { return parseArgs(args, io.Discard) }

// runCLI is resolveArgs plus the output, for the verbs whose whole
// product IS what they printed (fav list, fav path).
func runCLI(t *testing.T, args ...string) (cliResult, string) {
	t.Helper()
	var buf bytes.Buffer
	res := parseArgs(args, &buf)
	return res, buf.String()
}

// TestResolveArgs_NoArgsRootsCurrentDir keeps the no-arg path simple:
// "." as rootDir, no file to open, action = edit.
func TestResolveArgs_NoArgsRootsCurrentDir(t *testing.T) {
	got := resolveArgs(nil)
	if got.Action != actionEdit {
		t.Fatalf("action: got %q, want edit", got.Action)
	}
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_DirectoryArgUsesAsRoot pins the existing behaviour:
// passing a directory uses it as the editor's root.
func TestResolveArgs_DirectoryArgUsesAsRoot(t *testing.T) {
	dir := t.TempDir()
	got := resolveArgs([]string{dir})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_FileArgRootsParent is the regression test for the
// "ced main.go" bug: a file argument should root the editor at
// the file's parent and seed an OpenFile so the user's tab is ready.
func TestResolveArgs_FileArgRootsParent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{target})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_BarefilenameRootsCwd covers the common "ced
// foo.go" form where the path has no directory component. The
// filepath.Dir of "foo.go" is "." — without the empty-string guard
// we'd hand the editor an empty rootDir and filetree.New would fail.
func TestResolveArgs_BarefilenameRootsCwd(t *testing.T) {
	// Use a real bare filename in a temp cwd so the stat path covers
	// the existing-file branch.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.WriteFile("bare.txt", []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{"bare.txt"})
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "bare.txt" {
		t.Fatalf("OpenFile: got %q, want bare.txt", got.OpenFile)
	}
}

// TestResolveArgs_MissingFileTreatsAsNew mirrors `vim foo.go` on a
// non-existent path: open the editor at the parent dir with the file
// queued for editing — first save creates it.
func TestResolveArgs_MissingFileTreatsAsNew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.go")

	got := resolveArgs([]string{target})
	if got.Err != nil {
		t.Fatalf("missing file should not be an error, got %v", got.Err)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_VersionFlag covers every flavour of --version we
// accept. Failing here would mean a user typing `--version` lands in
// the editor instead of seeing a printed version.
func TestResolveArgs_VersionFlag(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "-V", "version"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionVersion {
			t.Errorf("flag %q: action = %q, want version", flag, got.Action)
		}
	}
}

// TestResolveArgs_HelpFlag is the equivalent for --help. Like version,
// the multi-spelling list keeps the CLI forgiving — and all three reach
// the SAME words, because helpText is installed as the parser's own help
// template rather than printed by a function beside it. A second printer
// is exactly how help drifts from what the parser accepts.
//
// The action is actionDone rather than a help action: the parser has
// already printed by the time it returns, so there is nothing left for
// main to do. That default is load-bearing — if it were actionEdit, a
// user asking for help would get an editor.
func TestResolveArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		got, out := runCLI(t, flag)
		if got.Action != actionDone {
			t.Errorf("flag %q: action = %q, want done", flag, got.Action)
		}
		if got.Err != nil {
			t.Errorf("flag %q: Err = %v", flag, got.Err)
		}
		if !strings.Contains(out, "opinionated mouse-first terminal code editor") {
			t.Errorf("flag %q printed no help:\n%s", flag, out)
		}
		if !strings.Contains(out, "ced fav <name>") {
			t.Errorf("flag %q: help omits the favorites block:\n%s", flag, out)
		}
	}
}

// TestResolveArgs_LastFlag covers every spelling of --last: the action
// is still edit and RootDir stays the safe "." fallback, with Last set
// so main can look the folder up. Resolution happens in main, not here,
// because resolveArgs is deliberately IO-free beyond os.Stat.
func TestResolveArgs_LastFlag(t *testing.T) {
	for _, flag := range []string{"--last", "-l", "last"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionEdit {
			t.Errorf("flag %q: action = %q, want edit", flag, got.Action)
		}
		if !got.Last {
			t.Errorf("flag %q: Last = false, want true", flag)
		}
		if got.RootDir != "." {
			t.Errorf("flag %q: RootDir = %q, want the . fallback", flag, got.RootDir)
		}
	}
}

// TestResolveArgs_BareCedDoesNotRestoreLast is the pin on the deliberate
// choice behind --last existing at all: bare `ced` opens the CURRENT
// directory. `cd myproj && ced` is the gesture this editor is launched
// with, and silently landing in a different project would make that
// reflex a lie — the folder's TABS come back instead (session restore).
func TestResolveArgs_BareCedDoesNotRestoreLast(t *testing.T) {
	got := resolveArgs(nil)
	if got.Last {
		t.Fatal("bare ced must not resolve to the last folder")
	}
	if got.RootDir != "." {
		t.Fatalf("RootDir = %q, want .", got.RootDir)
	}
}

// TestLastFolder_ReadsTheStateFile pins the --last lookup end to end
// against a real state file, and its two silent fallbacks: nothing
// recorded, and an unreadable file. Both must yield "" so main opens the
// current directory rather than refusing to start over a convenience
// file.
func TestLastFolder_ReadsTheStateFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)

	if got := lastFolder(); got != "" {
		t.Fatalf("lastFolder() with no state file = %q, want empty", got)
	}

	st := &session.Store{}
	st.Touch(filepath.Join(cfg, "older"))
	st.Touch(filepath.Join(cfg, "newest"))
	if err := st.Save(userconfig.StatePath()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, want := lastFolder(), filepath.Join(cfg, "newest"); got != want {
		t.Fatalf("lastFolder() = %q, want %q", got, want)
	}

	if err := os.WriteFile(userconfig.StatePath(), []byte("{{{"), 0644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if got := lastFolder(); got != "" {
		t.Fatalf("lastFolder() on a broken state file = %q, want empty", got)
	}
}

// TestResolveArgs_RemoteAndWaitFlags pins the $EDITOR surface: both
// flags parse, they compose in either order, and the file behind them is
// resolved exactly as a bare `ced <file>` would be — the handoff must
// not become a second way to interpret a path.
func TestResolveArgs_RemoteAndWaitFlags(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(file, []byte("msg"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		args       []string
		wantRemote bool
		wantWait   bool
	}{
		{"remote only", []string{"--remote", file}, true, false},
		{"wait only", []string{"--wait", file}, false, true},
		{"both", []string{"--remote", "--wait", file}, true, true},
		{"both reversed", []string{"--wait", "--remote", file}, true, true},
		{"nvim spelling", []string{"--remote-wait", file}, true, true},
		{"short forms", []string{"-r", "-w", file}, true, true},
		{"repeated", []string{"--wait", "--wait", file}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := resolveArgs(c.args)
			if res.Err != nil {
				t.Fatalf("Err = %v", res.Err)
			}
			if res.Action != actionEdit {
				t.Fatalf("Action = %q, want edit", res.Action)
			}
			if res.Remote != c.wantRemote || res.Wait != c.wantWait {
				t.Fatalf("Remote/Wait = %v/%v, want %v/%v",
					res.Remote, res.Wait, c.wantRemote, c.wantWait)
			}
			if res.OpenFile != file {
				t.Fatalf("OpenFile = %q, want %q", res.OpenFile, file)
			}
			if res.RootDir != dir {
				t.Fatalf("RootDir = %q, want %q", res.RootDir, dir)
			}
		})
	}
}

// TestResolveArgs_RemoteWaitNeedAFile pins the two refusals. Neither has
// anything to hand over or wait on, and a $EDITOR that silently opened
// the wrong thing would be worse than one that says so.
func TestResolveArgs_RemoteWaitNeedAFile(t *testing.T) {
	if res := resolveArgs([]string{"--wait"}); res.Err == nil {
		t.Error("--wait with no argument should be an error")
	}
	if res := resolveArgs([]string{"--remote", t.TempDir()}); res.Err == nil {
		t.Error("--remote on a directory should be an error")
	}
}

// TestResolveArgs_MissingFileWithWait covers `ced --wait notes.md` on a
// path that doesn't exist yet: the new-file intent survives the flags,
// because $EDITOR callers routinely name a file they are about to
// create.
func TestResolveArgs_MissingFileWithWait(t *testing.T) {
	target := filepath.Join(t.TempDir(), "fresh.md")
	res := resolveArgs([]string{"--wait", target})
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if !res.Wait || res.OpenFile != target {
		t.Fatalf("res = %+v, want Wait with OpenFile %q", res, target)
	}
}

// TestResolveArgs_RootOverride pins the flag the cats split depends on
// (internal/app/catssplit.go): a spawned sibling editor has to open the
// PROJECT its parent is in, and the file path alone can only ever say
// which directory the file sits in.
func TestResolveArgs_RootOverride(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "internal", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "x.go")
	if err := os.WriteFile(file, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without it, the nested file roots at its own parent — the right
	// guess for a human typing it, the wrong one for a program.
	if res := resolveArgs([]string{file}); res.RootDir != sub {
		t.Fatalf("bare file RootDir = %q, want %q", res.RootDir, sub)
	}
	// With it, the file still opens and the root is the project.
	res := resolveArgs([]string{"--root", root, file})
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if res.RootDir != root || res.OpenFile != file {
		t.Fatalf("res = %+v, want root %q + file %q", res, root, file)
	}
	// With nothing after it, it is just another way to open a project.
	if res := resolveArgs([]string{"--root", root}); res.Err != nil || res.RootDir != root || res.OpenFile != "" {
		t.Fatalf("res = %+v, want a plain open of %q", res, root)
	}
	// And it is checked rather than trusted: a bad root would otherwise
	// surface as a file tree rooted nowhere, seconds after the mistake.
	if res := resolveArgs([]string{"--root"}); res.Err == nil {
		t.Error("--root with no directory should be an error")
	}
	if res := resolveArgs([]string{"--root", filepath.Join(root, "nope"), file}); res.Err == nil {
		t.Error("--root on a missing directory should be an error")
	}
	if res := resolveArgs([]string{"--root", file, file}); res.Err == nil {
		t.Error("--root on a file should be an error")
	}
}

// -----------------------------------------------------------------------------
// ced fav
// -----------------------------------------------------------------------------

// favProject seeds a project with the layout the favorites examples
// assume and points ced's config at a throwaway directory, so no test
// run can read — or rewrite — the developer's real favorites.json.
// Returns the project root and a subdirectory deep inside it, because
// resolving from a subdirectory is the case the feature exists for.
func favProject(t *testing.T) (root, deep string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root = t.TempDir()
	deep = filepath.Join(root, "internal", "app")
	for _, d := range []string{
		filepath.Join(root, ".git"),
		filepath.Join(root, "ai_docs", "plans"),
		filepath.Join(root, "ai_docs", "claude_sessions"),
		deep,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, deep
}

// TestFav_OpenKeepsTheProjectRootAndRevealsTheFolder is the whole
// feature in one assertion. `ced fav plans` must NOT re-root at
// ai_docs/plans — `ced ai_docs/plans` already does that, and pays for it
// by throwing the project away (git status, gopls's rootUri, the finder
// index and every plugin's working directory are all derived from the
// root). What this adds is the thing a plain path cannot express: keep
// the project, move the view.
func TestFav_OpenKeepsTheProjectRootAndRevealsTheFolder(t *testing.T) {
	root, deep := favProject(t)
	if res := resolveArgs([]string{"fav", "add", "plans", "ai_docs/plans"}); res.Err != nil {
		t.Fatalf("add: %v", res.Err)
	}

	res := resolveArgs([]string{"fav", "plans", deep})
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if res.Action != actionEdit {
		t.Fatalf("Action = %q, want edit", res.Action)
	}
	if res.RootDir != root {
		t.Fatalf("RootDir = %q, want the PROJECT %q — a favorite must not re-root", res.RootDir, root)
	}
	if want := filepath.Join(root, "ai_docs", "plans"); res.Reveal != want {
		t.Fatalf("Reveal = %q, want %q", res.Reveal, want)
	}
	if res.OpenFile != "" {
		t.Fatalf("OpenFile = %q, want empty — a folder favorite opens no tab", res.OpenFile)
	}
}

// TestFav_ResolvesByWalkingUp pins the reason the walk exists: `ced fav
// plans` is typed from wherever the user is standing, which for a
// project of any size is not the project root. A version that only
// worked from the root would be half a feature.
func TestFav_ResolvesByWalkingUp(t *testing.T) {
	root, deep := favProject(t)
	resolveArgs([]string{"fav", "add", "clsess", "ai_docs/claude_sessions"})

	// From the root, from one level down, and from two — all three land
	// on the same project and the same folder.
	for _, from := range []string{root, filepath.Join(root, "internal"), deep} {
		res := resolveArgs([]string{"fav", "clsess", from})
		if res.Err != nil {
			t.Fatalf("from %q: %v", from, res.Err)
		}
		if res.RootDir != root {
			t.Fatalf("from %q: RootDir = %q, want %q", from, res.RootDir, root)
		}
		if want := filepath.Join(root, "ai_docs", "claude_sessions"); res.Reveal != want {
			t.Fatalf("from %q: Reveal = %q, want %q", from, res.Reveal, want)
		}
	}
}

// TestFav_ProjectOverrideShadowsTheGlobalFromASubdirectory covers the
// two-scope rule and the subtlety that makes it work: the override is
// keyed by the root that owns it, so it is only reachable if the name is
// looked up per CANDIDATE as the walk climbs. Looking it up once against
// the starting directory would make an override invisible from every
// subdirectory of the project it belongs to.
func TestFav_ProjectOverrideShadowsTheGlobalFromASubdirectory(t *testing.T) {
	root, deep := favProject(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolveArgs([]string{"fav", "add", "plans", "ai_docs/plans"})
	if res := resolveArgs([]string{"fav", "add", "--project", "plans", "docs/plans", deep}); res.Err != nil {
		t.Fatalf("add --project: %v", res.Err)
	}

	res := resolveArgs([]string{"fav", "plans", deep})
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if want := filepath.Join(root, "docs", "plans"); res.Reveal != want {
		t.Fatalf("Reveal = %q, want the project's %q", res.Reveal, want)
	}

	// Removing the override uncovers the global rather than unbinding
	// the name — and says so, because a user who doesn't expect that
	// will read the silence as the removal having failed.
	rmRes, out := runCLI(t, "fav", "rm", "plans", deep)
	if rmRes.Err != nil {
		t.Fatalf("rm: %v", rmRes.Err)
	}
	if !strings.Contains(out, "falls back to the global ai_docs/plans") {
		t.Fatalf("rm said nothing about the uncovered global:\n%s", out)
	}
	if res := resolveArgs([]string{"fav", "plans", deep}); res.Reveal != filepath.Join(root, "ai_docs", "plans") {
		t.Fatalf("after rm, Reveal = %q, want the global path", res.Reveal)
	}
}

// TestFav_UnknownNameListsWhatIsBound pins the error a typo produces. A
// bare "no such favorite" makes the user go and read a config file to
// find out what they meant; the names they DO have are right there.
func TestFav_UnknownNameListsWhatIsBound(t *testing.T) {
	_, deep := favProject(t)
	resolveArgs([]string{"fav", "add", "plans", "ai_docs/plans"})

	res := resolveArgs([]string{"fav", "plsn", deep})
	if res.Err == nil {
		t.Fatal("an unknown favorite should be an error")
	}
	if !strings.Contains(res.Err.Error(), "plans") {
		t.Fatalf("error names no known favorite: %v", res.Err)
	}

	// With nothing bound at all, the message is the one that teaches the
	// verb instead of listing an empty set.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	res = resolveArgs([]string{"fav", "plans", deep})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "ced fav add") {
		t.Fatalf("empty-set error should teach the add verb, got %v", res.Err)
	}
}

// TestFav_BoundButMissingNamesThePath separates the two failures a user
// can hit, because they have different fixes: an unbound name is a typo
// or a forgotten word, while a bound name whose directory isn't there is
// a project that doesn't follow the convention. The second must name the
// path so there is something to go and check.
func TestFav_BoundButMissingNamesThePath(t *testing.T) {
	_, deep := favProject(t)
	resolveArgs([]string{"fav", "add", "cosess", "ai_docs/copilot_sessions"})

	res := resolveArgs([]string{"fav", "cosess", deep})
	if res.Err == nil {
		t.Fatal("a favorite pointing at a missing directory should be an error")
	}
	if !strings.Contains(res.Err.Error(), "copilot_sessions") {
		t.Fatalf("error should name the path it looked for, got %v", res.Err)
	}
	if errorsIsNotFound(res.Err) {
		t.Fatal("a bound-but-missing favorite must not report as unbound")
	}
}

// errorsIsNotFound is a tiny readability wrapper — the distinction it
// checks is the point of the test above, not plumbing worth inlining.
func errorsIsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), favorites.ErrNotFound.Error())
}

// TestFav_AddRefusesItsOwnSubcommandNames is the trap this check exists
// to close: urfave resolves a subcommand before falling through to the
// open-a-favorite action, so a favorite called "list" would be written
// happily and then be permanently unreachable. Write time is the only
// moment the user can still pick another word.
func TestFav_AddRefusesItsOwnSubcommandNames(t *testing.T) {
	favProject(t)
	for _, name := range []string{"add", "rm", "list", "path"} {
		res := resolveArgs([]string{"fav", "add", name, "some/dir"})
		if res.Err == nil {
			t.Errorf("fav add %q should be refused — it could never be opened", name)
		}
	}
}

// TestFav_AddRefusesPathsThatLeaveTheProject pins the confinement rule
// at the moment it costs least. A favorite is a project-RELATIVE
// location; the one thing this feature must not become is a way for a
// config file to point the editor at /etc.
func TestFav_AddRefusesPathsThatLeaveTheProject(t *testing.T) {
	favProject(t)
	for _, bad := range []string{"/etc", "../../etc", "..", "."} {
		if res := resolveArgs([]string{"fav", "add", "bad", bad}); res.Err == nil {
			t.Errorf("fav add bad %q should be refused", bad)
		}
	}
}

// TestFav_ListMarksWhatDoesNotResolveHere covers the listing's one
// judgment call: a global default this project doesn't follow is still
// shown, marked. Hiding it would leave the user asking why a name they
// know they bound isn't listed; "missing here" is the answer to the
// question they are about to ask.
func TestFav_ListMarksWhatDoesNotResolveHere(t *testing.T) {
	_, deep := favProject(t)
	resolveArgs([]string{"fav", "add", "plans", "ai_docs/plans"})
	resolveArgs([]string{"fav", "add", "cosess", "ai_docs/copilot_sessions"})

	res, out := runCLI(t, "fav", "list", deep)
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if res.Action != actionDone {
		t.Fatalf("Action = %q — listing must not start an editor", res.Action)
	}
	if !strings.Contains(out, "plans") || !strings.Contains(out, "cosess") {
		t.Fatalf("listing dropped an entry:\n%s", out)
	}
	if !strings.Contains(out, "missing here") {
		t.Fatalf("listing didn't mark the unresolvable entry:\n%s", out)
	}

	// A bare `ced fav` lists too, rather than erroring on the missing
	// name: a user who has forgotten the word is better served by the
	// list than by usage text. (The project comes from the global
	// --root here only because a test has no working directory of its
	// own to stand in; in use it is simply where you are.)
	if _, bare := runCLI(t, "--root", deep, "fav"); !strings.Contains(bare, "plans") {
		t.Fatalf("bare `ced fav` should list:\n%s", bare)
	}

	// A DIRECTORY handed to the bare form is a name, and saying "no
	// favorite named /home/me/proj" would be an error about the wrong
	// thing. The refusal names what the argument actually is.
	if res := resolveArgs([]string{"fav", deep}); res.Err == nil ||
		!strings.Contains(res.Err.Error(), "plain word") {
		t.Fatalf("a path in the name slot should be refused as such, got %v", res.Err)
	}
}

// TestFav_PathPrintsOnlyThePath pins the one fav verb whose output is
// meant for another program: `cd "$(ced fav path plans)"` breaks the
// moment anything else shares the line.
func TestFav_PathPrintsOnlyThePath(t *testing.T) {
	root, deep := favProject(t)
	resolveArgs([]string{"fav", "add", "plans", "ai_docs/plans"})

	res, out := runCLI(t, "fav", "path", "plans", deep)
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if got, want := strings.TrimSpace(out), filepath.Join(root, "ai_docs", "plans"); got != want {
		t.Fatalf("output = %q, want exactly %q", got, want)
	}
}

// TestFav_FileFavoriteRevealsTheFile keeps a favorite from being a
// folders-only idea: `notes` → `ai_docs/NOTES.md` is an ordinary thing
// to want, and the app's RevealPath opens a file rather than merely
// highlighting its row.
func TestFav_FileFavoriteRevealsTheFile(t *testing.T) {
	root, deep := favProject(t)
	note := filepath.Join(root, "ai_docs", "NOTES.md")
	if err := os.WriteFile(note, []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolveArgs([]string{"fav", "add", "notes", "ai_docs/NOTES.md"})

	res := resolveArgs([]string{"fav", "notes", deep})
	if res.Err != nil {
		t.Fatalf("Err = %v", res.Err)
	}
	if res.Reveal != note || res.RootDir != root {
		t.Fatalf("res = %+v, want reveal %q in %q", res, note, root)
	}
}
