// =============================================================================
// File: internal/app/workspaceedit_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// wsTestFile writes a file under the app's root and returns its path.
func wsTestFile(t *testing.T, a *App, name, body string) string {
	t.Helper()
	p := filepath.Join(a.rootDir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// wsEdit builds a one-document WorkspaceEdit replacing a single-line range.
func wsEdit(path string, line, start, end int, newText string) *lsp.WorkspaceEdit {
	return &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{{
		Path: path,
		URI:  lsp.PathToURI(path),
		Edits: []lsp.TextEdit{{
			Range: lsp.Range{
				Start: lsp.Position{Line: line, Character: start},
				End:   lsp.Position{Line: line, Character: end},
			},
			NewText: newText,
		}},
	}}}
}

// wsOpenTab opens path in the app and returns the tab, with the LSP sync
// bookkeeping seeded so the staleness guards see a synced document.
func wsOpenTab(t *testing.T, a *App, path string) *editor.Tab {
	t.Helper()
	a.openFile(path)
	tab := a.tabByPath(path)
	if tab == nil {
		t.Fatalf("openFile(%s) produced no tab", path)
	}
	if a.lsp.syncedRev == nil {
		a.lsp.syncedRev = map[string]int{}
		a.lsp.versions = map[string]int{}
	}
	a.lsp.syncedRev[path] = tab.EditRev
	a.lsp.versions[path] = 1
	return tab
}

// TestApplyServerEdit_OpenTabEditsBufferNotDisk pins the rule that decides
// where every edit lands: the server answered from the text ced synced to it,
// which for an open tab is the BUFFER including unsaved edits. Writing that
// file's disk copy instead would apply coordinates to text they were never
// measured against.
func TestApplyServerEdit_OpenTabEditsBufferNotDisk(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "main.go", "package main\nvar foo int\n")
	tab := wsOpenTab(t, a, path)

	req := a.captureWSRequest(tab)
	if !a.applyServerEdit(wsEdit(path, 1, 4, 7, "bar"), "Rename foo → bar", req) {
		t.Fatal("applyServerEdit refused a valid edit")
	}
	if a.modal != nil {
		if _, isConfirm := a.modal.(*confirmModal); isConfirm {
			t.Fatal("an all-open edit asked for confirmation — nothing leaves memory")
		}
	}
	if got, want := tab.Buffer.Lines[1], "var bar int"; got != want {
		t.Errorf("buffer line = %q, want %q", got, want)
	}
	if !tab.Dirty {
		t.Error("tab not marked dirty — an open tab is left for the user to save")
	}
	onDisk, _ := os.ReadFile(path)
	if !strings.Contains(string(onDisk), "var foo int") {
		t.Errorf("disk file was rewritten behind an open tab: %q", onDisk)
	}
}

// TestApplyServerEdit_ClosedFileWritesDiskAndOpensNoTab pins the decision
// that keeps a twelve-file rename usable: files with no tab are edited in a
// DETACHED tab and written, never opened. Opening each one would fire didOpen,
// every plugin hook and a syntax pass, and leave a dirty tab behind.
func TestApplyServerEdit_ClosedFileWritesDiskAndOpensNoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "other.go", "package main\nvar foo int\n")
	tabsBefore := len(a.tabs)

	if !a.applyServerEdit(wsEdit(path, 1, 4, 7, "bar"), "Rename foo → bar", wsRequest{}) {
		t.Fatal("applyServerEdit refused a valid edit")
	}
	// A disk-touching edit must confirm first — nothing is written until then.
	cm, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("modal = %T, want *confirmModal for an edit that writes files", a.modal)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "var foo int") {
		t.Fatal("the file was written before the user confirmed")
	}
	cm.yes(a)

	if len(a.tabs) != tabsBefore {
		t.Errorf("tab count = %d, want %d — the apply opened tabs", len(a.tabs), tabsBefore)
	}
	data, _ := os.ReadFile(path)
	if got, want := string(data), "package main\nvar bar int\n"; got != want {
		t.Errorf("disk file = %q, want %q", got, want)
	}
}

