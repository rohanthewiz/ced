// =============================================================================
// File: internal/filetree/filetree.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package filetree implements the left-hand sidebar's file explorer. It is a
// lazy directory tree: children are only read from disk when their parent is
// expanded, so opening the editor on a huge repo is still instant. The tree
// also keeps a flat list of "currently visible" rows so that hit-testing a
// click against rendered rows is O(1).
package filetree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/icons"
	"github.com/rohanthewiz/ced/internal/theme"
)

// Node is a single entry in the file tree. Directories also carry their
// children (loaded lazily on first expansion); files carry only their path.
type Node struct {
	Path     string
	Name     string
	IsDir    bool
	Expanded bool
	Loaded   bool
	Children []*Node

	// IsExec marks a regular file that carries an execute bit
	// (mode&0111). The renderer appends an `ls -F` style '*' to its
	// name — a marker, not a colour, so it never competes with the
	// single-slot fg cascade that git-dirty highlighting owns. Always
	// false for directories (their traversal bit isn't "executable"
	// in the sense a user reads here) and for non-regular files.
	IsExec bool
}

// IsExecFile reports whether a stat result describes a file the user could
// run: a REGULAR file carrying any execute bit. It is exported because two
// callers need the same answer and a second spelling would drift — the tree's
// reload stamps Node.IsExec with it (driving the ls -F '*' marker), and the
// app's "Run in terminal" verb re-checks it against the live file before it
// hands anything to a shell. Directories and symlinks are excluded for the
// reason the marker excludes them: a directory's traversal bit is not what a
// user reads as "executable", and a symlink reports its own non-regular mode.
func IsExecFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

// Tree owns the root node and the most recently rendered flat list of
// visible rows. Click hit-testing maps a screen row index back to the Node
// drawn at that row.
type Tree struct {
	Root    *Node
	visible []*Node // index = screen row in the list area; nil for blank rows.
	ScrollY int

	// ActiveFolder is the absolute path of the folder the user is
	// currently "working in" — the default target for actions like New
	// File. The Render() method bolds the matching row so the choice is
	// always visible. The app updates this whenever the user clicks a
	// tree node or opens a file.
	ActiveFolder string

	// ActiveFile is the absolute path of the file open in the editor's
	// active tab. Render() draws the matching file row bold so the user
	// can see at a glance which tree entry they're editing. The app
	// re-syncs it from the active tab on every draw, so it stays correct
	// no matter which path switched tabs. Empty when no file is open.
	ActiveFile string

	// DirtyFiles and DirtyFolders carry the project's git status — both
	// indexed by absolute path. Files in DirtyFiles render in the theme's
	// Modified color; folders in DirtyFolders do the same so a collapsed
	// branch still signals there's a change inside. Both maps are nil
	// when the project isn't a git repo or when git status hasn't been
	// loaded yet, and the renderer treats nil as "everything clean".
	DirtyFiles   map[string]bool
	DirtyFolders map[string]bool

	// IconsEnabled toggles the Nerd Font glyph that prefixes each row.
	// Set by App.loadUserConfig at startup based on the user's
	// config.json + auto-detection. Off means the row is rendered with
	// only the existing chevron (the legacy look) — important for
	// terminals or fonts that can't render the private-use glyphs.
	IconsEnabled bool

	// ExecMarks toggles the ls -F style '*' the renderer appends to
	// executable files. Defaults to on (set by New; overridden by
	// App.loadUserConfig from config.json's "execmarks" key and the ≡
	// view toggle). The IsExec bit is still computed on every reload
	// regardless, so flipping this re-renders instantly without a tree
	// reload.
	ExecMarks bool

	// Selected is the keyboard-navigation cursor: the row arrow keys
	// move and Enter acts on. Distinct from ActiveFolder/ActiveFile —
	// those describe the EDITOR's state, this one is a position in the
	// tree that may wander freely before committing to anything. nil
	// until keyboard navigation first touches the tree.
	Selected *Node
	// Focused marks the tree as owning the keyboard, which is the only
	// time the Selected row renders with its highlight — a cursor you
	// can't move shouldn't look like one.
	Focused bool
}

