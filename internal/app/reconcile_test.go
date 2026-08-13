// =============================================================================
// File: internal/app/reconcile_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the disk/buffer conflict machinery: the watcher matrix, the
// save guard, the four-choice prompt and its resolutions, the deferral
// rule, and the tab-strip marker. The through-line every case is really
// checking is one sentence — no path may write over bytes the user has
// not been asked about.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// reconcileFixture builds an app with one open tab backed by a real
// file — the starting state of every scenario below.
func reconcileFixture(t *testing.T) (*App, *editor.Tab, string) {
	t.Helper()
	root := t.TempDir()
	a := newTestApp(t, root)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab := openTabAtPath(t, a, path)
	return a, tab, path
}

// externalWrite simulates another tool writing the file. The mtime is
// pushed a definite distance into the future rather than left to the
// clock: on a coarse-grained filesystem a same-second rewrite is
// indistinguishable from our own, and the whole mechanism keys off
// "is disk newer?". minutesAhead lets a test stage two successive
// external writes and have the second read as genuinely newer news.
func externalWrite(t *testing.T, path, body string, minutesAhead int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	when := time.Now().Add(time.Duration(minutesAhead) * time.Minute)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

// conflictOf returns the open conflict prompt, or nil when the modal
// slot holds something else.
func conflictOf(a *App) *conflictModal {
	m, _ := a.modal.(*conflictModal)
	return m
}

// -----------------------------------------------------------------------------
// A. The watcher matrix
// -----------------------------------------------------------------------------

// TestReconcile_CleanTabTakesDiskUndoably covers the first row of the
// matrix: with nothing of the user's at stake the disk copy is strictly
// newer information and is taken — but a single Undo must still be able
// to put the old text back, because "the editor changed what I was
// reading" needs a way out even when nothing was unsaved.
func TestReconcile_CleanTabTakesDiskUndoably(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	externalWrite(t, path, "package main // theirs\n", 60)

	a.reconcileOpenTabsWithDisk()

	if got := tab.Buffer.String(); got != "package main // theirs\n" {
		t.Fatalf("clean tab should have reloaded, buffer = %q", got)
	}
	if a.conflictFor(tab) != nil {
		t.Fatal("a clean reload is not a conflict")
	}
	if !tab.Undo() || tab.Buffer.String() != "package main\n" {
		t.Fatalf("the reload should be undoable, buffer = %q", tab.Buffer.String())
	}
}

// TestReconcile_DirtyTabRecordsConflictAndKeepsMtime is the second row,
// and the mtime assertion is the load-bearing one: the pre-Phase-2 code
// adopted the disk mtime here purely to stop the per-tick re-flash,
// which quietly told the save guard "this tab is based on the current
// file" — re-opening the very clobber the guard exists to prevent. The
// conflict record suppresses the repeats now instead.
func TestReconcile_DirtyTabRecordsConflictAndKeepsMtime(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	tab.InsertString("// mine\n")
	loadedAt := tab.Mtime
	externalWrite(t, path, "package main // theirs\n", 60)

	a.reconcileOpenTabsWithDisk()

	if a.conflictFor(tab) == nil {
		t.Fatal("a dirty tab whose file changed must record a conflict")
	}
	if !strings.Contains(tab.Buffer.String(), "// mine") {
		t.Fatalf("the buffer must be left alone, got %q", tab.Buffer.String())
	}
	if !tab.Mtime.Equal(loadedAt) {
		t.Fatal("the tab must not adopt the disk mtime — that would disarm the save guard")
	}
	if a.modal != nil {
		t.Fatal("the tick itself must not steal the modal slot; conflictAfterEvent raises")
	}
}

// TestReconcile_DeletedFileMarksGoneAndSaveRecreates is the third row:
// the buffer is now the only copy in existence, so it is kept and marked
// — and an explicit save puts the file back rather than being refused as
// a conflict.
func TestReconcile_DeletedFileMarksGoneAndSaveRecreates(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	tab.InsertString("// mine\n")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	a.reconcileOpenTabsWithDisk()

	if !tab.DiskGone || !tab.Dirty {
		t.Fatalf("deleted file: DiskGone=%v Dirty=%v, want both true", tab.DiskGone, tab.Dirty)
	}
	if marker, _, ok := a.tabMarker(tab); !ok || marker != diskGoneMarker {
		t.Fatalf("tab marker = %q (%v), want %q", marker, ok, diskGoneMarker)
	}

	if !a.saveTabAt(0) {
		t.Fatal("saving a deleted file must recreate it, not be blocked as a conflict")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file was not recreated: %v", err)
	}
	if string(got) != tab.Buffer.String() {
		t.Fatalf("recreated file = %q, want the buffer", got)
	}
}

// TestReconcile_RepeatTicksStayQuietUntilNewNews pins the deferral
// contract against the background tick: once the user has said "later",
// further ticks about the SAME state must not re-ask, but a genuinely
// newer external write is new news and re-arms the question.
func TestReconcile_RepeatTicksStayQuietUntilNewNews(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	tab.InsertString("// mine\n")
	externalWrite(t, path, "package main // theirs\n", 60)
	a.reconcileOpenTabsWithDisk()

	c := a.conflictFor(tab)
	if c == nil {
		t.Fatal("expected a conflict record")
	}
	a.openConflictModal(c) // the prompt the user is about to defer
	a.closeModal()
	a.conflictLater(c)

	a.reconcileOpenTabsWithDisk()
	a.conflictAfterEvent()
	if a.modal != nil {
		t.Fatal("a deferred conflict must not re-pop on the next tick")
	}

	externalWrite(t, path, "package main // theirs again\n", 120)
	a.reconcileOpenTabsWithDisk()
	a.conflictAfterEvent()
	if conflictOf(a) == nil {
		t.Fatal("a further external write is new news — the prompt should return")
	}
}

// TestConflictAfterEvent_WaitsForTheTabToBeFrontmost is the other half
// of the deferral rule: a conflict on a background tab is recorded
// silently and never interrupts the file the user is actually looking
// at. It surfaces the moment they turn to it.
func TestConflictAfterEvent_WaitsForTheTabToBeFrontmost(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	tab.InsertString("// mine\n")
	other := filepath.Join(a.rootDir, "other.go")
	if err := os.WriteFile(other, []byte("package other\n"), 0o644); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	openTabAtPath(t, a, other) // becomes the active tab
	externalWrite(t, path, "package main // theirs\n", 60)

	a.reconcileOpenTabsWithDisk()
	a.conflictAfterEvent()
	if a.modal != nil {
		t.Fatal("a background tab's conflict must not interrupt the frontmost file")
	}

	a.switchToTab(0)
	a.conflictAfterEvent()
	if conflictOf(a) == nil {
		t.Fatal("switching to the conflicted tab should raise its prompt")
	}
}

// -----------------------------------------------------------------------------
// B. The save guard
// -----------------------------------------------------------------------------

// guardedSaveFixture drives an app to the exact state the guard exists
// for: unsaved edits in the buffer, a newer copy on disk, and an
// explicit save that has just bounced off the guard.
func guardedSaveFixture(t *testing.T) (*App, *editor.Tab, string) {
	t.Helper()
	a, tab, path := reconcileFixture(t)
	tab.InsertString("// mine\n")
	externalWrite(t, path, "package main // theirs\n", 60)
	if a.saveTabAt(0) {
		t.Fatal("the save guard let a clobbering write through")
	}
	return a, tab, path
}

// TestSaveGuard_AbortsTheWriteAndAsks is the headline promise of the
// phase: ced used to warn about the conflict and write anyway. Now the
// bytes stay put and the user gets the question.
func TestSaveGuard_AbortsTheWriteAndAsks(t *testing.T) {
	a, _, path := guardedSaveFixture(t)

	got, _ := os.ReadFile(path)
	if string(got) != "package main // theirs\n" {
		t.Fatalf("the external edit was clobbered: %q", got)
	}
	m := conflictOf(a)
	if m == nil {
		t.Fatal("a blocked save must raise the conflict prompt")
	}
	if m.c.resume == nil {
		t.Fatal("the blocked save must be armed to resume")
	}
}

// TestConflictKeepMine_CompletesTheBlockedSave closes the loop: the
// user's answer to "your save is blocked" has to finish the save they
// asked for, not merely unblock some future one.
func TestConflictKeepMine_CompletesTheBlockedSave(t *testing.T) {
	a, tab, path := guardedSaveFixture(t)
	m := conflictOf(a)
	m.hover = 1 // [ Keep mine ]

	m.activate(a)

	if a.conflictFor(tab) != nil {
		t.Fatal("keeping mine resolves the conflict")
	}
	got, _ := os.ReadFile(path)
	if string(got) != tab.Buffer.String() {
		t.Fatalf("disk = %q, want the buffer the user chose to keep", got)
	}
	if tab.Dirty {
		t.Fatal("the resumed save should have cleared Dirty")
	}
}

// TestConflictTakeDisk_ReloadsReversibly checks the other direction —
// and that it is reversible, which is the only reason a button that
// throws away unsaved work is safe to put next to three others.
func TestConflictTakeDisk_ReloadsReversibly(t *testing.T) {
	a, tab, _ := guardedSaveFixture(t)
	mine := tab.Buffer.String()
	m := conflictOf(a)
	m.hover = 2 // [ Take disk ]

	m.activate(a)

	if a.conflictFor(tab) != nil {
		t.Fatal("taking the disk copy resolves the conflict")
	}
	if got := tab.Buffer.String(); got != "package main // theirs\n" {
		t.Fatalf("buffer = %q, want the disk copy", got)
	}
	if !tab.Undo() || tab.Buffer.String() != mine {
		t.Fatalf("undo must bring the user's version back, got %q", tab.Buffer.String())
	}
}

// TestConflictLater_LeavesTheGuardArmed pins the deferral: nothing is
// written, the marker stays, and the next explicit save asks again —
// because a save gesture cannot be answered with silence.
func TestConflictLater_LeavesTheGuardArmed(t *testing.T) {
	a, tab, path := guardedSaveFixture(t)
	m := conflictOf(a)
	m.hover = 3 // [ Later ]

	m.activate(a)

	c := a.conflictFor(tab)
	if c == nil {
		t.Fatal("deciding later must keep the conflict record")
	}
	if !c.prompted {
		t.Fatal("deciding later must not re-pop the prompt unprompted")
	}
	if c.resume != nil {
		t.Fatal("the declined save must be dropped, not left armed")
	}
	if marker, _, ok := a.tabMarker(tab); !ok || marker != conflictMarker {
		t.Fatalf("tab marker = %q (%v), want %q", marker, ok, conflictMarker)
	}

	if a.saveTabAt(0) {
		t.Fatal("saves stay blocked while the conflict stands")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "package main // theirs\n" {
		t.Fatalf("disk was written despite an unresolved conflict: %q", got)
	}
	if conflictOf(a) == nil {
		t.Fatal("an explicit save must re-ask even a deferred question")
	}
}

// TestConflictCompare_ShowsBufferAgainstDisk pins the look-before-you-
// choose path: the compare panel opens on the buffer vs. the file as it
// now stands, and the conflict deliberately survives so the other three
// answers are still waiting afterwards.
func TestConflictCompare_ShowsBufferAgainstDisk(t *testing.T) {
	a, tab, _ := guardedSaveFixture(t)
	m := conflictOf(a)
	m.hover = 0 // [ Compare ]

	m.activate(a)

	if !a.compare.open {
		t.Fatal("Compare should open the compare panel")
	}
	if !strings.Contains(a.compare.oldLabel, "(saved)") {
		t.Fatalf("old side = %q, want the on-disk copy", a.compare.oldLabel)
	}
	if a.conflictFor(tab) == nil {
		t.Fatal("looking is not deciding — the conflict must survive Compare")
	}
}

// TestAutoSaveGuard_SuspendsSilently pins the background contract: an
// auto-save that meets a conflict may not write, may not open a modal
// (the user is mid-thought), but must leave the record that marks the
// tab and queues the question.
func TestAutoSaveGuard_SuspendsSilently(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	a.autoSaveEnabled = true
	t.Cleanup(a.stopAutoSave)
	tab.InsertString("// mine\n")
	externalWrite(t, path, "package main // theirs\n", 60)

	a.handleAutoSave()

	if a.modal != nil {
		t.Fatal("a background save must never raise a modal")
	}
	if a.conflictFor(tab) == nil {
		t.Fatal("the suspended auto-save must leave a conflict record")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "package main // theirs\n" {
		t.Fatalf("auto-save clobbered the external edit: %q", got)
	}
	if !tab.Dirty {
		t.Fatal("nothing was written, so the tab is still dirty")
	}

	// …and it stays suspended on later ticks, rather than each tick
	// re-deciding from scratch.
	a.handleAutoSave()
	if got, _ := os.ReadFile(path); string(got) != "package main // theirs\n" {
		t.Fatalf("a later tick wrote anyway: %q", got)
	}
}

// TestFormatOnSave_KeptEditsAreNotAConflict guards against the guard:
// format-on-save rewrites the file itself, and when the user typed
// again while the formatter ran, ced keeps the buffer and leaves the
// formatted bytes on disk. That is ced's own write, so it must not read
// back as a foreign one — otherwise the user's next save would be
// blocked behind a question about something the editor did.
func TestFormatOnSave_KeptEditsAreNotAConflict(t *testing.T) {
	a, tab, path := reconcileFixture(t)
	// The formatter's output lands on disk while the user types on.
	externalWrite(t, path, "package main // formatted\n", 60)
	tab.InsertString("// mine\n")

	a.handleFormatDone(&formatDoneEvent{when: time.Now(), tabPath: path, label: "gofmt"})
	a.reconcileOpenTabsWithDisk()

	if a.conflictFor(tab) != nil {
		t.Fatal("ced's own formatter write must not raise a clobber conflict")
	}
	if !strings.Contains(tab.Buffer.String(), "// mine") {
		t.Fatalf("the user's edits must survive: %q", tab.Buffer.String())
	}
	if !a.saveTabAt(0) {
		t.Fatal("the next save must not be blocked")
	}
}

// TestApplyWorkspaceEdit_RefusesAChangedDetachedFile covers the one
// write path that cannot stop to ask: a multi-file edit whose closed
// participant moved under it aborts the whole group rather than write
// half a rename.
func TestApplyWorkspaceEdit_RefusesAChangedDetachedFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openPath := wsTestFile(t, a, "open.go", "var foo int\n")
	detached := wsTestFile(t, a, "closed.go", "var foo int\n")
	tab := wsOpenTab(t, a, openPath)

	we := &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(openPath, 0, 4, 7, "bar").Documents[0],
		wsEdit(detached, 0, 4, 7, "bar").Documents[0],
	}}
	plan, err := a.planWorkspaceEdit(we, "Rename foo → bar", a.captureWSRequest(tab))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Somebody else writes the closed file between planning and applying.
	externalWrite(t, detached, "var foo int // theirs\n", 60)

	if _, err := a.applyWorkspaceEdit(plan); err == nil {
		t.Fatal("a changed detached file must abort the group")
	}
	got, _ := os.ReadFile(detached)
	if string(got) != "var foo int // theirs\n" {
		t.Fatalf("the external edit was clobbered: %q", got)
	}
	if strings.Contains(tab.Buffer.String(), "bar") {
		t.Fatalf("the open participant should have been rolled back: %q", tab.Buffer.String())
	}
}

