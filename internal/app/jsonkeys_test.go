// =============================================================================
// File: internal/app/jsonkeys_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// jsonKeysLog is a SYNTHETIC three-record JSON-lines log: sorted keys
// like a logrus formatter writes, one key ("start") only one line has.
const jsonKeysLog = `{"file":"a.go","level":"info","msg":"one","time":"t1"}
{"file":"b.go","level":"warn","msg":"two","start":"s","time":"t2"}
{"file":"c.go","level":"info","msg":"three","time":"t3"}
`

// jsonKeysApp opens content as app.log and the dialog on it, returning
// the app, the tab and the dialog.
func jsonKeysApp(t *testing.T, content string) (*App, *editor.Tab, *jsonKeysModal) {
	t.Helper()
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "app.log", content)
	a := newTestApp(t, root)
	a.openFile(p)
	a.menuReformatJSON()
	m, ok := a.modal.(*jsonKeysModal)
	if !ok {
		t.Fatalf("modal = %T, want the Reformat JSON dialog (flash %q)", a.modal, a.statusMsg)
	}
	return a, a.activeTabPtr(), m
}

// jsonKeysFrame draws a frame and flushes it to the simulation screen,
// so screenText reads what a user would see.
func jsonKeysFrame(a *App) {
	a.draw()
	a.screen.Show()
}

// jsonKeysAlt sends Alt+r through the real key router.
func jsonKeysAlt(a *App, r rune) {
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt))
}

// jsonKeysTick sets the ticks to exactly names, the way a user would:
// Select none, then Space on each named key.
func jsonKeysTick(t *testing.T, a *App, m *jsonKeysModal, names ...string) {
	t.Helper()
	if m.allVisibleKept() {
		jsonKeysAlt(a, 'a')
	}
	for _, n := range names {
		found := false
		for i, k := range m.recs.Keys {
			if k.Name == n {
				m.cursor = i
				a.handleKey(keyEvent(tcell.KeyRune, ' '))
				found = true
			}
		}
		if !found {
			t.Fatalf("no key %q in %v", n, m.recs.Keys)
		}
	}
}

// TestMenuReformatJSON_RowLivesInFile pins the ≡ door: a File row, live
// on any text tab (the dialog refuses non-JSON with a reason), dimmed
// with no tab at all.
func TestMenuReformatJSON_RowLivesInFile(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "notes.txt", "plain\n")
	a := newTestApp(t, root)
	row := menuItemByLabel(t, a, "Reformat JSON (pick keys)…")
	if row.enabled(a) {
		t.Fatal("row should be dimmed with no tab open")
	}
	a.openFile(p)
	if !row.enabled(a) {
		t.Fatal("row should be live on a text tab")
	}
	var inFile bool
	for _, g := range builtinMenuGroups() {
		for _, it := range g.items {
			if it.label == "Reformat JSON (pick keys)…" {
				inFile = g.title == "File"
			}
		}
	}
	if !inFile {
		t.Fatal("the row belongs in ≡ File, beside Format / Validate")
	}
}

// TestOpenJSONKeys_ListsEveryKeyTicked: the dialog lists the union of
// the records' keys in first-seen order with their counts, all ticked —
// opening and applying unchanged would change nothing.
func TestOpenJSONKeys_ListsEveryKeyTicked(t *testing.T) {
	_, _, m := jsonKeysApp(t, jsonKeysLog)
	var names []string
	for _, k := range m.recs.Keys {
		names = append(names, k.Name)
		if !m.keep[k.Name] {
			t.Errorf("%q should start ticked", k.Name)
		}
	}
	if got := strings.Join(names, ","); got != "file,level,msg,time,start" {
		t.Fatalf("keys = %s", got)
	}
	if m.recs.Keys[4].Count != 1 || m.recs.Keys[0].Count != 3 {
		t.Fatalf("counts = %v", m.recs.Keys)
	}
	if m.selection || m.scope != "app.log" {
		t.Fatalf("scope = %q selection %v, want the whole file", m.scope, m.selection)
	}
}