// New creates a tree rooted at root and pre-loads its top-level children so
// the user sees something immediately. Hidden entries (dotfiles) are kept
// because they're often what people actually want to inspect over SSH.
func New(root string) (*Tree, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrInvalid
	}
	n := &Node{Path: abs, Name: filepath.Base(abs), IsDir: true, Expanded: true}
	if err := loadChildren(n); err != nil {
		return nil, err
	}
	// ExecMarks defaults on so the '*' shows out of the box; the app
	// stamps the user's config.json preference over this at startup.
	return &Tree{Root: n, ExecMarks: true}, nil
}

// loadChildren is the lazy-load entry point used the first time a directory
// is expanded. It defers to reload, which knows how to merge fresh disk
// state with whatever (if anything) we already had cached.
func loadChildren(n *Node) error {
	if !n.IsDir || n.Loaded {
		return nil
	}
	return n.reload()
}

// reload re-reads the directory's children from disk and replaces n.Children
// with the new list. Existing child Nodes whose names still appear on disk
// are kept by-pointer so their Expanded state, loaded grandchildren, etc.
// survive a refresh. New names get fresh Nodes; vanished names are dropped.
func (n *Node) reload() error {
	if !n.IsDir {
		return nil
	}
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		return err
	}

	existing := make(map[string]*Node, len(n.Children))
	for _, c := range n.Children {
		existing[c.Name] = c
	}

	children := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if shouldHide(e.Name()) {
			continue
		}
		// Executable bit drives the ls -F '*' marker. Only regular
		// files qualify — a symlink reports its own (non-regular)
		// mode, and directories are excluded outright. Recomputed on
		// every reload so a `chmod +x` surfaces on the next refresh,
		// including for survivor nodes whose pointer we reuse below.
		isExec := false
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				isExec = IsExecFile(info)
			}
		}
		if old, ok := existing[e.Name()]; ok && old.IsDir == e.IsDir() {
			old.IsExec = isExec
			children = append(children, old)
			continue
		}
		children = append(children, &Node{
			Path:   filepath.Join(n.Path, e.Name()),
			Name:   e.Name(),
			IsDir:  e.IsDir(),
			IsExec: isExec,
		})
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].IsDir != children[j].IsDir {
			return children[i].IsDir
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	n.Children = children
	n.Loaded = true
	return nil
}

// Refresh re-reads every directory in the tree that has been loaded at
// least once (i.e. anywhere the user has previously expanded). Surviving
// entries keep their Node pointers so deeper Expanded state is preserved;
// new files appear, deleted files vanish.
func (t *Tree) Refresh() {
	refreshNode(t.Root)
}

// refreshNode is Tree.Refresh's recursive worker. It reloads only Loaded
// directories — there's no value in reading directories the user has
// never seen.
func refreshNode(n *Node) {
	if !n.IsDir || !n.Loaded {
		return
	}
	_ = n.reload()
	for _, c := range n.Children {
		refreshNode(c)
	}
}

// shouldHide is the project's small, opinionated list of names the file
// tree refuses to show. These are universally noise: VCS metadata, OS
// junk, language-specific build caches.
func shouldHide(name string) bool {
	switch name {
	case ".git", ".svn", ".hg",
		".DS_Store",
		"node_modules",
		".idea", ".vscode":
		return true
	}
	return false
}

// flatNode pairs a Node with its render depth so the renderer can indent
// without re-walking the tree.
type flatNode struct {
	Node  *Node
	Depth int
}

// flattenInto appends node into out. If node is an expanded directory, it
// recursively appends its children at depth+1.
func flattenInto(n *Node, depth int, out *[]flatNode) {
	if n == nil {
		return
	}
	*out = append(*out, flatNode{Node: n, Depth: depth})
	if n.IsDir && n.Expanded {
		for _, c := range n.Children {
			flattenInto(c, depth+1, out)
		}
	}
}

// treeHeaderRows is how many rows Render spends above the file list: the
// all-caps EXPLORER label and the project name.
const treeHeaderRows = 2

