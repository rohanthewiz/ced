// =============================================================================
// File: internal/app/openfile_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// openPathFixture builds a project root holding sub/a.go, plus a SEPARATE
// temp dir standing in for "somewhere else on disk" with a notes file in
// it — the case the verb exists for.
func openPathFixture(t *testing.T) (a *App, root, other string) {
	t.Helper()
	root = t.TempDir()
	other = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := map[string]string{
		filepath.Join(root, "sub", "a.go"):  "package sub\n",
		filepath.Join(other, "notes.txt"): "outside\n",
	}
	for p, body := range seed {
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	return newTestApp(t, root), root, other
}

// touchAll creates empty files (and, for names ending in /, folders)
// under dir — the listing a completion test reads.
func touchAll(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, n)
		var err error
		if strings.HasSuffix(n, "/") {
			err = os.MkdirAll(p, 0755)
		} else {
			err = os.WriteFile(p, nil, 0644)
		}
		if err != nil {
			t.Fatalf("touch %s: %v", n, err)
		}
	}
}

// TestResolveOpenPath_AbsoluteRelativeAndTilde pins the spelling rule the
// verb shares with Open folder: absolute as given, relative to the ROOT
// (not the process cwd), and a leading ~ expanded.
func TestResolveOpenPath_AbsoluteRelativeAndTilde(t *testing.T) {
	a, root, other := openPathFixture(t)
	want := filepath.Join(other, "notes.txt")

	got, isDir, err := a.resolveOpenPath(want)
	if err != nil || isDir || got != want {
		t.Fatalf("absolute = %q, %v, %v; want %q", got, isDir, err, want)
	}

	got, _, err = a.resolveOpenPath("sub/a.go")
	if err != nil || got != filepath.Join(root, "sub", "a.go") {
		t.Fatalf("relative = %q, %v; want it under the root", got, err)
	}

	t.Setenv("HOME", other)
	got, _, err = a.resolveOpenPath("~/notes.txt")
	if err != nil || got != want {
		t.Fatalf("tilde = %q, %v; want %q", got, err, want)
	}
}

// TestResolveOpenPath_RefusesMissingFoldersAndBlank: every refusal says
// why, and a folder is flagged so the caller can re-prompt into it.
func TestResolveOpenPath_RefusesMissingFoldersAndBlank(t *testing.T) {
	a, root, _ := openPathFixture(t)

	if _, _, err := a.resolveOpenPath(filepath.Join(root, "nope.go")); err == nil ||
		!strings.Contains(err.Error(), "no such file") {
		t.Fatalf("missing: err = %v, want a 'no such file' reason", err)
	}
	_, isDir, err := a.resolveOpenPath(filepath.Join(root, "sub"))
	if err == nil || !isDir || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("folder: isDir=%v err=%v, want flagged and explained", isDir, err)
	}
	if _, _, err := a.resolveOpenPath("   "); err == nil {
		t.Fatal("a blank path must not resolve")
	}
}

// TestResolveOpenPath_RefusesNonRegularFiles: a device (or fifo) would
// be read on the main loop and could block it forever, so it is refused
// before editor.NewTab ever sees it.
func TestResolveOpenPath_RefusesNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/null on windows")
	}
	a, _, _ := openPathFixture(t)
	if _, _, err := a.resolveOpenPath("/dev/null"); err == nil ||
		!strings.Contains(err.Error(), "regular") {
		t.Fatalf("err = %v, want a 'not a regular file' refusal", err)
	}
}

// TestOpenPathTyped_OpensAFileOutsideTheRoot is the feature: a typed path
// outside the project opens as a tab, and the active folder stays inside
// the project — the ≡ folder verbs must not arm on a folder the tree
// cannot show.
func TestOpenPathTyped_OpensAFileOutsideTheRoot(t *testing.T) {
	a, root, other := openPathFixture(t)
	sub := filepath.Join(root, "sub")
	a.setActiveFolder(sub)

	a.openPathTyped(filepath.Join(other, "notes.txt"))

	tab := a.activeTabPtr()
	if tab == nil || filepath.Base(tab.Path) != "notes.txt" {
		t.Fatalf("active tab = %v, want notes.txt open", tab)
	}
	if a.activeFolder != sub {
		t.Fatalf("activeFolder = %q, want it left at %q", a.activeFolder, sub)
	}
	if a.modal != nil {
		t.Fatalf("a successful open left %T up", a.modal)
	}
}

// TestOpenPathTyped_InsideRootTakesItsFolder: for a file inside the
// project the verb is plain openFile — its folder becomes active, so the
// next New file lands beside it.
func TestOpenPathTyped_InsideRootTakesItsFolder(t *testing.T) {
	a, root, _ := openPathFixture(t)

	a.openPathTyped("sub/a.go")

	if tab := a.activeTabPtr(); tab == nil || tab.Path != filepath.Join(root, "sub", "a.go") {
		t.Fatalf("active tab = %v, want sub/a.go", tab)
	}
	if want := filepath.Join(root, "sub"); a.activeFolder != want {
		t.Fatalf("activeFolder = %q, want %q", a.activeFolder, want)
	}
}