// TestOpenJSONKeys_RefusesTextThatIsNotJSON: no dialog, and the flash
// says why — for a broken document, where.
func TestOpenJSONKeys_RefusesTextThatIsNotJSON(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"just some prose\n", "no JSON objects"},
		{"{\n  \"a\": 1,\n}\n", "line 2, col 9"},
	} {
		root := t.TempDir()
		a := newTestApp(t, root)
		a.openFile(writeStatusTestFile(t, root, "x.json", c.text))
		a.menuReformatJSON()
		if a.modal != nil {
			t.Fatalf("%q: no dialog expected, got %T", c.text, a.modal)
		}
		if !strings.Contains(a.statusMsg, "Can't reformat this file") || !strings.Contains(a.statusMsg, c.want) {
			t.Errorf("%q: flash = %q, want it to mention %q", c.text, a.statusMsg, c.want)
		}
	}
	a := newTestApp(t, t.TempDir())
	a.menuReformatJSON()
	if a.modal != nil || !strings.Contains(a.statusMsg, "Open a JSON file") {
		t.Fatalf("no tab: modal %T flash %q", a.modal, a.statusMsg)
	}
}

// TestJSONKeys_EnterAppliesAsOneUndoStep is the feature end to end:
// keep time + msg, Enter rewrites the buffer (dirty, unsaved), the
// flash counts, and ONE Undo puts the log back.
func TestJSONKeys_EnterAppliesAsOneUndoStep(t *testing.T) {
	a, tab, m := jsonKeysApp(t, jsonKeysLog)
	jsonKeysTick(t, a, m, "time", "msg")
	a.handleKey(keyEvent(tcell.KeyEnter, 0))
	if a.modal != nil {
		t.Fatalf("Apply should close the dialog, still %T", a.modal)
	}
	want := `{"msg":"one","time":"t1"}
{"msg":"two","time":"t2"}
{"msg":"three","time":"t3"}
`
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer =\n%s\nwant\n%s", got, want)
	}
	if !tab.Dirty {
		t.Fatal("an applied reformat is an unsaved edit")
	}
	if !strings.Contains(a.statusMsg, "Reformatted 3 records, keeping 2 of 5 keys") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != jsonKeysLog {
		t.Fatalf("one Undo should restore the log, got\n%s", got)
	}
}

// TestJSONKeys_ApplyToSelectionTouchesOnlyItAndReselects: with a
// selection the dialog reads and rewrites just that, says so on its
// button, and leaves the result selected so it can be reopened on it.
func TestJSONKeys_ApplyToSelectionTouchesOnlyItAndReselects(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.openFile(writeStatusTestFile(t, root, "app.log", jsonKeysLog))
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 1}, false)
	tab.MoveCursorTo(editor.Position{Line: 2}, true) // the whole second line, newline included
	a.menuReformatJSON()
	m, ok := a.modal.(*jsonKeysModal)
	if !ok {
		t.Fatalf("modal = %T (flash %q)", a.modal, a.statusMsg)
	}
	if !m.selection || m.applyLabel() != "[ Apply to selection ]" || !strings.HasPrefix(m.scope, "selection") {
		t.Fatalf("selection %v label %q scope %q", m.selection, m.applyLabel(), m.scope)
	}
	jsonKeysTick(t, a, m, "start")
	m.apply(a)
	lines := strings.Split(tab.Buffer.String(), "\n")
	if lines[0] != strings.Split(jsonKeysLog, "\n")[0] || lines[1] != `{"start":"s"}` || lines[2] != strings.Split(jsonKeysLog, "\n")[2] {
		t.Fatalf("only line 2 should change:\n%s", tab.Buffer.String())
	}
	if got := tab.SelectionText(); got != "{\"start\":\"s\"}\n" {
		t.Fatalf("selection after apply = %q, want the reformatted text", got)
	}
}

// TestJSONKeys_ApplyRefusesWhenTheTextMoved pins the generation check:
// a buffer that changed under the open dialog (a reload, an edit) is
// never overwritten with a reformat of the text it used to hold.
func TestJSONKeys_ApplyRefusesWhenTheTextMoved(t *testing.T) {
	a, tab, m := jsonKeysApp(t, jsonKeysLog)
	jsonKeysTick(t, a, m, "msg")
	tab.MoveCursorTo(tab.Buffer.EndPos(), false)
	tab.InsertString("{\"msg\":\"four\"}\n")
	moved := tab.Buffer.String()
	m.apply(a)
	if tab.Buffer.String() != moved {
		t.Fatal("Apply must not write over text the dialog never read")
	}
	if a.modal != nil || !strings.Contains(a.statusMsg, "changed while the dialog was open") {
		t.Fatalf("modal %T flash %q", a.modal, a.statusMsg)
	}
}