// Render draws the tree into the rectangle (x, y, w, h). Each visible row
// is also remembered (in t.visible) so HitTest can map a click back to a
// node without re-walking the tree.
func (t *Tree) Render(scr tcell.Screen, th theme.Theme, x, y, w, h int) {
	bg := th.SidebarBG
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Header — small all-caps label above the project name. The
	// project name itself is also a click target: it's the only way
	// to reset the active folder back to the root once a subfolder
	// has been selected. Render bold/Accent when it *is* the active
	// folder, plain text otherwise — same visual rule the children
	// rows follow, so the highlight is honest.
	headerStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Bold(true)
	drawString(scr, x, y, w, " EXPLORER", headerStyle)
	rootActive := t.ActiveFolder == "" || t.ActiveFolder == t.Root.Path
	rootStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text).Bold(true)
	if rootActive {
		rootStyle = tcell.StyleDefault.Background(bg).Foreground(th.Accent).Bold(true)
	}
	drawString(scr, x, y+1, w, " "+t.Root.Name, rootStyle)

	// Build the flat list of visible rows from the root's children.
	flat := make([]flatNode, 0, 128)
	for _, c := range t.Root.Children {
		flattenInto(c, 0, &flat)
	}

	// Through ListRows so the header's row count has exactly one
	// spelling — the overflow marker below reads the same split.
	off, listH := t.ListRows(h)
	listTop := y + off
	t.clampScroll(len(flat), listH)

	visible := make([]*Node, 0, listH)
	for row := 0; row < listH; row++ {
		idx := t.ScrollY + row
		if idx < 0 || idx >= len(flat) {
			visible = append(visible, nil)
			continue
		}
		item := flat[idx]
		active := item.Node.IsDir && item.Node.Path == t.ActiveFolder
		activeFile := !item.Node.IsDir && t.ActiveFile != "" && item.Node.Path == t.ActiveFile
		dirty := t.isDirty(item.Node)
		selected := t.Focused && t.Selected != nil && item.Node == t.Selected
		drawNodeRow(scr, th, x, listTop+row, w, item, active, activeFile, dirty, t.IconsEnabled, t.ExecMarks, selected)
		visible = append(visible, item.Node)
	}
	t.visible = visible

	// The "there is more" markers used to be painted here. They now live
	// in app/overflow.go, which draws the same pair of glyphs on the
	// editor and the git panel's diff pane as well — one mechanism, so a
	// marker means the same thing wherever it appears, and one place for
	// the hover popup to read its counts from. Everything it needs is
	// already exported: RowCount, ScrollY and ListRows.
}

// isDirty reports whether a node should render in the Modified color —
// either because the file itself has uncommitted changes or because a
// folder somewhere below it does. Returns false for any node when git
// status hasn't been loaded.
func (t *Tree) isDirty(n *Node) bool {
	if n == nil {
		return false
	}
	if n.IsDir {
		return t.DirtyFolders[n.Path]
	}
	return t.DirtyFiles[n.Path]
}