// TestApplyServerEdit_RoundTripsCRLFAndBOM pins that a detached write goes
// through the same encode path an ordinary Save does. Editing a CRLF file
// must not silently convert it to LF and show the whole file as changed.
func TestApplyServerEdit_RoundTripsCRLFAndBOM(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	body := "\xEF\xBB\xBFpackage main\r\nvar foo int\r\n"
	path := wsTestFile(t, a, "crlf.go", body)

	if !a.applyServerEdit(wsEdit(path, 1, 4, 7, "bar"), "Rename", wsRequest{}) {
		t.Fatal("applyServerEdit refused a valid edit")
	}
	a.modal.(*confirmModal).yes(a)

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.HasPrefix(got, "\xEF\xBB\xBF") {
		t.Error("BOM was lost")
	}
	if !strings.Contains(got, "var bar int\r\n") {
		t.Errorf("CRLF was not preserved: %q", got)
	}
}

// TestApplyServerEdit_UndoRestoresEveryFile is the primitive's whole promise:
// one gesture puts every touched file back, buffers and disk alike.
func TestApplyServerEdit_UndoRestoresEveryFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openPath := wsTestFile(t, a, "open.go", "package main\nvar foo int\n")
	diskPath := wsTestFile(t, a, "disk.go", "package main\nvar foo bool\n")
	tab := wsOpenTab(t, a, openPath)

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(openPath, 1, 4, 7, "bar").Documents[0],
		wsEdit(diskPath, 1, 4, 7, "bar").Documents[0],
	}}
	if !a.applyServerEdit(we, "Rename foo → bar", a.captureWSRequest(tab)) {
		t.Fatal("applyServerEdit refused a valid edit")
	}
	a.modal.(*confirmModal).yes(a)

	if !strings.Contains(tab.Buffer.Lines[1], "bar") {
		t.Fatalf("open tab not edited: %q", tab.Buffer.Lines[1])
	}
	if data, _ := os.ReadFile(diskPath); !strings.Contains(string(data), "bar") {
		t.Fatalf("disk file not edited: %q", data)
	}
	if a.wsGroup == nil || len(a.wsGroup.parts) != 2 {
		t.Fatalf("journal = %v, want 2 participants", a.wsGroup)
	}

	a.closeModal()
	if !a.undoWorkspaceGroup() {
		t.Fatal("group undo refused")
	}
	if got, want := tab.Buffer.Lines[1], "var foo int"; got != want {
		t.Errorf("open tab after undo = %q, want %q", got, want)
	}
	data, _ := os.ReadFile(diskPath)
	if got, want := string(data), "package main\nvar foo bool\n"; got != want {
		t.Errorf("disk file after undo = %q, want %q", got, want)
	}
}

// TestMenuUndo_ClaimsTheGroupFromAParticipantTab pins the gesture the journal
// exists for. A reflex Esc-u in one renamed file must not roll back that file
// alone and leave the rest — a half-applied refactor with nothing on screen
// to say so.
func TestMenuUndo_ClaimsTheGroupFromAParticipantTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	one := wsTestFile(t, a, "one.go", "var foo int\n")
	two := wsTestFile(t, a, "two.go", "var foo int\n")
	tabOne := wsOpenTab(t, a, one)
	tabTwo := wsOpenTab(t, a, two)

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(one, 0, 4, 7, "bar").Documents[0],
		wsEdit(two, 0, 4, 7, "bar").Documents[0],
	}}
	if !a.applyServerEdit(we, "Rename foo → bar", a.captureWSRequest(tabOne)) {
		t.Fatal("applyServerEdit refused")
	}
	a.closeModal()

	// Cursor in the FIRST file; plain undo must unwind both.
	a.activeTab = 0
	if !a.wsGroupClaimsUndo(tabOne) {
		t.Fatal("the group did not claim undo from a participant tab")
	}
	a.menuUndo()
	if strings.Contains(tabOne.Buffer.Lines[0], "bar") {
		t.Errorf("file one not undone: %q", tabOne.Buffer.Lines[0])
	}
	if strings.Contains(tabTwo.Buffer.Lines[0], "bar") {
		t.Errorf("file two survived the group undo: %q — half-applied rename",
			tabTwo.Buffer.Lines[0])
	}
	if !strings.Contains(a.statusMsg, "Undid") {
		t.Errorf("flash = %q, want it to report the group undo", a.statusMsg)
	}
}