// TestJSONKeys_ApplyUnchangedSaysSo: every key ticked, layout as found
// — Apply makes no edit (no undo step, no dirty) and says why.
func TestJSONKeys_ApplyUnchangedSaysSo(t *testing.T) {
	a, tab, m := jsonKeysApp(t, jsonKeysLog)
	depth := tab.UndoDepth()
	m.apply(a)
	if tab.Dirty || tab.UndoDepth() != depth {
		t.Fatalf("an unchanged apply should not edit (dirty %v, undo %d→%d)", tab.Dirty, depth, tab.UndoDepth())
	}
	if !strings.Contains(a.statusMsg, "Nothing to change") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestJSONKeys_PreviewFollowsTheTicks: the drawn preview is the
// projection — a dropped key leaves the screen on the next frame.
func TestJSONKeys_PreviewFollowsTheTicks(t *testing.T) {
	a, _, m := jsonKeysApp(t, jsonKeysLog)
	jsonKeysFrame(a)
	if s := screenText(a); !strings.Contains(s, `"file":"a.go"`) || !strings.Contains(s, "Reformat JSON · app.log") {
		t.Fatalf("first frame should preview the whole log:\n%s", s)
	}
	jsonKeysTick(t, a, m, "msg", "level")
	jsonKeysFrame(a)
	s := screenText(a)
	if strings.Contains(s, `"file"`) || !strings.Contains(s, `{"level":"info","msg":"one"}`) {
		t.Fatalf("preview should show only level + msg:\n%s", s)
	}
	if !strings.Contains(s, "2 of 5 keys kept") {
		t.Fatalf("summary should count the ticks:\n%s", s)
	}
}

// TestJSONKeys_OptionsChangeTheLayout: alt+p pretty-prints each record;
// alt+e hides the records a selection emptied (here, every line but the
// one carrying "start").
func TestJSONKeys_OptionsChangeTheLayout(t *testing.T) {
	a, _, m := jsonKeysApp(t, jsonKeysLog)
	jsonKeysTick(t, a, m, "start")
	if got := string(m.output()); got != "{}\n{\"start\":\"s\"}\n{}\n" {
		t.Fatalf("plain = %q", got)
	}
	jsonKeysAlt(a, 'e')
	if got := string(m.output()); got != "{\"start\":\"s\"}\n" {
		t.Fatalf("hide empty = %q", got)
	}
	jsonKeysAlt(a, 'p')
	if got := string(m.output()); got != "{\n  \"start\": \"s\"\n}\n" {
		t.Fatalf("pretty = %q", got)
	}
	if s := m.summaryText(); !strings.Contains(s, "2 emptied records hidden") {
		t.Fatalf("summary = %q", s)
	}
}

// TestJSONKeys_FilterNarrowsAndBulkActsOnVisible: typing filters the
// list; Select none touches only what the filter shows; Esc clears the
// filter first and closes on the second press.
func TestJSONKeys_FilterNarrowsAndBulkActsOnVisible(t *testing.T) {
	a, _, m := jsonKeysApp(t, jsonKeysLog)
	for _, r := range "ti" {
		a.handleKey(keyEvent(tcell.KeyRune, r))
	}
	if len(m.vis()) != 1 || m.recs.Keys[m.vis()[0]].Name != "time" {
		t.Fatalf("filter ti should leave only time, got %v", m.vis())
	}
	jsonKeysAlt(a, 'a') // all visible are ticked → Select none
	if m.keep["time"] || !m.keep["msg"] || !m.keep["file"] {
		t.Fatalf("Select none under a filter should untick only time: %v", m.keep)
	}
	a.handleKey(keyEvent(tcell.KeyEsc, 0))
	if m.filter != "" || a.modal == nil {
		t.Fatalf("first Esc should clear the filter (filter %q, modal %T)", m.filter, a.modal)
	}
	jsonKeysAlt(a, 'i') // invert the full list now
	if !m.keep["time"] || m.keep["msg"] {
		t.Fatalf("Invert should flip every visible key: %v", m.keep)
	}
	a.handleKey(keyEvent(tcell.KeyEsc, 0))
	if a.modal != nil {
		t.Fatal("second Esc should close the dialog")
	}
}

// TestJSONKeys_CopyLeavesBufferAndDialog: alt+c copies the projection to
// both clipboards and changes nothing else.
func TestJSONKeys_CopyLeavesBufferAndDialog(t *testing.T) {
	a, tab, m := jsonKeysApp(t, jsonKeysLog)
	var copied string
	jsonKeysCopyFn = func(s string) error { copied = s; return nil }
	jsonKeysTick(t, a, m, "msg")
	jsonKeysAlt(a, 'c')
	want := "{\"msg\":\"one\"}\n{\"msg\":\"two\"}\n{\"msg\":\"three\"}\n"
	if copied != want || a.clipBuf != want {
		t.Fatalf("copied %q / clipBuf %q, want %q", copied, a.clipBuf, want)
	}
	if a.modal != m || tab.Buffer.String() != jsonKeysLog || tab.Dirty {
		t.Fatal("Copy must leave the dialog up and the buffer alone")
	}
	if !strings.Contains(a.statusMsg, "Copied 3 records") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestJSONKeys_MouseTicksBoxesAndPressesButtons: a press on a key's box
// ticks it, a press on the row only highlights (a second press ticks),
// and the option and Apply buttons answer clicks.
func TestJSONKeys_MouseTicksBoxesAndPressesButtons(t *testing.T) {
	a, tab, m := jsonKeysApp(t, jsonKeysLog)
	click := func(x, y int) {
		a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
		a.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	}
	g := m.geom(a)
	click(g.listX+2, g.top) // "file"'s box
	if m.keep["file"] {
		t.Fatal("a press on the box should untick file")
	}
	click(g.listX+8, g.top+1) // "level"'s name: highlight only
	if m.cursor != 1 || !m.keep["level"] {
		t.Fatalf("a press on the name should highlight (cursor %d) without ticking", m.cursor)
	}
	click(g.pretty.x, g.pretty.y)
	if !m.pretty {
		t.Fatal("pretty-print checkbox should answer a click")
	}
	click(g.pretty.x, g.pretty.y) // back off
	g = m.geom(a)
	click(g.apply.x+1, g.apply.y)
	if a.modal != nil || strings.Contains(tab.Buffer.String(), `"file"`) {
		t.Fatalf("Apply click should rewrite without file:\n%s", tab.Buffer.String())
	}
}

// TestJSONKeys_PressOutsideDismisses: the modal rule — a press outside
// the dialog closes it, nothing applied.
func TestJSONKeys_PressOutsideDismisses(t *testing.T) {
	a, tab, m := jsonKeysApp(t, jsonKeysLog)
	jsonKeysTick(t, a, m, "msg")
	g := m.geom(a)
	a.handleMouse(tcell.NewEventMouse(g.x+g.w, g.y+g.h, tcell.Button1, tcell.ModNone))
	if a.modal != nil || tab.Buffer.String() != jsonKeysLog {
		t.Fatal("a press outside should cancel")
	}
}

// TestJSONKeysPreviewRows_WrapWithHangingIndentOnByteOffsets: a long
// line wraps at the width, continuations narrower by the hanging
// indent, and every row's byte span lands on rune boundaries even with
// multi-byte text.
func TestJSONKeysPreviewRows_WrapWithHangingIndentOnByteOffsets(t *testing.T) {
	m := &jsonKeysModal{built: true, lines: []string{"ééééééééééééé", "short"}}
	rows := m.previewRows(6)
	// 13 runes at width 6, then 4 per continuation: 6 + 4 + 3 → 3 rows.
	if len(rows) != 4 {
		t.Fatalf("rows = %+v, want 3 for the long line + 1", rows)
	}
	if rows[0].cont || !rows[1].cont || !rows[2].cont || rows[3].cont {
		t.Fatalf("continuation flags wrong: %+v", rows)
	}
	line := m.lines[0]
	if got := line[rows[1].b0:rows[1].b1]; got != "éééé" || rows[1].r0 != 6 {
		t.Fatalf("second row = %q (r0 %d), want 4 runes from rune 6", got, rows[1].r0)
	}
	if got := line[rows[2].b0:rows[2].b1]; got != "ééé" {
		t.Fatalf("last row = %q", got)
	}
	if m.previewRows(6)[0] != rows[0] || m.rowsW != 6 {
		t.Fatal("rows should be cached per width")
	}
}

// TestJSONKeys_PreviewScrollsWithAltArrowsAndWheel: Alt+↓ and the wheel
// over the preview move it; the bare arrow walks the key list instead.
func TestJSONKeys_PreviewScrollsWithAltArrowsAndWheel(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(`{"n":` + itoa(i) + `,"msg":"x"}` + "\n")
	}
	a, _, m := jsonKeysApp(t, b.String())
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModAlt))
	if m.pscroll != 1 || m.cursor != 0 {
		t.Fatalf("alt+↓: pscroll %d cursor %d, want 1 / 0", m.pscroll, m.cursor)
	}
	a.handleKey(keyEvent(tcell.KeyDown, 0))
	if m.pscroll != 1 || m.cursor != 1 {
		t.Fatalf("↓: pscroll %d cursor %d, want 1 / 1", m.pscroll, m.cursor)
	}
	g := m.geom(a)
	a.handleMouse(tcell.NewEventMouse(g.prevX+1, g.top+1, tcell.WheelDown, tcell.ModNone))
	if m.pscroll != 1+wheelLines {
		t.Fatalf("wheel over the preview: pscroll %d", m.pscroll)
	}
	jsonKeysFrame(a)
	if s := screenText(a); !strings.Contains(s, "lines 5–") {
		t.Fatalf("summary should say where the preview is:\n%s", s)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModAlt))
	rows := m.previewRows(m.geom(a).previewWidth())
	if m.pscroll != len(rows)-m.geom(a).rows {
		t.Fatalf("alt+End: pscroll %d, want the last page", m.pscroll)
	}
}