// drawNodeRow renders one tree row with proper indent, chevron, and color.
// active=true marks this folder as the editor's current working folder
// (the New File default), and is drawn bold + accent-tinted so the user
// can see at a glance where the next "New file" will land. activeFile=true
// marks the file open in the editor's active tab; it's drawn bold (but
// keeps its own file/dirty color) so the currently-edited entry stands
// out without stealing the folder's accent tint. active and activeFile
// are mutually exclusive (one is folder-only, the other file-only).
// dirty=true
// marks the node as having uncommitted git changes (or, for folders,
// containing some) — it overrides the normal foreground with the
// theme's Modified color so changed files stand out at a glance.
// withIcons=true prefixes the name with a Nerd Font glyph + space; off
// renders the legacy chevron-only look for terminals that can't show
// the private-use glyphs. When execMarks=true an executable regular
// file additionally gets a trailing '*' (ls -F style, mirroring a
// directory's '/'), drawn in the row's own colour so it never competes
// with the git-dirty highlight.
//
// When icons are enabled the row is rendered in three segments
// (prefix → glyph → name) so the glyph can take its own per-language
// colour while the name keeps the row's normal fg/dirty/active
// styling. That's the visual cue you find in nvim-tree and friends:
// a quick eye-scan picks out Go from Ruby from Markdown without
// reading any text.
func drawNodeRow(scr tcell.Screen, th theme.Theme, x, y, w int, item flatNode, active, activeFile, dirty, withIcons, execMarks, selected bool) {
	bg := th.SidebarBG
	// The keyboard cursor paints the whole row on the Selection color —
	// the same highlight every list in the editor uses — so the fg
	// cascade below stays untouched and dirty/active reads keep working
	// on the selected row.
	if selected {
		bg = th.Selection
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, y, ' ', nil, tcell.StyleDefault.Background(bg))
		}
	}
	// Compute the row-level foreground via this priority cascade
	// (highest wins last):
	//
	//   1. base = FolderColor / FileColor for the node type
	//   2. dotfile/dotdir → Muted, so .gitignore / .github read as
	//      "metadata, not source" without disappearing
	//   3. active folder → Accent, so the current target is loud
	//   4. dirty → Modified, so uncommitted work always stands out
	//
	// Active/dirty deliberately override the dotfile dimming — a
	// modified .env or the active .github/ folder is still the most
	// important thing on the row.
	var fg tcell.Color
	if item.Node.IsDir {
		fg = th.FolderColor
	} else {
		fg = th.FileColor
	}
	if strings.HasPrefix(item.Node.Name, ".") {
		fg = th.Muted
	}
	if active {
		fg = th.Accent
	}
	if dirty {
		fg = th.Modified
	}
	rowStyle := tcell.StyleDefault.Background(bg).Foreground(fg)
	if active || activeFile {
		rowStyle = rowStyle.Bold(true)
	}

	prefix, glyph, tail := nodeRowSegments(item, withIcons, execMarks)

	if glyph == "" {
		drawString(scr, x, y, w, prefix+tail, rowStyle)
		return
	}

	glyphFg := icons.ColorFor(item.Node.Name, item.Node.IsDir, fg)
	// Dirty files keep their per-language glyph colour — the language
	// hue is the at-a-glance cue, and the name turning Modified is
	// already enough to flag "this is dirty".
	glyphStyle := tcell.StyleDefault.Background(bg).Foreground(glyphFg)
	if active || activeFile {
		glyphStyle = glyphStyle.Bold(true)
	}

	drawString(scr, x, y, w, prefix, rowStyle)
	px := runeLen(prefix)
	drawString(scr, x+px, y, w-px, glyph, glyphStyle)
	gx := runeLen(glyph)
	drawString(scr, x+px+gx, y, w-px-gx, tail, rowStyle)
}

// nodeRowSegments builds the three text chunks a row is painted from: the
// indent + chevron prefix, the optional Nerd Font glyph (its own segment
// because it takes its own per-language colour), and the tail carrying
// the name. An empty glyph means "icons off" — then prefix+tail is the
// whole row.
//
// This is the ONE construction of a row's text, shared by drawNodeRow and
// ContentWidth. A second copy in the measurer would drift, and the
// auto-fit width would then be wrong by a column or two in exactly the
// cases that matter most (deep nesting, icons on, an executable's '*').
func nodeRowSegments(item flatNode, withIcons, execMarks bool) (prefix, glyph, tail string) {
	indent := strings.Repeat("  ", item.Depth)
	if item.Node.IsDir {
		chev := "▸"
		if item.Node.Expanded {
			chev = "▾"
		}
		prefix = " " + indent + chev + " "
		tail = item.Node.Name + "/"
	} else {
		prefix = " " + indent + "  "
		tail = item.Node.Name
		// ls -F style marker: an executable file gets a trailing '*',
		// mirroring the directory's '/'. It rides the row's own style
		// so it inherits the dirty/muted/normal fg — deliberately NOT
		// a new colour, which would collide with the git palette.
		// Gated on execMarks so the ≡ view toggle can hide it.
		if execMarks && item.Node.IsExec {
			tail += "*"
		}
	}
	if !withIcons {
		return prefix, "", tail
	}
	// The two spaces belong to the tail so the glyph segment is exactly
	// the glyph — drawNodeRow offsets the tail by the glyph's own width.
	return prefix, icons.For(item.Node.Name, item.Node.IsDir, item.Node.Expanded), "  " + tail
}