// TestMenuUndo_FallsThroughAndClearsWhenAParticipantMoved pins the loud
// degradation. Once the user has typed in one of the files, the edit is no
// longer one step — so plain undo does the ordinary per-tab thing, SAYS why,
// and clears the slot so a later press can't half-apply the rest.
func TestMenuUndo_FallsThroughAndClearsWhenAParticipantMoved(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	one := wsTestFile(t, a, "one.go", "var foo int\n")
	two := wsTestFile(t, a, "two.go", "var foo int\n")
	tabOne := wsOpenTab(t, a, one)
	tabTwo := wsOpenTab(t, a, two)

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(one, 0, 4, 7, "bar").Documents[0],
		wsEdit(two, 0, 4, 7, "bar").Documents[0],
	}}
	a.applyServerEdit(we, "Rename foo → bar", a.captureWSRequest(tabOne))
	a.closeModal()

	// The user types in the second file — the group is no longer one step.
	tabTwo.InsertRune('x')

	a.activeTab = 0
	a.menuUndo()

	if a.wsGroup != nil {
		t.Error("the journal survived a degraded undo — a later press could half-apply the rest")
	}
	if !strings.Contains(a.statusMsg, "no longer one step") ||
		!strings.Contains(a.statusMsg, "two.go") {
		t.Errorf("flash = %q, want it to name two.go and say the group broke", a.statusMsg)
	}
	// It still undid THIS file — an undo that does nothing reads as broken.
	if strings.Contains(tabOne.Buffer.Lines[0], "bar") {
		t.Errorf("the active tab was not undone: %q", tabOne.Buffer.Lines[0])
	}
}

// TestMenuRedo_ClaimsTheGroupAfterAGroupUndo pins the mirror, so a redo can't
// re-apply the rename in one file while the others stay unwound.
func TestMenuRedo_ClaimsTheGroupAfterAGroupUndo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	one := wsTestFile(t, a, "one.go", "var foo int\n")
	two := wsTestFile(t, a, "two.go", "var foo int\n")
	tabOne := wsOpenTab(t, a, one)
	tabTwo := wsOpenTab(t, a, two)

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(one, 0, 4, 7, "bar").Documents[0],
		wsEdit(two, 0, 4, 7, "bar").Documents[0],
	}}
	a.applyServerEdit(we, "Rename foo → bar", a.captureWSRequest(tabOne))
	a.closeModal()
	a.activeTab = 0
	a.menuUndo()

	if !a.wsGroupClaimsRedo(tabOne) {
		t.Fatal("the group did not claim redo after a group undo")
	}
	a.menuRedo()
	if !strings.Contains(tabOne.Buffer.Lines[0], "bar") ||
		!strings.Contains(tabTwo.Buffer.Lines[0], "bar") {
		t.Errorf("redo did not re-apply both files: %q / %q",
			tabOne.Buffer.Lines[0], tabTwo.Buffer.Lines[0])
	}
}

// TestPlanWorkspaceEdit_RefusesStaleOrigin pins the captured-(path, EditRev)
// rule. If the document the question was asked about has moved, every
// coordinate in the answer describes text that is gone.
func TestPlanWorkspaceEdit_RefusesStaleOrigin(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "main.go", "var foo int\n")
	tab := wsOpenTab(t, a, path)
	req := a.captureWSRequest(tab)

	tab.InsertRune('x') // the buffer moves while the server is thinking

	if _, err := a.planWorkspaceEdit(wsEdit(path, 0, 4, 7, "bar"), "Rename", req); err == nil {
		t.Fatal("a stale origin was accepted")
	} else if !strings.Contains(err.Error(), "changed while the server was thinking") {
		t.Errorf("error = %v, want it to explain the staleness", err)
	}
}

// TestPlanWorkspaceEdit_RefusesUnsyncedParticipant generalises the staleness
// check to a file the request never mentioned: an open tab whose buffer is
// not the text the server has cannot be edited by the server's coordinates.
func TestPlanWorkspaceEdit_RefusesUnsyncedParticipant(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	origin := wsTestFile(t, a, "origin.go", "var foo int\n")
	other := wsTestFile(t, a, "other.go", "var foo int\n")
	originTab := wsOpenTab(t, a, origin)
	otherTab := wsOpenTab(t, a, other)
	req := a.captureWSRequest(originTab)

	// The second file drifts out of sync without the origin moving.
	otherTab.InsertRune('x')

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(other, 0, 4, 7, "bar").Documents[0],
	}}
	if _, err := a.planWorkspaceEdit(we, "Rename", req); err == nil {
		t.Fatal("an unsynced participant was accepted")
	} else if !strings.Contains(err.Error(), "other.go") {
		t.Errorf("error = %v, want it to name other.go", err)
	}
}