// -----------------------------------------------------------------------------
// Marker, teardown, and the prompt's own mechanics
// -----------------------------------------------------------------------------

// TestTabMarker_WorstNewsWins pins the single-slot precedence: the strip
// has one column for status, and it shows the most alarming true thing.
func TestTabMarker_WorstNewsWins(t *testing.T) {
	a, tab, _ := reconcileFixture(t)

	if _, _, ok := a.tabMarker(tab); ok {
		t.Fatal("a clean tab's marker slot stays blank")
	}

	tab.Dirty = true
	if m, _, _ := a.tabMarker(tab); m != '●' {
		t.Fatalf("dirty marker = %q, want ●", m)
	}

	a.noteConflict(tab, time.Now(), nil)
	if m, _, _ := a.tabMarker(tab); m != conflictMarker {
		t.Fatalf("conflict marker = %q, want %q", m, conflictMarker)
	}

	tab.DiskGone = true
	if m, _, _ := a.tabMarker(tab); m != diskGoneMarker {
		t.Fatalf("deleted marker = %q, want %q", m, diskGoneMarker)
	}
}

// TestCloseTab_ForgetsTheConflict guards the bookkeeping: records are
// keyed by tab pointer, so a closed tab's record would otherwise linger
// forever, holding the buffer alive and suspending auto-save for a file
// nobody has open.
func TestCloseTab_ForgetsTheConflict(t *testing.T) {
	a, tab, _ := reconcileFixture(t)
	a.noteConflict(tab, time.Now(), nil)

	a.closeTab(0)

	if len(a.conflicts) != 0 {
		t.Fatalf("closing a tab must drop its conflict record, %d left", len(a.conflicts))
	}
}