// TestOpenPathTyped_MistakeReopensWithTheValue: a refusal re-opens the
// prompt holding what was typed, the reason under the field — and a
// folder gains its trailing slash so the next Tab lists it.
func TestOpenPathTyped_MistakeReopensWithTheValue(t *testing.T) {
	a, root, _ := openPathFixture(t)

	typed := filepath.Join(root, "sub", "b.go")
	a.openPathTyped(typed)
	m := promptOf(a)
	if m == nil {
		t.Fatalf("missing file: modal = %T, want the prompt back", a.modal)
	}
	if m.field.String() != typed || !strings.Contains(m.completeNote, "no such file") {
		t.Fatalf("re-prompt = %q / note %q, want the same value and the reason", m.field.String(), m.completeNote)
	}
	if len(a.tabs) != 0 {
		t.Fatalf("a refused open made %d tabs", len(a.tabs))
	}

	a.openPathTyped(filepath.Join(root, "sub"))
	m = promptOf(a)
	if m == nil || m.field.String() != filepath.Join(root, "sub")+"/" {
		t.Fatalf("folder: re-prompt = %v, want the folder with a trailing /", m)
	}
}

// TestMenuOpenFileByPath_SeedsTheActiveFilesFolder: the field opens on
// the folder of what is on screen (else the root), with a trailing / so
// Tab or a typed name continues the path.
func TestMenuOpenFileByPath_SeedsTheActiveFilesFolder(t *testing.T) {
	a, root, _ := openPathFixture(t)

	a.menuOpenFileByPath()
	m := promptOf(a)
	if m == nil {
		t.Fatalf("menu row opened %T, want the path prompt", a.modal)
	}
	if want := displayPath(root) + "/"; m.field.String() != want {
		t.Fatalf("no tab: seed = %q, want the root %q", m.field.String(), want)
	}
	if m.complete == nil {
		t.Fatal("the Open file prompt must carry a Tab completer")
	}

	a.closeModal()
	a.openFile(filepath.Join(root, "sub", "a.go"))
	a.menuOpenFileByPath()
	if m = promptOf(a); m == nil {
		t.Fatalf("menu row opened %T, want the path prompt", a.modal)
	}
	if want := displayPath(filepath.Join(root, "sub")) + "/"; m.field.String() != want {
		t.Fatalf("with a tab: seed = %q, want its folder %q", m.field.String(), want)
	}
}

// TestCompleteOpenPath_OneMatchCompletesAFolderWithSlash: a unique match
// is completed whole, and a folder gets the slash the next Tab descends
// through.
func TestCompleteOpenPath_OneMatchCompletesAFolderWithSlash(t *testing.T) {
	a, root, _ := openPathFixture(t)
	touchAll(t, root, "internal/", "main.go")

	next, note := a.completeOpenPath(root + "/int")
	if next != root+"/internal/" || note != "" {
		t.Fatalf("folder = %q / %q, want %q and no note", next, note, root+"/internal/")
	}
	next, _ = a.completeOpenPath(root + "/ma")
	if next != root+"/main.go" {
		t.Fatalf("file = %q, want main.go with no slash", next)
	}
}

// TestCompleteOpenPath_SeveralExtendToTheirCommonPrefix: an ambiguous
// Tab extends as far as the candidates agree and lists them, count first
// so a clipped note still says how many there are.
func TestCompleteOpenPath_SeveralExtendToTheirCommonPrefix(t *testing.T) {
	a, root, _ := openPathFixture(t)
	touchAll(t, root, "config.json", "config.yaml", "configs/")

	next, note := a.completeOpenPath("con")
	if next != "config" {
		t.Fatalf("next = %q, want the shared prefix 'config'", next)
	}
	if !strings.HasPrefix(note, "3 matches:") || !strings.Contains(note, "configs/") {
		t.Fatalf("note = %q, want the count then the names, folders slashed", note)
	}

	// Already at the shared prefix: unchanged, still listed.
	if again, n := a.completeOpenPath("config"); again != "config" || n == "" {
		t.Fatalf("second Tab = %q / %q, want unchanged with the list", again, n)
	}
}

// TestCompleteOpenPath_DotfilesOnlyWhenAsked: the shell's rule — hidden
// names are offered once the prefix starts with a dot, never otherwise.
func TestCompleteOpenPath_DotfilesOnlyWhenAsked(t *testing.T) {
	a, root, _ := openPathFixture(t)
	touchAll(t, root, ".zshrc", "zeta.txt")

	if next, _ := a.completeOpenPath(root + "/"); strings.Contains(next, ".zshrc") {
		t.Fatalf("bare Tab offered a dotfile: %q", next)
	}
	if next, _ := a.completeOpenPath(root + "/.z"); next != root+"/.zshrc" {
		t.Fatalf("dot prefix = %q, want .zshrc", next)
	}
}