// TestPlanWorkspaceEdit_RefusesOutsideRoot pins the confinement rule. A
// language server's workspace legitimately spans the module cache and
// symlinked sibling repos, and it will happily hand back a rename touching
// them.
func TestPlanWorkspaceEdit_RefusesOutsideRoot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	if err := os.WriteFile(outside, []byte("var foo int\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := a.planWorkspaceEdit(wsEdit(outside, 0, 4, 7, "bar"), "Rename", wsRequest{})
	if err == nil {
		t.Fatal("an edit outside the project root was accepted")
	}
	if !strings.Contains(err.Error(), "outside the project root") {
		t.Errorf("error = %v, want it to say the path is outside the root", err)
	}
}

// TestPlanWorkspaceEdit_RefusesSymlinkEscape pins the sharper half of the
// same rule. writeFileAtomic resolves symlinks BEFORE writing, so a link
// inside the root pointing outside it would pass a purely textual check and
// then be written through.
func TestPlanWorkspaceEdit_RefusesSymlinkEscape(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "target.go")
	if err := os.WriteFile(outside, []byte("var foo int\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(a.rootDir, "link.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := a.planWorkspaceEdit(wsEdit(link, 0, 4, 7, "bar"), "Rename", wsRequest{})
	if err == nil {
		t.Fatal("a symlink escaping the root was accepted")
	}
	if !strings.Contains(err.Error(), "outside the project root") {
		t.Errorf("error = %v, want it to say the path escapes the root", err)
	}
}

// TestPlanWorkspaceEdit_RefusesBinaryFile pins that the open guards apply
// here too: a file ced would refuse to open has no ranges worth applying.
func TestPlanWorkspaceEdit_RefusesBinaryFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "blob.bin", "abc\x00def\n")
	_, err := a.planWorkspaceEdit(wsEdit(path, 0, 0, 1, "x"), "Rename", wsRequest{})
	if err == nil {
		t.Fatal("a binary file was accepted")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error = %v, want it to name the binary guard", err)
	}
}

// TestPlanWorkspaceEdit_RefusesMissingFile pins that a path the server named
// but which no longer exists is refused rather than resurrected. NewTab
// deliberately succeeds on a missing path — that is the "ced foo.go" new-file
// intent, and it is wrong here.
func TestPlanWorkspaceEdit_RefusesMissingFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	gone := filepath.Join(a.rootDir, "gone.go")
	_, err := a.planWorkspaceEdit(wsEdit(gone, 0, 0, 1, "x"), "Rename", wsRequest{})
	if err == nil {
		t.Fatal("an edit naming a nonexistent file was accepted")
	}
}

// TestPlanWorkspaceEdit_RefusesOverlappingEdits pins that a server breaking
// the spec's no-overlap rule gets a refusal. Applied bottom-up they would not
// fail — they would produce plausible-looking garbage.
func TestPlanWorkspaceEdit_RefusesOverlappingEdits(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "main.go", "abcdefghij\n")
	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{{
		Path: path, URI: lsp.PathToURI(path),
		Edits: []lsp.TextEdit{
			{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 5}}, NewText: "x"},
			{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 3}, End: lsp.Position{Line: 0, Character: 8}}, NewText: "y"},
		},
	}}}
	_, err := a.planWorkspaceEdit(we, "Rename", wsRequest{})
	if err == nil {
		t.Fatal("overlapping edits were accepted")
	}
	if !strings.Contains(err.Error(), "overlapping") {
		t.Errorf("error = %v, want it to name the overlap", err)
	}
}

// TestApplyServerEdit_RefusesResourceOpsByName pins that a file create /
// rename / delete is refused with its kind named, never silently skipped.
// Applying the text edits of a package rename while dropping the file move
// leaves a tree that no longer builds, with nothing to say why.
func TestApplyServerEdit_RefusesResourceOpsByName(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "main.go", "var foo int\n")
	we := wsEdit(path, 0, 4, 7, "bar")
	we.Resources = []lsp.ResourceOp{
		{Kind: lsp.ResourceRename, Path: "/a.go", NewPath: "/b.go"},
		{Kind: lsp.ResourceRename, Path: "/c.go", NewPath: "/d.go"},
	}
	if a.applyServerEdit(we, "Rename foo → bar", wsRequest{}) {
		t.Fatal("an edit carrying resource operations was applied")
	}
	if !strings.Contains(a.statusMsg, "2 rename files") {
		t.Errorf("flash = %q, want it to name the two file renames", a.statusMsg)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "foo") {
		t.Error("the text edits were applied despite the refusal")
	}
}

