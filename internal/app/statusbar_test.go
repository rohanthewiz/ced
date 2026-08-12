// =============================================================================
// File: internal/app/statusbar_test.go
// Author: Rohan Allison
// =============================================================================

// Tests for the clickable status bar: segment construction, the stamped
// rects that make draw and hit-testing share one geometry, click routing,
// and the open-menu-at-section door the Copilot/language segments use.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drawnStatusSeg returns the first stamped clickable segment whose text
// contains substr, or nil. Callers draw first — the stamp happens there.
func drawnStatusSeg(a *App, substr string) *statusSegment {
	for i := range a.statusSegs {
		if strings.Contains(a.statusSegs[i].text, substr) {
			return &a.statusSegs[i]
		}
	}
	return nil
}

// expireFlash clears any live status flash. openFile flashes "Opened X",
// and a live flash owns the whole left side of the bar by design — these
// tests are about the ambient segments underneath it.
func expireFlash(a *App) {
	a.statusMsg, a.statusUntil = "", time.Time{}
}

// writeStatusTestFile drops a small file under root and returns its path.
func writeStatusTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestStatusBarSegmentsStamped(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	a := newTestApp(t, root)
	a.openFile(p)
	expireFlash(a)
	a.drawStatusBar()

	for _, want := range []string{"main.go", "go", "Ln 1, Col 1"} {
		seg := drawnStatusSeg(a, want)
		if seg == nil {
			t.Fatalf("no clickable segment containing %q; segs: %+v", want, a.statusSegs)
		}
		if seg.rect.w == 0 {
			t.Fatalf("segment %q stamped with zero-width rect", want)
		}
		if seg.rect.y != a.height-1 {
			t.Fatalf("segment %q stamped off the status row: y=%d", want, seg.rect.y)
		}
	}
	// The ≡ button pins to the far right corner.
	menuSeg := drawnStatusSeg(a, "≡")
	if menuSeg == nil {
		t.Fatal("no ≡ segment stamped")
	}
	if got := menuSeg.rect.x + menuSeg.rect.w; got != a.width {
		t.Fatalf("≡ segment should end at the right edge: ends %d, width %d", got, a.width)
	}
}

func TestStatusBarClickGoToLine(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "a.txt", "one\ntwo\nthree\n")
	a := newTestApp(t, root)
	a.openFile(p)
	expireFlash(a)
	a.drawStatusBar()

	seg := drawnStatusSeg(a, "Ln ")
	if seg == nil {
		t.Fatal("no Ln,Col segment")
	}
	a.statusBarClick(seg.rect.x, seg.rect.y)
	if a.modal == nil {
		t.Fatal("clicking Ln,Col should open the go-to-line prompt")
	}
}

func TestStatusBarClickMenuButton(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.drawStatusBar()

	seg := drawnStatusSeg(a, "≡")
	if seg == nil {
		t.Fatal("no ≡ segment")
	}
	a.statusBarClick(seg.rect.x, seg.rect.y)
	if !a.menuOpen {
		t.Fatal("clicking the status-bar ≡ should open the menu")
	}
}

func TestStatusBarDirtyDotSaves(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "b.txt", "hello\n")
	a := newTestApp(t, root)
	a.openFile(p)
	expireFlash(a)
	tab := a.activeTabPtr()
	tab.InsertString("x")
	if !tab.Dirty {
		t.Fatal("insert should dirty the tab")
	}
	a.drawStatusBar()

	seg := drawnStatusSeg(a, "●")
	if seg == nil {
		t.Fatal("dirty tab should stamp a ● segment")
	}
	a.statusBarClick(seg.rect.x, seg.rect.y)
	if tab.Dirty {
		t.Fatal("clicking ● should save the tab")
	}
}

func TestStatusBarSwitchTabSegment(t *testing.T) {
	root := t.TempDir()
	p1 := writeStatusTestFile(t, root, "one.txt", "1\n")
	p2 := writeStatusTestFile(t, root, "two.txt", "2\n")
	a := newTestApp(t, root)
	a.openFile(p1)
	a.openFile(p2)
	expireFlash(a)
	a.drawStatusBar()

	seg := drawnStatusSeg(a, "two.txt")
	if seg == nil {
		t.Fatal("no filename segment for the active tab")
	}
	a.statusBarClick(seg.rect.x, seg.rect.y)
	if a.modal == nil {
		t.Fatal("clicking the filename should open the switch-tab picker")
	}
}

func TestStatusBarFlashSuppressesLeftSegments(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "c.txt", "text\n")
	a := newTestApp(t, root)
	a.openFile(p)
	expireFlash(a)
	a.flash("saved!")
	a.drawStatusBar()

	if seg := drawnStatusSeg(a, "c.txt"); seg != nil {
		t.Fatal("flash text should replace the left segments while live")
	}
	// The right side survives a flash — the menu stays reachable.
	if seg := drawnStatusSeg(a, "≡"); seg == nil {
		t.Fatal("≡ segment should survive a flash")
	}
	a.statusUntil = time.Time{} // expire the flash
	a.drawStatusBar()
	if seg := drawnStatusSeg(a, "c.txt"); seg == nil {
		t.Fatal("left segments should return once the flash expires")
	}
}

func TestStatusBarNarrowWindowDropsRightSegments(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "long-name-file.txt", "text\n")
	a := newTestApp(t, root)
	a.openFile(p)
	a.gitBranch = "a-quite-long-branch-name"
	a.width = 18 // force the overflow path

	a.drawStatusBar()
	for _, seg := range a.statusSegs {
		if seg.rect.x+seg.rect.w > a.width {
			t.Fatalf("segment %q extends past the window edge", seg.text)
		}
	}
	// The ≡ button is the last segment standing.
	if seg := drawnStatusSeg(a, "≡"); seg == nil {
		t.Fatal("≡ should survive the narrowest layout")
	}
}

func TestOpenMenuAtSection(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.setAllMenuSections(true) // fold everything, the startup default

	a.openMenuAtSection("Copilot")
	if !a.menuOpen {
		t.Fatal("openMenuAtSection should open the menu")
	}
	if a.sectionCollapsed("Copilot") {
		t.Fatal("the named section should be unfolded")
	}
	items, _, _ := a.menuLayout()
	if a.hoveredMenuRow < 0 || a.hoveredMenuRow >= len(items) {
		t.Fatalf("hover row out of range: %d", a.hoveredMenuRow)
	}
	// The hovered row must be the section header or a row inside it —
	// walk back to the nearest header and check its title.
	for i := a.hoveredMenuRow; i >= 0; i-- {
		if items[i].header {
			if items[i].label != "Copilot" {
				t.Fatalf("hover landed in section %q, want Copilot", items[i].label)
			}
			return
		}
	}
	t.Fatal("hover row has no section header above it")
}

func TestStatusBarClickOnPlainTextDoesNothing(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "d.txt", "text\n")
	a := newTestApp(t, root)
	a.openFile(p)
	expireFlash(a)
	a.drawStatusBar()

	// Click a separator cell: find the " · " after the filename by
	// probing a cell between two stamped rects.
	seg := drawnStatusSeg(a, "Ln ")
	if seg == nil {
		t.Fatal("no Ln segment")
	}
	a.statusBarClick(seg.rect.x-2, seg.rect.y) // the " · " lead-in
	if a.modal != nil || a.menuOpen {
		t.Fatal("clicking an informational span should do nothing")
	}
}