// TestCompleteOpenPath_FoldsCaseOnlyWithoutAnExactMatch: "readme" finds
// README.md when nothing matches exactly, an exact-case match outranks
// the folded ones, and Tab never deletes letters the user typed.
func TestCompleteOpenPath_FoldsCaseOnlyWithoutAnExactMatch(t *testing.T) {
	a, root, _ := openPathFixture(t)
	dir := filepath.Join(root, "docs")
	touchAll(t, root, "docs/")
	touchAll(t, dir, "README.md")

	if next, _ := a.completeOpenPath("docs/readme"); next != "docs/README.md" {
		t.Fatalf("folded = %q, want docs/README.md", next)
	}

	touchAll(t, dir, "readme-old.md")
	if next, _ := a.completeOpenPath("docs/readme"); next != "docs/readme-old.md" {
		t.Fatalf("exact should win over folded, got %q", next)
	}

	// Two folded candidates that share less than the typed prefix: the
	// value must not shrink to their common "R".
	touchAll(t, dir, "Readme.txt")
	if err := os.Remove(filepath.Join(dir, "readme-old.md")); err != nil {
		t.Fatal(err)
	}
	if next, note := a.completeOpenPath("docs/READm"); next != "docs/READm" || !strings.HasPrefix(note, "2 matches") {
		t.Fatalf("folded pair = %q / %q, want the value kept and both listed", next, note)
	}
}

// TestCompleteOpenPath_KeepsTheTildeSpelling: only the last segment is
// rewritten, so a ~ the user typed is still a ~ after Tab — and a bare ~
// completes to the home folder itself.
func TestCompleteOpenPath_KeepsTheTildeSpelling(t *testing.T) {
	a, _, other := openPathFixture(t)
	t.Setenv("HOME", other)

	if next, _ := a.completeOpenPath("~/no"); next != "~/notes.txt" {
		t.Fatalf("tilde = %q, want ~/notes.txt", next)
	}
	if next, _ := a.completeOpenPath("~"); next != "~/" {
		t.Fatalf("bare ~ = %q, want ~/", next)
	}
}

// TestCompleteOpenPath_NoMatchAndUnreadable: nothing to offer leaves the
// value alone and says so — "no match" for a name, the folder for a
// directory part that can't be listed.
func TestCompleteOpenPath_NoMatchAndUnreadable(t *testing.T) {
	a, root, _ := openPathFixture(t)

	if next, note := a.completeOpenPath(root + "/zz"); next != root+"/zz" || note != "no match" {
		t.Fatalf("no match = %q / %q", next, note)
	}
	gone := root + "/missing/dir/x"
	if next, note := a.completeOpenPath(gone); next != gone || !strings.HasPrefix(note, "can't list") {
		t.Fatalf("unreadable = %q / %q", next, note)
	}
}

// TestCompleteOpenPath_FollowsASymlinkedFolder: a link to a folder
// completes with a slash, like the folder it points at.
func TestCompleteOpenPath_FollowsASymlinkedFolder(t *testing.T) {
	a, root, other := openPathFixture(t)
	if err := os.Symlink(other, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if next, _ := a.completeOpenPath("lin"); next != "linked/" {
		t.Fatalf("symlinked folder = %q, want linked/", next)
	}
}

// TestCommonRunePrefix is a smoke test of the prefix helper, including a
// multi-byte name that must not be cut mid-rune.
func TestCommonRunePrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"alone"}, "alone"},
		{[]string{"config.json", "config.yaml"}, "config."},
		{[]string{"résumé.md", "résumé.txt"}, "résumé."},
		{[]string{"abc", "xyz"}, ""},
	}
	for _, c := range cases {
		if got := commonRunePrefix(c.in); got != c.want {
			t.Errorf("commonRunePrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOpenFileByPath_TabInThePromptCompletes drives the real key path:
// with the prompt up, a Tab through handleKey reaches the completer
// (the editor's own Tab — indent, ghost accept — never sees it), and
// Enter then opens the completed path.
func TestOpenFileByPath_TabInThePromptCompletes(t *testing.T) {
	a, _, other := openPathFixture(t)
	a.promptOpenPath(other+"/no", "")

	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if m := promptOf(a); m == nil || m.field.String() != other+"/notes.txt" {
		t.Fatalf("after Tab: prompt = %v, want the completed path", m)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if tab := a.activeTabPtr(); tab == nil || filepath.Base(tab.Path) != "notes.txt" {
		t.Fatalf("Enter after Tab opened %v, want notes.txt", tab)
	}
}