// TestApplyServerEdit_ReportsInTheFindAllPanel pins the receipt. Since the
// apply opens no tabs, the list is the only thing showing what changed —
// which is why it reuses the panel every other cross-file result lands in.
func TestApplyServerEdit_ReportsInTheFindAllPanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	one := wsTestFile(t, a, "one.go", "var foo int\n")
	tab := wsOpenTab(t, a, one)

	a.applyServerEdit(wsEdit(one, 0, 4, 7, "bar"), "Rename foo → bar", a.captureWSRequest(tab))

	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("modal = %T, want *findAllModal", a.modal)
	}
	if !m.project {
		t.Error("the receipt is not in project mode — its rows carry paths")
	}
	if m.heading != "Changed by" {
		t.Errorf("heading = %q, want %q", m.heading, "Changed by")
	}
	if len(m.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(m.rows))
	}
	if !strings.Contains(m.rows[0].text, "bar") {
		t.Errorf("row text = %q, want the NEW text — the receipt shows what it is now",
			m.rows[0].text)
	}
}

// TestApplyWorkspaceEdit_RollsBackOnAFailedWrite pins the atomicity story.
// Buffer edits go first because they cannot fail; a failed disk write then
// unwinds everything already applied, out of the retained detached tabs.
func TestApplyWorkspaceEdit_RollsBackOnAFailedWrite(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	good := wsTestFile(t, a, "good.go", "var foo int\n")
	openPath := wsTestFile(t, a, "open.go", "var foo int\n")
	tab := wsOpenTab(t, a, openPath)

	// A file inside a directory that will be made unwritable, sorting AFTER
	// good.go so at least one write has already landed when it fails.
	subdir := filepath.Join(a.rootDir, "zsub")
	bad := wsTestFile(t, a, "zsub/bad.go", "var foo int\n")

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(good, 0, 4, 7, "bar").Documents[0],
		wsEdit(openPath, 0, 4, 7, "bar").Documents[0],
		wsEdit(bad, 0, 4, 7, "bar").Documents[0],
	}}
	plan, err := a.planWorkspaceEdit(we, "Rename foo → bar", a.captureWSRequest(tab))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Make the write fail: read-only file inside a read-only directory
	// defeats both the atomic rename and the in-place fallback.
	if err := os.Chmod(bad, 0o444); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	if err := os.Chmod(subdir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subdir, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("running as root — permissions cannot make a write fail")
	}

	if _, err := a.applyWorkspaceEdit(plan); err == nil {
		t.Fatal("a failed write was reported as success")
	}
	if data, _ := os.ReadFile(good); !strings.Contains(string(data), "var foo int") {
		t.Errorf("good.go was not rolled back: %q", data)
	}
	if got := tab.Buffer.Lines[0]; !strings.Contains(got, "var foo int") {
		t.Errorf("the open tab was not rolled back: %q", got)
	}
}

// TestWsForgetTab_DropsTheGroupWhenAParticipantCloses pins that closing a
// participant drops the journal. A closed tab takes its undo stack with it,
// so the group could no longer be unwound as one gesture — and a group that
// silently skipped a file is the half-rename all of this exists to prevent.
func TestWsForgetTab_DropsTheGroupWhenAParticipantCloses(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "main.go", "var foo int\n")
	tab := wsOpenTab(t, a, path)

	a.applyServerEdit(wsEdit(path, 0, 4, 7, "bar"), "Rename foo → bar", a.captureWSRequest(tab))
	a.closeModal()
	if a.wsGroup == nil {
		t.Fatal("no journal was armed")
	}
	a.closeTab(0)
	if a.wsGroup != nil {
		t.Error("the journal survived closing one of its participants")
	}
}