// ContentWidth reports how many columns the tree would need to draw all
// of its currently visible rows untruncated, header block included. The
// app's auto-fit (app/treeautofit.go) sizes the sidebar from it.
//
// Two scoping decisions worth keeping:
//
//   - It measures EVERY expanded row, not just the scroll window. A
//     window-scoped measure (the word-highlighter's rule) would make the
//     panel breathe in and out as the user wheels past a long filename,
//     shifting the editor's columns under a gesture that changed nothing
//     about the tree. Expanding and collapsing a folder is deliberate;
//     scrolling is not. The walk is bounded in practice because the tree
//     is lazy — rows only exist for folders the user opened by hand.
//   - It counts RUNES, because drawString advances exactly one column
//     per rune. Both measure and paint therefore make the same
//     (terminal-dependent) assumption about glyph width, so they can't
//     disagree about whether a row fits.
func (t *Tree) ContentWidth() int {
	if t == nil || t.Root == nil {
		return 0
	}
	// The header block Render draws above the list: the all-caps label
	// and the project name, both with the same one-column gutter.
	w := runeLen(" EXPLORER")
	if n := runeLen(" " + t.Root.Name); n > w {
		w = n
	}
	flat := make([]flatNode, 0, 128)
	for _, c := range t.Root.Children {
		flattenInto(c, 0, &flat)
	}
	for _, item := range flat {
		prefix, glyph, tail := nodeRowSegments(item, t.IconsEnabled, t.ExecMarks)
		if n := runeLen(prefix) + runeLen(glyph) + runeLen(tail); n > w {
			w = n
		}
	}
	return w
}

