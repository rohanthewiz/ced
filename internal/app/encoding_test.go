// =============================================================================
// File: internal/app/encoding_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// latin1 is a Windows-1252 file's bytes: "café" then "keep".
const latin1 = "caf\xe9\nkeep\n"

// openLatin1 writes latin1 into a fresh project and opens it.
func openLatin1(t *testing.T) (*App, *editor.Tab, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "fr.txt")
	if err := os.WriteFile(path, []byte(latin1), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil || tab.Encoding != editor.EncodingCP1252 {
		t.Fatalf("fixture did not open as cp1252: %+v", tab)
	}
	return a, tab, path
}

// typeArrow puts a character Windows-1252 can't hold at the end of line 1.
func typeArrow(tab *editor.Tab) {
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 4}, false)
	tab.InsertString(" →")
}

// statusText joins the left status segments the way the bar draws them.
func statusText(a *App) string {
	a.statusUntil = time.Time{}
	var b strings.Builder
	for _, s := range a.statusLeftSegments() {
		b.WriteString(s.text)
	}
	return b.String()
}

// TestSaveTabAt_UnencodableAsksThenConvertsAndSaves pins the explicit
// save's door: the refusal becomes the conversion question, the disk is
// untouched until the answer, and Yes writes UTF-8 and leaves the tab
// clean.
func TestSaveTabAt_UnencodableAsksThenConvertsAndSaves(t *testing.T) {
	a, tab, path := openLatin1(t)
	typeArrow(tab)
	if a.saveTabAt(a.activeTab) {
		t.Fatal("saveTabAt reported success for a refused save")
	}
	m := confirmOf(a)
	if m == nil || m.title != "Save as UTF-8?" {
		t.Fatalf("modal = %#v, want the conversion question", a.modal)
	}
	if body := strings.Join(m.lines, "\n"); !strings.Contains(body, "Line 2 has '→' (U+2192)") {
		t.Fatalf("question does not name the character and line:\n%s", body)
	}
	if disk, _ := os.ReadFile(path); string(disk) != latin1 {
		t.Fatalf("the question wrote the file: %q", disk)
	}
	a.closeModal()
	m.callback(a) // Yes
	if disk, _ := os.ReadFile(path); string(disk) != "café\nkeep →\n" {
		t.Fatalf("disk after Yes = %q", disk)
	}
	if tab.Dirty || tab.Encoding != "" {
		t.Fatalf("after Yes: dirty %v encoding %q", tab.Dirty, tab.Encoding)
	}
}

// TestSaveTabAt_UnencodableNoKeepsTheFile pins No: nothing written, the
// tab still dirty and still Windows-1252.
func TestSaveTabAt_UnencodableNoKeepsTheFile(t *testing.T) {
	a, tab, path := openLatin1(t)
	typeArrow(tab)
	a.saveTabAt(a.activeTab)
	if confirmOf(a) == nil {
		t.Fatal("no question asked")
	}
	a.closeModal() // No
	if disk, _ := os.ReadFile(path); string(disk) != latin1 {
		t.Fatalf("disk = %q", disk)
	}
	if !tab.Dirty || tab.Encoding != editor.EncodingCP1252 {
		t.Fatalf("after No: dirty %v encoding %q", tab.Dirty, tab.Encoding)
	}
}

// TestMenuSaveAndClose_UnencodableYesFinishesTheGesture pins that the
// answer resumes the user's whole gesture: saved as UTF-8 AND closed.
func TestMenuSaveAndClose_UnencodableYesFinishesTheGesture(t *testing.T) {
	a, tab, path := openLatin1(t)
	typeArrow(tab)
	a.menuSaveAndClose()
	m := confirmOf(a)
	if m == nil {
		t.Fatal("no question asked")
	}
	a.closeModal()
	m.callback(a)
	if disk, _ := os.ReadFile(path); string(disk) != "café\nkeep →\n" {
		t.Fatalf("disk = %q", disk)
	}
	if a.tabIndexOf(tab) >= 0 {
		t.Fatal("tab still open after Save & close")
	}
}