// TestWsEditUndoLabel_NamesTheVerbAndCount pins the ≡ row's label. "Undo
// workspace edit" says nothing about what would change, and this row rewrites
// files that may not be on screen.
func TestWsEditUndoLabel_NamesTheVerbAndCount(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.wsUndoAvailable() {
		t.Error("the undo row is available with no edit applied")
	}
	a.wsGroup = &wsEditGroup{
		label: "Rename foo → bar",
		parts: []wsParticipant{{path: "/a.go"}, {path: "/b.go"}},
	}
	if got, want := a.wsEditUndoLabel(), "Undo Rename foo → bar (2 files)"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
}

// TestApplyServerEdit_EmptyEditSaysNothingToChange pins that an edit asking
// for nothing is reported as such rather than as an error — a legal rename
// that changes nothing is a real answer.
func TestApplyServerEdit_EmptyEditSaysNothingToChange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.applyServerEdit(nil, "Rename", wsRequest{}) {
		t.Error("a nil edit was reported as applied")
	}
	if !strings.Contains(a.statusMsg, "nothing to change") {
		t.Errorf("flash = %q, want it to say there was nothing to change", a.statusMsg)
	}
}

// TestConvertEdits_UTF16ColumnsAgainstTheRealLine pins the coordinate
// conversion. LSP counts UTF-16 code units, the buffer counts runes, and the
// two disagree the moment a line carries a non-BMP character — which is
// exactly when a wrong conversion silently edits the wrong columns.
func TestConvertEdits_UTF16ColumnsAgainstTheRealLine(t *testing.T) {
	// "𝄞" is one rune but TWO UTF-16 code units, so every column after it
	// differs between the two systems.
	buf := editor.NewBuffer("x𝄞foo end")
	edits, err := convertEdits(buf, []lsp.TextEdit{{
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 3}, // after x + surrogate pair
			End:   lsp.Position{Line: 0, Character: 6},
		},
		NewText: "bar",
	}})
	if err != nil {
		t.Fatalf("convertEdits: %v", err)
	}
	if edits[0].Start.Col != 2 || edits[0].End.Col != 5 {
		t.Fatalf("rune cols = %d..%d, want 2..5", edits[0].Start.Col, edits[0].End.Col)
	}
	editor.ApplyEdits(buf, edits)
	if got, want := buf.Lines[0], "x𝄞bar end"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// TestMenuUndoWorkspaceEdit_RefusesAnAlreadyUndoneGroup pins the guard. A
// second undo through the ≡ row would pop a DIFFERENT step out of every
// participant tab — the row's enabled predicate already excludes this, but
// the failure is bad enough to check twice.
func TestMenuUndoWorkspaceEdit_RefusesAnAlreadyUndoneGroup(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := wsTestFile(t, a, "main.go", "var foo int\nvar keep int\n")
	tab := wsOpenTab(t, a, path)
	tab.InsertRune('x') // an ordinary edit BELOW the group's snapshot
	a.lsp.syncedRev[path] = tab.EditRev

	a.applyServerEdit(wsEdit(path, 0, 5, 8, "bar"), "Rename foo → bar", a.captureWSRequest(tab))
	a.closeModal()
	a.menuUndoWorkspaceEdit()
	before := tab.Buffer.String()

	a.menuUndoWorkspaceEdit()
	if got := tab.Buffer.String(); got != before {
		t.Errorf("a second group undo changed the buffer:\n got %q\nwant %q", got, before)
	}
	if !strings.Contains(a.statusMsg, "already undone") {
		t.Errorf("flash = %q, want it to say the group is already undone", a.statusMsg)
	}
	if a.wsUndoAvailable() {
		t.Error("wsUndoAvailable() = true for an undone group")
	}
}

// TestMenuLayout_CarriesTheWorkspaceUndoRow pins that the ≡ Code row exists
// and is gated — it is the only path to the group undo when no participant
// tab is active, which is every edit that went straight to disk.
func TestMenuLayout_CarriesTheWorkspaceUndoRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	found := false
	items, _, _ := a.menuLayout()
	for _, it := range items {
		if it.labelFor == nil {
			continue
		}
		if label := it.labelFor(a); strings.Contains(label, "multi-file edit") {
			found = true
			if it.enabled != nil && it.enabled(a) {
				t.Error("the row is enabled with no multi-file edit applied")
			}
		}
	}
	if !found {
		t.Error("the ≡ Code group has no multi-file undo row")
	}
}