// runeLen is the column count drawString will consume for s — one column
// per rune, matching what it actually does.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// drawString writes s left-aligned within [x, x+w). Excess content is
// truncated; short content is implicitly padded by the row's pre-painted bg.
func drawString(scr tcell.Screen, x, y, w int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		if col >= w {
			return
		}
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// clampScroll keeps ScrollY within bounds for the current visible-row count.
func (t *Tree) clampScroll(total, viewH int) {
	if t.ScrollY > maxTreeScroll(total, viewH) {
		t.ScrollY = maxTreeScroll(total, viewH)
	}
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
}

// maxTreeScroll is the largest ScrollY that keeps the last row on
// screen. Unlike the editor's viewport there is no overscroll pad: a
// file tree has no "read the bottom comfortably" problem, and letting
// the list scroll into blank space would just lose rows.
func maxTreeScroll(total, viewH int) int {
	if total <= viewH {
		return 0
	}
	return total - viewH
}

// MaxScroll reports the largest ScrollY the tree will hold for a list
// band of viewH rows — the same number clampScroll enforces.
//
// Exported so nothing above this layer re-derives the ceiling the wheel
// can reach — two copies of the arithmetic would drift. Unlike the
// editor's there is no overscroll pad: a tree has no "read the bottom
// comfortably" problem, and scrolling into blank space would just lose
// rows.
func (t *Tree) MaxScroll(viewH int) int {
	return maxTreeScroll(t.RowCount(), viewH)
}

// RowCount is how many rows the list has in total, at the current
// expansion — what the overflow markers count the hidden rows against
// (app/overflow.go), together with ScrollY and ListRows. It walks
// the tree, like ContentWidth does, which is bounded in practice because
// the tree is lazy: rows exist only under folders somebody opened.
func (t *Tree) RowCount() int {
	if t == nil || t.Root == nil {
		return 0
	}
	flat := make([]flatNode, 0, 128)
	for _, c := range t.Root.Children {
		flattenInto(c, 0, &flat)
	}
	return len(flat)
}

// ListRows splits a render rect of h rows the way Render does: the
// offset of the first list row from the top of the rect, and how many
// rows the list gets under the two-row header (the EXPLORER label and
// the project name).
//
// It exists so nothing outside this package has to hard-code that "2".
// The overflow markers span the LIST, not the header — those two rows
// scroll with nothing, and the project name is itself a click target.
func (t *Tree) ListRows(h int) (offset, rows int) {
	rows = h - treeHeaderRows
	if rows < 0 {
		rows = 0
	}
	return treeHeaderRows, rows
}

// HitTest maps a click within the tree's render rectangle to a Node.
// Row 0 is the "EXPLORER" header (not clickable). Row 1 is the project
// root name — clicking it returns t.Root so the caller can set the
// active folder back to the project root, which is otherwise
// unreachable once the user has selected any subfolder. Rows 2+ map
// into the rendered children list.
//
// ok=false means the click landed on the EXPLORER header or empty
// space below the last entry.
func (t *Tree) HitTest(localX, localY int) (*Node, bool) {
	_ = localX
	if localY < 1 {
		return nil, false
	}
	if localY == 1 {
		return t.Root, true
	}
	row := localY - 2
	if row < 0 || row >= len(t.visible) {
		return nil, false
	}
	n := t.visible[row]
	if n == nil {
		return nil, false
	}
	return n, true
}

// Toggle expands or collapses a directory node, lazily loading its children
// the first time it is expanded.
func (t *Tree) Toggle(n *Node) {
	if !n.IsDir {
		return
	}
	if !n.Expanded {
		_ = loadChildren(n)
	}
	n.Expanded = !n.Expanded
}

// Scroll moves the file tree's viewport by delta rows (negative = up).
func (t *Tree) Scroll(delta int) {
	t.ScrollY += delta
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
}

// -----------------------------------------------------------------------------
// Keyboard navigation
// -----------------------------------------------------------------------------
//
// The tree was mouse-only from birth; these helpers are the mechanics
// behind the app's keyboard layer (internal/app/treenav.go). Policy —
// which key does what — stays in the app; the tree only knows how to
// enumerate its visible rows and move a cursor through them.

// VisibleNodes returns every currently visible row in display order:
// the same flattening Render draws, independent of scroll. The root is
// not a row (it renders as the header), matching the click contract.
func (t *Tree) VisibleNodes() []*Node {
	flat := make([]flatNode, 0, 128)
	for _, c := range t.Root.Children {
		flattenInto(c, 0, &flat)
	}
	out := make([]*Node, len(flat))
	for i, f := range flat {
		out[i] = f.Node
	}
	return out
}

// selectedIndex finds Selected in rows, or -1. A selection collapsed
// out of view (its ancestor folded) is "gone" on purpose — the cursor
// only ever points at something the user can see.
func (t *Tree) selectedIndex(rows []*Node) int {
	for i, n := range rows {
		if n == t.Selected {
			return i
		}
	}
	return -1
}

// SelectDelta moves the keyboard cursor by delta visible rows, clamping
// at both ends. With no (visible) selection, any movement lands on the
// first row — the predictable entry point.
func (t *Tree) SelectDelta(delta int) {
	rows := t.VisibleNodes()
	if len(rows) == 0 {
		t.Selected = nil
		return
	}
	idx := t.selectedIndex(rows)
	if idx < 0 {
		t.Selected = rows[0]
		return
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	t.Selected = rows[idx]
}

// ParentOf returns the directory containing n, or nil when n is a
// top-level entry (its parent is the root header, which is not a row).
func (t *Tree) ParentOf(n *Node) *Node {
	var walk func(dir *Node) *Node
	walk = func(dir *Node) *Node {
		for _, c := range dir.Children {
			if c == n {
				if dir == t.Root {
					return nil
				}
				return dir
			}
			if c.IsDir {
				if p := walk(c); p != nil {
					return p
				}
			}
		}
		return nil
	}
	return walk(t.Root)
}

// EnsureSelectedVisible scrolls the list so the cursor sits inside a
// viewport of viewH rows. No-op when nothing is selected.
func (t *Tree) EnsureSelectedVisible(viewH int) {
	if t.Selected == nil || viewH <= 0 {
		return
	}
	rows := t.VisibleNodes()
	idx := t.selectedIndex(rows)
	if idx < 0 {
		return
	}
	if idx < t.ScrollY {
		t.ScrollY = idx
	}
	if idx >= t.ScrollY+viewH {
		t.ScrollY = idx - viewH + 1
	}
}

// SelectedIndex returns the cursor's position within rows (as returned
// by VisibleNodes), or -1 when nothing visible is selected — the
// exported face of selectedIndex for the app's keyboard layer.
func (t *Tree) SelectedIndex(rows []*Node) int {
	return t.selectedIndex(rows)
}
