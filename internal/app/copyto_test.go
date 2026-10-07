// =============================================================================
// File: internal/app/copyto_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-07
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/history"
)

// copyToFixture builds a project with a file and a nested folder, plus a
// SEPARATE temp dir standing in for "another location" outside it.
func copyToFixture(t *testing.T) (a *App, root, elsewhere string) {
	t.Helper()
	root = t.TempDir()
	for rel, body := range map[string]string{
		"a.txt":         "alpha\n",
		"pkg/one.go":    "package pkg\n",
		"pkg/deep/x.md": "# x\n",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return newTestApp(t, root), root, t.TempDir()
}

// submitCopyTo types value into the open Copy to… prompt and submits it.
func submitCopyTo(t *testing.T, a *App, value string) {
	t.Helper()
	pm := promptOf(a)
	if pm == nil {
		t.Fatalf("expected the Copy to… prompt, got %T", a.modal)
	}
	pm.field = newTextField(value)
	pm.submit(a)
}

// readString reads a file the test expects to exist.
func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// TestPromptCopyTo_OpensAFolderPrompt pins the question: the title names
// the source and says a FOLDER is wanted, the field carries the
// destination history's ▾, and a first use is seeded with the source's
// own folder so the field is an edit of a real path.
func TestPromptCopyTo_OpensAFolderPrompt(t *testing.T) {
	a, root, _ := copyToFixture(t)
	a.promptCopyTo([]string{filepath.Join(root, "a.txt")})
	pm := promptOf(a)
	if pm == nil {
		t.Fatalf("expected a prompt, got %T", a.modal)
	}
	if pm.title != "Copy a.txt to folder" {
		t.Errorf("title = %q", pm.title)
	}
	if pm.histKind != history.CopyDestinations {
		t.Errorf("histKind = %q, want the copy-destination list", pm.histKind)
	}
	if got := pm.field.String(); got != displayPath(root) {
		t.Errorf("seed = %q, want the source's folder %q", got, displayPath(root))
	}
}

// TestPromptCopyTo_SeedsWithTheLastDestination pins the repeat rhythm:
// after one copy, the next prompt starts on the place it went.
func TestPromptCopyTo_SeedsWithTheLastDestination(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	a.promptCopyTo([]string{filepath.Join(root, "a.txt")})
	submitCopyTo(t, a, elsewhere)
	a.handlePasteDone(waitForPasteEvent(t, a))

	a.promptCopyTo([]string{filepath.Join(root, "pkg")})
	if got := promptOf(a).field.String(); got != displayPath(elsewhere) {
		t.Fatalf("seed = %q, want the last destination %q", got, displayPath(elsewhere))
	}
}

// TestPromptCopyTo_NothingToCopy pins the empty refusal: a flash, no
// prompt asking where to put nothing.
func TestPromptCopyTo_NothingToCopy(t *testing.T) {
	a, _, _ := copyToFixture(t)
	a.promptCopyTo(nil)
	if a.modal != nil {
		t.Fatalf("no prompt expected, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "Nothing to copy") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestCopyToWhat_NamesFoldersAndSets pins the source label: a folder
// wears a trailing /, a set is a count, a long name keeps its tail.
func TestCopyToWhat_NamesFoldersAndSets(t *testing.T) {
	_, root, _ := copyToFixture(t)
	if got := copyToWhat([]string{filepath.Join(root, "pkg")}); got != "pkg/" {
		t.Errorf("folder = %q, want pkg/", got)
	}
	if got := copyToWhat([]string{filepath.Join(root, "a.txt"), filepath.Join(root, "pkg")}); got != "2 items" {
		t.Errorf("set = %q, want 2 items", got)
	}
	long := strings.Repeat("n", 40) + ".go"
	got := copyToWhat([]string{filepath.Join(root, long)})
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, ".go") || len([]rune(got)) != copyToTitleMax {
		t.Errorf("long name = %q, want a %d-rune tail", got, copyToTitleMax)
	}
}

// TestResolveCopyDest_HomeAndRelative pins how typed text becomes a
// path: ~ is the home directory, relative is the PROJECT ROOT (not the
// process cwd), and the result is clean.
func TestResolveCopyDest_HomeAndRelative(t *testing.T) {
	a, root, _ := copyToFixture(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if got := a.resolveCopyDest("~/backup/"); got != filepath.Join(home, "backup") {
		t.Errorf("~ form = %q", got)
	}
	if got := a.resolveCopyDest("  ../sibling "); got != filepath.Join(filepath.Dir(root), "sibling") {
		t.Errorf("relative form = %q", got)
	}
	if got := a.resolveCopyDest("/abs/x/../y"); got != "/abs/y" {
		t.Errorf("absolute form = %q", got)
	}
}

// TestCopyTo_FileLandsOutsideTheProject is the feature: a file copied to
// a folder the tree cannot show, the original untouched, a receipt that
// says where, and the destination remembered for next time.
func TestCopyTo_FileLandsOutsideTheProject(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	src := filepath.Join(root, "a.txt")
	a.promptCopyTo([]string{src})
	submitCopyTo(t, a, elsewhere)
	a.handlePasteDone(waitForPasteEvent(t, a))

	if got := readString(t, filepath.Join(elsewhere, "a.txt")); got != "alpha\n" {
		t.Fatalf("copy = %q", got)
	}
	if got := readString(t, src); got != "alpha\n" {
		t.Fatalf("original changed: %q", got)
	}
	if want := "Copied a.txt to " + displayPath(elsewhere); a.statusMsg != want {
		t.Fatalf("flash = %q, want %q", a.statusMsg, want)
	}
	if got := a.searchHistory(history.CopyDestinations); len(got) != 1 || got[0] != displayPath(elsewhere) {
		t.Fatalf("destination history = %v", got)
	}
}

// TestCopyTo_FolderCopiesItsContents pins the recursive half: a folder
// arrives whole, nested levels included.
func TestCopyTo_FolderCopiesItsContents(t *testing.T) {
	a, _, elsewhere := copyToFixture(t)
	ctxCopyTo(a, treeNodeFor(t, a, "pkg"))
	submitCopyTo(t, a, elsewhere)
	a.handlePasteDone(waitForPasteEvent(t, a))

	if got := readString(t, filepath.Join(elsewhere, "pkg", "deep", "x.md")); got != "# x\n" {
		t.Fatalf("nested file = %q", got)
	}
	if got := readString(t, filepath.Join(elsewhere, "pkg", "one.go")); got != "package pkg\n" {
		t.Fatalf("top file = %q", got)
	}
	if want := "Copied pkg/ to " + displayPath(elsewhere); a.statusMsg != want {
		t.Fatalf("receipt = %q, want %q", a.statusMsg, want)
	}
}

// TestCopyTo_CollisionTakesACopyName pins the never-overwrite contract
// and its honest receipt: the destination's own file is untouched, the
// copy is "a copy.txt", and the flash says so.
func TestCopyTo_CollisionTakesACopyName(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	if err := os.WriteFile(filepath.Join(elsewhere, "a.txt"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.promptCopyTo([]string{filepath.Join(root, "a.txt")})
	submitCopyTo(t, a, elsewhere)
	a.handlePasteDone(waitForPasteEvent(t, a))

	if got := readString(t, filepath.Join(elsewhere, "a.txt")); got != "theirs\n" {
		t.Fatalf("existing file overwritten: %q", got)
	}
	if got := readString(t, filepath.Join(elsewhere, "a copy.txt")); got != "alpha\n" {
		t.Fatalf("copy = %q", got)
	}
	if !strings.HasSuffix(a.statusMsg, " as a copy.txt") {
		t.Fatalf("flash = %q, want the name the copy took", a.statusMsg)
	}
}

// TestCopyTo_RefusesAFileDestination pins one meaning per field: a path
// naming an existing FILE is refused, never read as "copy as".
func TestCopyTo_RefusesAFileDestination(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	target := filepath.Join(elsewhere, "taken.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.promptCopyTo([]string{filepath.Join(root, "a.txt")})
	submitCopyTo(t, a, target)

	if !strings.Contains(a.statusMsg, "is a file") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
	if got := readString(t, target); got != "keep\n" {
		t.Fatalf("file changed: %q", got)
	}
}

// TestCopyTo_MissingFolderConfirmsThenCreates pins the create gate: a
// destination that does not exist asks first, naming it; No leaves the
// disk alone; Yes creates it and copies.
func TestCopyTo_MissingFolderConfirmsThenCreates(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	dest := filepath.Join(elsewhere, "new", "deeper")
	src := []string{filepath.Join(root, "a.txt")}

	a.promptCopyTo(src)
	submitCopyTo(t, a, dest)
	cm := confirmOf(a)
	if cm == nil {
		t.Fatalf("expected a confirm, got %T", a.modal)
	}
	if body := strings.Join(cm.confirmBody(), "\n"); !strings.Contains(body, displayPath(dest)) {
		t.Fatalf("confirm body %q should name the folder", body)
	}
	a.closeModal() // No
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("declining must create nothing: err=%v", err)
	}

	a.promptCopyTo(src)
	submitCopyTo(t, a, dest)
	confirmOf(a).callback(a) // Yes
	a.handlePasteDone(waitForPasteEvent(t, a))
	if got := readString(t, filepath.Join(dest, "a.txt")); got != "alpha\n" {
		t.Fatalf("copy = %q", got)
	}
}

// TestCopyTo_RefusedBeforeTheCreateConfirm pins the order: a copy that
// would be refused anyway (a folder into its own not-yet-made subfolder)
// is refused BEFORE the user is asked to create a folder for it.
func TestCopyTo_RefusedBeforeTheCreateConfirm(t *testing.T) {
	a, root, _ := copyToFixture(t)
	a.promptCopyTo([]string{filepath.Join(root, "pkg")})
	submitCopyTo(t, a, filepath.Join(root, "pkg", "backup"))

	if a.modal != nil {
		t.Fatalf("no confirm expected, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "into itself") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "backup")); !os.IsNotExist(err) {
		t.Fatalf("refused copy created its folder: err=%v", err)
	}
}

// TestCopyTo_RefusesAFolderIntoItselfThroughASymlink pins the resolved
// guard end to end: a typed link into the source is refused, not walked.
func TestCopyTo_RefusesAFolderIntoItselfThroughASymlink(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	link := filepath.Join(elsewhere, "into-pkg")
	if err := os.Symlink(filepath.Join(root, "pkg", "deep"), link); err != nil {
		t.Fatal(err)
	}
	a.promptCopyTo([]string{filepath.Join(root, "pkg")})
	submitCopyTo(t, a, link)
	if !strings.Contains(a.statusMsg, "into itself") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "pkg", "deep"))
	if len(entries) != 1 {
		t.Fatalf("source folder grew: %d entries", len(entries))
	}
}

// TestCopyTo_CopiesTheUnsavedBuffer pins the buffer-before-disk rule for
// this verb: the copy holds the tab's edits, the file on disk and the
// tab's dirty state are left as they were, and the receipt says so.
func TestCopyTo_CopiesTheUnsavedBuffer(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	src := filepath.Join(root, "a.txt")
	a.openFile(src)
	tab := a.activeTabPtr()
	tab.InsertString("edited ")

	a.menuCopyFileTo()
	submitCopyTo(t, a, elsewhere)
	a.handlePasteDone(waitForPasteEvent(t, a))

	if got := readString(t, filepath.Join(elsewhere, "a.txt")); got != "edited alpha\n" {
		t.Fatalf("copy = %q, want the buffer", got)
	}
	if got := readString(t, src); got != "alpha\n" {
		t.Fatalf("original saved behind the user's back: %q", got)
	}
	if !tab.Dirty {
		t.Fatal("the tab must stay dirty")
	}
	if !strings.HasSuffix(a.statusMsg, "(with unsaved edits)") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestCopyTo_ASetReportsItsCount pins the selection door: the whole set
// lands, a vanished member is announced at the start, and the receipt
// counts what arrived.
func TestCopyTo_ASetReportsItsCount(t *testing.T) {
	a, root, elsewhere := copyToFixture(t)
	paths := []string{filepath.Join(root, "a.txt"), filepath.Join(root, "pkg"), filepath.Join(root, "gone.txt")}
	a.promptCopyTo(paths)
	submitCopyTo(t, a, elsewhere)
	if !strings.Contains(a.statusMsg, "(1 no longer exist)") {
		t.Fatalf("start flash = %q, want the missing count", a.statusMsg)
	}
	a.handlePasteDone(waitForPasteEvent(t, a))
	if want := "Copied 2 items to " + displayPath(elsewhere); a.statusMsg != want {
		t.Fatalf("receipt = %q, want %q", a.statusMsg, want)
	}
}

// TestCopyToGoneMessage pins the all-missing refusal for one and many.
func TestCopyToGoneMessage(t *testing.T) {
	if got := copyToGoneMessage([]string{"/x/a.txt"}, []string{"a.txt"}); got != "Copy failed: a.txt no longer exists" {
		t.Errorf("one = %q", got)
	}
	got := copyToGoneMessage([]string{"/x/a", "/x/b"}, []string{"a", "b"})
	if !strings.Contains(got, "none of the 2 items") || !strings.Contains(got, "a, b") {
		t.Errorf("many = %q", got)
	}
}

// TestHandleCopyToDone_ReportsAPartialFailure pins the failure receipt:
// when some items landed before the error, the flash says how many and
// where, because they stay.
func TestHandleCopyToDone_ReportsAPartialFailure(t *testing.T) {
	a, _, elsewhere := copyToFixture(t)
	a.handlePasteDone(&pasteDoneEvent{into: elsewhere, count: 2, err: os.ErrPermission})
	if !strings.Contains(a.statusMsg, "after 2 item(s) reached "+displayPath(elsewhere)) {
		t.Fatalf("flash = %q", a.statusMsg)
	}
	a.handlePasteDone(&pasteDoneEvent{into: elsewhere, err: os.ErrPermission})
	if !strings.HasPrefix(a.statusMsg, "Copy failed: ") || strings.Contains(a.statusMsg, "reached") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestCopyToReceipt pins the success wording for a set, a kept name and
// a changed name.
func TestCopyToReceipt(t *testing.T) {
	cases := []struct {
		e    pasteDoneEvent
		want string
	}{
		{pasteDoneEvent{into: "/d", count: 3}, "Copied 3 items to /d"},
		{pasteDoneEvent{into: "/d", count: 1, src: "/s/a.go", dest: "/d/a.go"}, "Copied a.go to /d"},
		{pasteDoneEvent{into: "/d", count: 1, src: "/s/a.go", dest: "/d/a copy.go"}, "Copied a.go to /d as a copy.go"},
	}
	for _, c := range cases {
		if got := copyToReceipt(&c.e); got != c.want {
			t.Errorf("copyToReceipt = %q, want %q", got, c.want)
		}
	}
}

// TestMenuCopyFileTo_NeedsAFileTab pins the ≡ file row: no tab, no
// prompt; with one, the prompt is about that file.
func TestMenuCopyFileTo_NeedsAFileTab(t *testing.T) {
	a, root, _ := copyToFixture(t)
	a.menuCopyFileTo()
	if a.modal != nil {
		t.Fatalf("no tab: no prompt expected, got %T", a.modal)
	}
	a.openFile(filepath.Join(root, "a.txt"))
	a.menuCopyFileTo()
	if pm := promptOf(a); pm == nil || pm.title != "Copy a.txt to folder" {
		t.Fatalf("prompt = %+v", pm)
	}
}

// TestMenuCopyFolderTo_ActiveFolderOrProject pins the ≡ folder row: its
// label and its action agree on the target — the active subfolder when
// there is one, the whole project otherwise.
func TestMenuCopyFolderTo_ActiveFolderOrProject(t *testing.T) {
	a, root, _ := copyToFixture(t)
	if got := a.copyFolderToLabel(); got != "Copy project folder to…" {
		t.Errorf("no subfolder: label = %q", got)
	}
	a.menuCopyFolderTo()
	if pm := promptOf(a); pm == nil || pm.title != "Copy "+filepath.Base(root)+"/ to folder" {
		t.Fatalf("project prompt = %+v", pm)
	}
	a.closeModal()

	a.setActiveFolder(filepath.Join(root, "pkg"))
	if got := a.copyFolderToLabel(); got != "Copy folder (pkg/) to…" {
		t.Errorf("subfolder: label = %q", got)
	}
	a.menuCopyFolderTo()
	if pm := promptOf(a); pm == nil || pm.title != "Copy pkg/ to folder" {
		t.Fatalf("subfolder prompt = %+v", pm)
	}
}

// TestCtxCopyTo_OfferedOnTheRoot pins the tree door on the root, which
// Copy is not: the project can be copied out, and copying it into itself
// is still refused.
func TestCtxCopyTo_OfferedOnTheRoot(t *testing.T) {
	a, root, _ := copyToFixture(t)
	ctxCopyTo(a, a.tree.Root)
	submitCopyTo(t, a, filepath.Join(root, "pkg"))
	if !strings.Contains(a.statusMsg, "into itself") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}