// TestAutoSave_UnencodableSkipsSilently pins the background door: no
// write, no dialog, no flash — the red status badge is the signal.
func TestAutoSave_UnencodableSkipsSilently(t *testing.T) {
	a, tab, path := openLatin1(t)
	a.autoSaveEnabled = true
	typeArrow(tab)
	a.statusMsg = ""
	a.handleAutoSave()
	if a.modal != nil {
		t.Fatalf("auto-save opened %T", a.modal)
	}
	if a.statusMsg != "" {
		t.Fatalf("auto-save flashed %q", a.statusMsg)
	}
	if disk, _ := os.ReadFile(path); string(disk) != latin1 {
		t.Fatalf("disk = %q", disk)
	}
	if !strings.Contains(statusText(a), "cp1252 ✗") {
		t.Fatalf("status bar = %q, want the red badge", statusText(a))
	}
}

// TestAutoSave_CP1252EditSavesItsBytes is the N-114 bug at the app level:
// an ordinary edit on a Latin-1 file auto-saves with é still one byte.
func TestAutoSave_CP1252EditSavesItsBytes(t *testing.T) {
	a, tab, path := openLatin1(t)
	a.autoSaveEnabled = true
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 4}, false)
	tab.InsertString("!")
	a.handleAutoSave()
	if disk, _ := os.ReadFile(path); string(disk) != "caf\xe9!\nkeep\n" {
		t.Fatalf("disk = %q", disk)
	}
}

// TestEncodingStatus_SegmentAndBadge pins the status bar: UTF-8 says
// nothing, a Windows-1252 file shows a clickable " · cp1252" with a tip,
// and an unencodable character turns it into the alert badge.
func TestEncodingStatus_SegmentAndBadge(t *testing.T) {
	a, tab, _ := openLatin1(t)
	segs := a.encodingStatusSegments(tab)
	if len(segs) != 1 || segs[0].text != " · cp1252" || segs[0].onClick == nil || segs[0].tip == "" || segs[0].alert {
		t.Fatalf("segments = %+v", segs)
	}
	typeArrow(tab)
	segs = a.encodingStatusSegments(tab)
	if len(segs) != 2 || segs[1].text != "cp1252 ✗" || !segs[1].alert || !strings.Contains(segs[1].tip, "line 2") {
		t.Fatalf("badge = %+v", segs)
	}
	u := &editor.Tab{Buffer: editor.NewBuffer("→\n")}
	if got := a.encodingStatusSegments(u); got != nil {
		t.Fatalf("UTF-8 tab got %+v", got)
	}
}

// TestEncodingStatus_ClickAsks pins the segment as a door: a click opens
// the conversion question for the active tab.
func TestEncodingStatus_ClickAsks(t *testing.T) {
	a, tab, _ := openLatin1(t)
	a.encodingStatusSegments(tab)[0].onClick(a)
	if m := confirmOf(a); m == nil || m.title != "Convert to UTF-8?" {
		t.Fatalf("modal = %#v", a.modal)
	}
}

// TestStatusAlertStyle_ReadsOnEveryBuiltin pins the badge's ink against
// its red fill on every built-in theme.
func TestStatusAlertStyle_ReadsOnEveryBuiltin(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, sp := range theme.Builtins() {
		th, err := sp.Resolve()
		if err != nil {
			t.Fatalf("%s: %v", sp.Name, err)
		}
		a.theme = th
		fg, bg, _ := a.statusAlertStyle().Decompose()
		if c := contrastRatio(fg, bg); c < 4.5 {
			t.Errorf("%s: badge contrast %.2f", sp.Name, c)
		}
	}
}