// TestJSONKeys_EmptyObjectHasNoKeys: `{}` is a record with no keys (the
// fuzz find in the format package) — the dialog opens, draws "(no
// keys)", and every key verb is a harmless no-op.
func TestJSONKeys_EmptyObjectHasNoKeys(t *testing.T) {
	a, _, m := jsonKeysApp(t, "{}\n")
	if m.cursor != -1 {
		t.Fatalf("cursor = %d, want -1", m.cursor)
	}
	a.handleKey(keyEvent(tcell.KeyRune, ' '))
	a.handleKey(keyEvent(tcell.KeyDown, 0))
	jsonKeysAlt(a, 'a')
	jsonKeysAlt(a, 'i')
	jsonKeysFrame(a)
	if !strings.Contains(screenText(a), "(no keys)") {
		t.Fatal("an empty key list should say so")
	}
}

// TestJSONKeys_AltChordsAreUnique is a smoke test of the table: five
// chords, none bound twice (TestAltChords_NoSurfaceBindsAKeyTwice also
// covers it through the README fixture).
func TestJSONKeys_AltChordsAreUnique(t *testing.T) {
	seen := map[rune]bool{}
	for _, c := range (&jsonKeysModal{}).altChords(nil) {
		if seen[c.key] {
			t.Fatalf("alt+%c bound twice", c.key)
		}
		seen[c.key] = true
	}
	if len(seen) != 5 {
		t.Fatalf("%d chords, want 5", len(seen))
	}
}

// TestLooksLikeJSONLine: JSON lines of either layout are coloured; a
// log's passenger lines are not.
func TestLooksLikeJSONLine(t *testing.T) {
	for l, want := range map[string]bool{
		`{"a":1}`: true, `  "a": 1,`: true, `  ]`: true, `  -1,`: true, `  42`: true,
		"panic: boom": false, "\tgoroutine 1": false, "": false, "   ": false,
	} {
		if got := looksLikeJSONLine(l); got != want {
			t.Errorf("looksLikeJSONLine(%q) = %v, want %v", l, got, want)
		}
	}
}

// TestDisplayJSONKey: control runes and an empty name are made
// paintable; ordinary names pass through.
func TestDisplayJSONKey(t *testing.T) {
	for in, want := range map[string]string{"msg": "msg", "": `""`, "a\nb": "a▯b", "a\tb": "a b"} {
		if got := displayJSONKey(in); got != want {
			t.Errorf("displayJSONKey(%q) = %q, want %q", in, got, want)
		}
	}
}