// TestConflictModal_ClicksRunTheChoiceUnderThem checks that the drawn
// button row and the hit-test agree — the btnRect discipline, applied
// to a four-way row where an off-by-one lands on "Take disk" instead of
// "Later".
func TestConflictModal_ClicksRunTheChoiceUnderThem(t *testing.T) {
	a, tab, _ := guardedSaveFixture(t)
	m := conflictOf(a)
	rects := m.buttons(a)
	if len(rects) != len(conflictChoices) {
		t.Fatalf("%d button rects for %d choices", len(rects), len(conflictChoices))
	}

	// The last button is [ Later ]: clicking it must defer, not resolve.
	last := rects[len(rects)-1]
	m.handleMouse(a, last.x+1, last.y, tcell.Button1)

	if a.modal != nil {
		t.Fatal("clicking a choice closes the prompt")
	}
	if a.conflictFor(tab) == nil {
		t.Fatal("clicking [ Later ] must leave the conflict standing")
	}
}

// TestConflictModal_DismissalDefers pins Esc and click-away: dismissing
// a question is deferring it, never answering it — neither may write and
// neither may discard.
func TestConflictModal_DismissalDefers(t *testing.T) {
	a, tab, path := guardedSaveFixture(t)
	m := conflictOf(a)

	m.handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if a.modal != nil {
		t.Fatal("Esc closes the prompt")
	}
	if a.conflictFor(tab) == nil {
		t.Fatal("Esc must leave the conflict standing")
	}
	if !strings.Contains(tab.Buffer.String(), "// mine") {
		t.Fatalf("Esc must not touch the buffer: %q", tab.Buffer.String())
	}
	got, _ := os.ReadFile(path)
	if string(got) != "package main // theirs\n" {
		t.Fatalf("Esc must not write: %q", got)
	}
}