// TestMenuConvertToUTF8_AsksThenConverts pins the ≡ row: a question,
// then a dirty UTF-8 tab and nothing written yet.
func TestMenuConvertToUTF8_AsksThenConverts(t *testing.T) {
	a, tab, path := openLatin1(t)
	a.menuConvertToUTF8()
	m := confirmOf(a)
	if m == nil || m.title != "Convert to UTF-8?" {
		t.Fatalf("modal = %#v", a.modal)
	}
	a.closeModal()
	m.callback(a)
	if tab.Encoding != "" || !tab.Dirty {
		t.Fatalf("after Yes: encoding %q dirty %v", tab.Encoding, tab.Dirty)
	}
	if disk, _ := os.ReadFile(path); string(disk) != latin1 {
		t.Fatalf("convert wrote the file: %q", disk)
	}
	if !strings.Contains(a.statusMsg, "will be saved as UTF-8") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestMenuConvertToUTF8_ExplainsWhenThereIsNothingToDo pins the
// unavailable cases: a flash that says why, no dialog.
func TestMenuConvertToUTF8_ExplainsWhenThereIsNothingToDo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuConvertToUTF8()
	if a.modal != nil || a.statusMsg != "No file open" {
		t.Fatalf("no tab: modal %T flash %q", a.modal, a.statusMsg)
	}
	path := filepath.Join(a.rootDir, "u.txt")
	if err := os.WriteFile(path, []byte("é\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(path)
	a.menuConvertToUTF8()
	if a.modal != nil || !strings.Contains(a.statusMsg, "already UTF-8") {
		t.Fatalf("UTF-8 tab: modal %T flash %q", a.modal, a.statusMsg)
	}
}

// TestDecodeDiffLine pins the rule for git's bytes: by the tab's
// encoding when it has one; otherwise only an invalid line (legacy bytes
// in the index after a conversion) is decoded.
func TestDecodeDiffLine(t *testing.T) {
	cases := []struct{ enc, in, want string }{
		{editor.EncodingCP1252, "-caf\xe9", "-café"},
		{editor.EncodingCP1252, "+caf\xc3\xa9", "+cafÃ©"},
		{"", "-caf\xe9", "-café"},
		{"", "+café", "+café"},
	}
	for _, c := range cases {
		if got := decodeDiffLine(c.enc, c.in); got != c.want {
			t.Errorf("decodeDiffLine(%q, %q) = %q, want %q", c.enc, c.in, got, c.want)
		}
	}
}

// TestRevertHunk_CP1252RemovalRestoresTheBytes is the end-to-end reason
// the diff is decoded: reverting a removed Latin-1 line puts "café" back
// in the buffer, and the save writes E9 — not git's raw byte as a
// U+FFFD.
func TestRevertHunk_CP1252RemovalRestoresTheBytes(t *testing.T) {
	requireGit(t)
	root := initRepo(t)
	path := filepath.Join(root, "fr.txt")
	if err := os.WriteFile(path, []byte(latin1), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "seed")
	if err := os.WriteFile(path, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.openFile(path)
	tab := a.activeTabPtr()
	a.handleGitDiff(&gitDiffEvent{path: path, hunks: loadFileDiff(root, path)})
	hunks := a.fileDiffs[path]
	if len(hunks) != 1 || len(hunks[0].Body) == 0 || hunks[0].Body[0] != "-café" {
		t.Fatalf("hunks = %+v", hunks)
	}
	if why := a.revertHunk(tab, hunks[0]); why != "" {
		t.Fatalf("revert refused: %s", why)
	}
	if tab.Buffer.Lines[0] != "café" {
		t.Fatalf("buffer line 0 = %q", tab.Buffer.Lines[0])
	}
	// The tab opened as UTF-8 ("keep" is ASCII), so the reverted é is
	// written as UTF-8 — what matters is that it is é, not U+FFFD.
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	if disk, _ := os.ReadFile(path); string(disk) != "café\nkeep\n" {
		t.Fatalf("disk = %q", disk)
	}
}

// TestBlameContents_SpeaksTheFilesEncoding pins blame's input: a
// Windows-1252 tab sends E9, so git attributes the line to its commit
// instead of calling it uncommitted.
func TestBlameContents_SpeaksTheFilesEncoding(t *testing.T) {
	requireGit(t)
	root := initRepo(t)
	path := filepath.Join(root, "fr.txt")
	if err := os.WriteFile(path, []byte(latin1), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "seed")
	tab, err := editor.NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := blameContents(tab); got != latin1 {
		t.Fatalf("blameContents = %q", got)
	}
	fb := loadFileBlame(root, path, blameContents(tab), time.Now())
	if fb == nil || len(fb.lines) < 1 || !fb.lines[0].committed() {
		t.Fatalf("line 1 not attributed to its commit: %+v", fb)
	}
}

// TestReadCompareFile_DecodesLikeTheTab pins "compare with saved copy"
// for a Windows-1252 file: the disk side reads as the tab does.
func TestReadCompareFile_DecodesLikeTheTab(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fr.txt")
	if err := os.WriteFile(path, []byte(latin1), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readCompareFile(path)
	if err != nil || got != "café\nkeep\n" {
		t.Fatalf("readCompareFile = %q, %v", got, err)
	}
}

// TestCopyTo_UnencodableBufferRefusesTheCopy pins the copy engine: a
// dirty buffer its own save would refuse is not copied in some other
// form — the copy is refused, naming the file.
func TestCopyTo_UnencodableBufferRefusesTheCopy(t *testing.T) {
	a, tab, path := openLatin1(t)
	typeArrow(tab)
	dest := filepath.Join(a.rootDir, "out")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	a.startCopyTo([]string{path}, dest)
	if !strings.HasPrefix(a.statusMsg, "Copy failed: fr.txt: line 2 has '→'") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
	if _, err := os.Stat(filepath.Join(dest, "fr.txt")); !os.IsNotExist(err) {
		t.Fatalf("a copy was made: %v", err)
	}
}

// TestChatFSRead_DecodesAClosedFile pins the agent's read of a file with
// no tab: text, not bytes JSON would mangle.
func TestChatFSRead_DecodesAClosedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fr.txt")
	if err := os.WriteFile(path, []byte(latin1), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	got, err := a.chatFSRead(path, 0, 0)
	if err != nil || !strings.HasPrefix(got, "café") {
		t.Fatalf("chatFSRead = %q, %v", got, err)
	}
}

// TestFormatFileInPlace_RefusesNonUTF8 pins the in-process formatter's
// guard: a Windows-1252 file is refused with a reason and not rewritten.
func TestFormatFileInPlace_RefusesNonUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.sql")
	orig := []byte("select 'caf\xe9' from t;\n")
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	err := formatFileInPlace(path)
	if err == nil || !strings.Contains(err.Error(), "isn't UTF-8") {
		t.Fatalf("err = %v", err)
	}
	if disk, _ := os.ReadFile(path); !bytes.Equal(disk, orig) {
		t.Fatalf("file rewritten: %q", disk)
	}
}

// TestUTF8Questions_FitTheConfirm pins the wording to the confirm's
// fixed width: the drawer elides an over-long line, and the first draft
// of the save question lost "hold." that way in the real binary.
func TestUTF8Questions_FitTheConfirm(t *testing.T) {
	a, tab, _ := openLatin1(t)
	typeArrow(tab)
	a.saveTabAt(a.activeTab)
	save := confirmOf(a)
	a.closeModal()
	a.menuConvertToUTF8()
	ask := confirmOf(a)
	if save == nil || ask == nil {
		t.Fatal("a question did not open")
	}
	for _, m := range []*confirmModal{save, ask} {
		for _, ln := range m.lines {
			if n := runeLen(ln); n > confirmModalWidth-4 {
				t.Errorf("%q: %q is %d cells, over %d", m.title, ln, n, confirmModalWidth-4)
			}
		}
	}
}