// TestConflictModal_DrawsTheChoicesAndTheHint renders the prompt on the
// simulation screen: the four labels have to fit inside the frame, and
// the hovered row's hint has to be the thing that explains the cost of
// pressing it.
func TestConflictModal_DrawsTheChoicesAndTheHint(t *testing.T) {
	a, _, _ := guardedSaveFixture(t)
	m := conflictOf(a)
	m.hover = 2 // [ Take disk ]

	a.draw()
	a.screen.Show()
	content := screenText(a)

	for _, ch := range conflictChoices {
		if !strings.Contains(content, ch.label) {
			t.Fatalf("drawn prompt is missing %q", ch.label)
		}
	}
	if !strings.Contains(content, "main.go changed on disk") {
		t.Fatal("the prompt should name the file")
	}
	if !strings.Contains(content, conflictChoices[2].hint) {
		t.Fatal("the hovered choice's hint should be drawn")
	}
}

// -----------------------------------------------------------------------------
// The marker as a click target — the way back from "Decide later"
// -----------------------------------------------------------------------------

// twoTabConflictFixture stages the case the marker click exists for: a
// conflict raised and deferred on one file while the user is looking at
// another. The second tab is active on return, so a test that ends up on
// the first has been taken there by the click.
func twoTabConflictFixture(t *testing.T) (*App, *editor.Tab, string) {
	t.Helper()
	a, tab, path := guardedSaveFixture(t)
	a.closeModal()
	a.conflictLater(a.conflictFor(tab))

	other := filepath.Join(filepath.Dir(path), "other.go")
	if err := os.WriteFile(other, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	openTabAtPath(t, a, other)
	if a.activeTab != 1 {
		t.Fatalf("fixture should leave the second tab active, activeTab = %d", a.activeTab)
	}
	return a, tab, path
}

// markerCell reports the rune actually painted in tab idx's status slot,
// read back off the simulation screen rather than recomputed — the point
// of the test is that the painter and the hit-test agree about the cell.
func markerCell(t *testing.T, a *App, idx int) rune {
	t.Helper()
	a.draw()
	a.screen.Show()
	r := a.lastTabRects[idx]
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	if r.MarkerX >= w {
		t.Fatalf("marker column %d is off a %d-wide screen", r.MarkerX, w)
	}
	c := cells[r.MarkerX] // row 0: the tab bar.
	if len(c.Runes) == 0 {
		return ' '
	}
	return c.Runes[0]
}

// TestTabMarkerClick_ReRaisesADeferredConflict is the follow-up the phase
// left owed. "Decide later" is an invitation to answer in your own time,
// and before this the only route back to the four choices was to attempt
// a save and be refused — a strange thing to have to do to answer a
// question you were told you could defer.
//
// The focus half is not incidental: every resolution acts on the ACTIVE
// tab (compare re-reads it, Take disk reloads it), so clicking a
// background tab's ⚠ must bring that tab forward before asking.
func TestTabMarkerClick_ReRaisesADeferredConflict(t *testing.T) {
	a, tab, _ := twoTabConflictFixture(t)

	if got := markerCell(t, a, 0); got != conflictMarker {
		t.Fatalf("conflicted tab's slot painted %q, want %q", got, conflictMarker)
	}
	a.tabBarClick(a.lastTabRects[0].MarkerX, 0)

	if conflictOf(a) == nil {
		t.Fatalf("clicking ⚠ must re-raise the prompt, modal = %T", a.modal)
	}
	if a.activeTab != 0 {
		t.Fatalf("the conflicted tab must come forward, activeTab = %d", a.activeTab)
	}
	if a.conflictFor(tab) == nil {
		t.Fatal("re-raising resolves nothing on its own")
	}
}

// TestTabMarkerClick_DirtyDotIsNotAButton keeps the slot's other two
// tenants inert. ⊘ and ● have no question behind them — a deleted file is
// answered by saving and an unsaved buffer by the save verb — so a click
// there is just a click on the tab. A marker that wrote to disk would be
// the one destructive cell in the strip, and it sits one column from the
// filename.
func TestTabMarkerClick_DirtyDotIsNotAButton(t *testing.T) {
	a, tab, _ := twoTabConflictFixture(t)
	a.clearConflict(tab)
	tab.Dirty = true

	if got := markerCell(t, a, 0); got != '●' {
		t.Fatalf("dirty tab's slot painted %q, want ●", got)
	}
	a.tabBarClick(a.lastTabRects[0].MarkerX, 0)

	if a.modal != nil {
		t.Fatalf("a dirty dot has nothing to ask, got modal %T", a.modal)
	}
	if a.activeTab != 0 {
		t.Fatalf("the click should fall through to a tab switch, activeTab = %d", a.activeTab)
	}
	if !tab.Dirty {
		t.Fatal("clicking the dot must not have saved")
	}
}

// TestTabMarkerClick_MissesTheFilename pins the geometry the whole
// gesture rests on: the slot is one cell wide, and the cell after it
// belongs to the name. An off-by-one here would open the prompt when the
// user meant to switch tabs — or, worse, quietly not open it when they
// aimed at the marker.
func TestTabMarkerClick_MissesTheFilename(t *testing.T) {
	a, _, _ := twoTabConflictFixture(t)
	markerCell(t, a, 0) // populate lastTabRects via a real draw.
	r := a.lastTabRects[0]

	a.tabBarClick(r.MarkerX+1, 0)

	if a.modal != nil {
		t.Fatalf("the cell past the marker is the tab, not the question (modal %T)", a.modal)
	}
	if a.activeTab != 0 {
		t.Fatalf("it should still switch tabs, activeTab = %d", a.activeTab)
	}
}
