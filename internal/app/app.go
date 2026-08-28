// =============================================================================
// File: internal/app/app.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// Package app is the editor's top-level glue: it owns the tcell screen,
// the file tree, the open tabs, and the event loop. The drawing is split
// into four panels (sidebar / tab bar / editor body / status bar) and the
// mouse dispatcher routes presses, drags, and wheel events to whichever
// panel the cursor is over.
//
// The editor is mouse-first by design — there are no Ctrl-keyed shortcuts
// because they collide with terminal flow control (Ctrl-S/Q) and tmux/zellij
// prefixes. Instead, every action lives behind a click on the ≡ icon at
// the top-left of the tab bar, which opens a centered modal of actions.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/customactions"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/finder"
	"github.com/rohanthewiz/ced/internal/icons"
	"github.com/rohanthewiz/ced/internal/plugins"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/theme"
	"github.com/rohanthewiz/ced/internal/userconfig"
	"github.com/rohanthewiz/ced/internal/version"
)

// Layout, behavior, and feel constants. Constants instead of config —
// the editor is opinionated by design.
const (
	defaultSidebarWidth = 30
	minSidebarWidth     = 18
	minEditorAfterDrag  = 40
	minWidth            = 50
	minHeight           = 24
	statusFlashFor      = 3 * time.Second
	doubleClickMs       = 500 * time.Millisecond
	doubleEscMs         = 500 * time.Millisecond
	wheelLines          = 3
	wheelCols           = 6 // horizontal step per WheelLeft/WheelRight event

	// modifierStickyWindow is how long a previously-seen Shift modifier
	// state is allowed to persist forward onto the next wheel event.
	// Some terminals (Zellij + macOS Terminal among them) emit the
	// Shift state as a separate ButtonNone+Shift event right before
	// firing the WheelUp/WheelDown without the modifier — so without
	// this carry-forward, shift+wheel reads as plain wheel. 250ms is
	// long enough to bridge the gap and short enough that releasing
	// Shift before scrolling reliably reverts to vertical scroll.
	modifierStickyWindow = 250 * time.Millisecond

	// treeRefreshInterval is how often the background goroutine kicks off
	// a file-tree reload so the sidebar stays in sync with on-disk changes
	// made by other tools (git, mv, another tmux pane, etc.). 10s feels
	// "fresh enough" while costing only a handful of ReadDir syscalls.
	treeRefreshInterval = 10 * time.Second

	// menuButtonWidth is how many cells the ≡ icon occupies at the top-left
	// of the tab bar. Tabs render starting just after it.
	menuButtonWidth = 4

	// modalWidth is the action modal's column count. Sized to comfortably
	// fit the longest dynamic label — "Rename folder (subdir/)" with a
	// folder name up to maxLabelSuffix runes — plus the leading "▸ "
	// chevron and one cell of right padding. Very long custom-action
	// labels will still clip but won't break layout. Height is computed
	// dynamically from the visible groups — see menuLayout.
	modalWidth = 48

	// maxLabelSuffix is the rune budget that newFileLabel /
	// renameFolderLabel / deleteFolderLabel use when truncating their
	// "(in subdir/)" / "(subdir/)" suffix. Pinned alongside modalWidth
	// so the two stay in lockstep — bumping the modal without bumping
	// the suffix budget leaves dead space, and shrinking the modal
	// without shrinking the suffix budget reintroduces the overflow
	// bug where folder names bled into the editor underneath.
	maxLabelSuffix = 30

	// autoScrollTick is how often the auto-scroll goroutine emits a tick
	// while the user is drag-selecting with the cursor parked outside the
	// editor's vertical edges. ~16 ticks/sec feels responsive without
	// overshooting on small files.
	autoScrollTick = 60 * time.Millisecond
)

// autoScrollEvent is the custom tcell event our auto-scroll goroutine
// posts at autoScrollTick intervals while the user is drag-selecting past
// the top or bottom edge of the editor pane.
type autoScrollEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *autoScrollEvent) When() time.Time { return e.when }

// treeRefreshEvent is the custom tcell event the background tree-refresh
// goroutine posts every treeRefreshInterval. The main loop reacts by
// asking the file tree to re-read every loaded directory.
type treeRefreshEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *treeRefreshEvent) When() time.Time { return e.when }

// customActionDoneEvent is posted by runCustomAction when its background
// shell-out finishes. Carries the label and any error so the main loop
// can flash a sensible status message — running scp / ssh inline would
// freeze the UI for the duration of the network round-trip.
type customActionDoneEvent struct {
	when   time.Time
	label  string
	err    error
	output []byte // combined stdout+stderr from the action's shell run
}

// When satisfies the tcell.Event interface.
func (e *customActionDoneEvent) When() time.Time { return e.when }

// tabRect remembers where each tab was drawn so click handling can hit-test
// against the actual rendered geometry rather than re-deriving it.
type tabRect struct {
	Index    int
	X, Width int
	CloseX   int // Cell column of the × close button.
	// MarkerX is the leading status slot (⊘ / ⚠ / ●, see reconcile.go).
	// It is a rect rather than a redundant "+1" in two places because it
	// is a click target now: the ⚠ re-raises the conflict prompt a
	// "Decide later" deferred, and a marker the hit-test located one cell
	// away from where the painter put it would open the wrong tab's
	// question.
	MarkerX int
}

// clickRecord tracks the last mouse-press location and time so we can
// detect double-clicks (and select the word under the cursor).
type clickRecord struct {
	x, y int
	when time.Time
}

// menuItemDef describes one row in the action modal: the label shown to
// the user, the y-offset it lives at inside the modal, the action it runs
// when clicked, and a predicate that returns true when the action is
// applicable in the current context (so we can dim non-applicable rows).
//
// labelFor is an optional dynamic-label hook: when non-nil, drawMenu calls
// it instead of using the static label string. Used by toggle-style rows
// whose label depends on app state ("Show / Hide file explorer").
//
// shortcut is the row's keyboard binding rendered right-aligned in the
// row, muted, like a GUI menu's accelerator column ("esc s", "alt+←").
// Purely informational — the actual dispatch lives in the leader table
// and handleKey; keep the two in sync when rebinding. Empty means the
// action is menu/mouse-only and the column stays blank.
//
// header marks a collapsible section-title row rather than an action:
// menuLayout stamps one at the top of every collapsible group, its
// action toggles that section's collapsed state (keyed by label), and
// drawMenu paints it with a fold chevron instead of a plain row.
type menuItemDef struct {
	label    string
	shortcut string
	relY     int
	action   func(*App)
	enabled  func(*App) bool
	labelFor func(*App) string
	header   bool
}

// menuGroup is a titled block of action rows in the ≡ menu. Collapsible
// groups get a clickable header row (fold chevron + title) and hide
// their items while collapsed; a non-collapsible group (Quit) renders
// its items directly, set off by a divider instead of a header.
type menuGroup struct {
	title       string
	items       []menuItemDef
	collapsible bool
}

// builtinMenuGroups returns the editor's built-in action groups in
// display order. Custom actions loaded from
// ~/.config/ced/actions.json get spliced in as their own group in
// menuLayout — they're not included here so toggling them on or off
// doesn't require touching this table.
//
// Each group renders as a titled, collapsible block: menuLayout stamps
// a fold header above it, hides its rows while the section is collapsed,
// and recomputes every relY. Quit is intentionally NOT collapsible — a
// one-row section you could fold away the exit from reads as a bug, so
// it renders headerless, set off by a divider. The relY field is left
// zero here on purpose — it gets stamped at layout time.
func builtinMenuGroups() []menuGroup {
	return []menuGroup{
		{title: "Tab", collapsible: true, items: []menuItemDef{
			{label: "Save", shortcut: "esc s", action: (*App).menuSave, enabled: (*App).hasSavableTab},
			{label: "Save & close tab", action: (*App).menuSaveAndClose, enabled: (*App).hasSavableTab},
			{label: "Close tab", shortcut: "esc w", action: (*App).menuClose, enabled: (*App).hasTab},
			// Tab switching (tabbar.go). These are the ONLY keyboard path
			// to another open file — the strip is mouse-driven, and on a
			// narrow window or with a dozen files open the tab you want
			// may not be drawn at all.
			{label: "Next tab", shortcut: "esc .", action: (*App).menuNextTab, enabled: (*App).hasMultipleTabs},
			{label: "Previous tab", shortcut: "esc ,", action: (*App).menuPrevTab, enabled: (*App).hasMultipleTabs},
			{label: "Switch tab…", shortcut: "esc b", action: (*App).menuSwitchTab, enabled: (*App).hasMultipleTabs},
			// Recent files sits under Switch tab because it answers the
			// same question — "get me to another file" — over a wider set:
			// the ones no longer open. In the Tab group rather than File
			// for that adjacency, and because File is far enough down the
			// menu to be below the fold (recentfiles.go).
			{label: "Recent files…", shortcut: "esc B", action: (*App).menuRecentFiles, enabled: (*App).hasRecentFiles},
			{action: (*App).menuToggleAutoSave, enabled: alwaysTrue, labelFor: (*App).autoSaveToggleLabel},
		}},
		// View toggles. Deliberately the second group: the menu outgrows
		// short windows and scrolls, so anything living near the bottom
		// is effectively hidden — and Show terminal is reached for far
		// too often to bury. Keep these rows above the fold.
		{title: "View", collapsible: true, items: []menuItemDef{
			{shortcut: "esc t", action: (*App).menuToggleSidebar, enabled: alwaysTrue, labelFor: (*App).sidebarToggleLabel},
			// Keyboard focus for the tree (treenav.go) — sits under the
			// row that shows/hides it, same subject one step deeper.
			{label: "Focus file tree", shortcut: "esc T", action: (*App).menuFocusTree, enabled: alwaysTrue},
			// Auto-fit sits under the tree rows it governs, above the
			// exec-marks row: all three are about the sidebar, and this
			// one is the only path back after a splitter drag turned it
			// off (the drag persists the change, so it outlives the
			// session). See treeautofit.go.
			{action: (*App).menuToggleTreeAutoFit, enabled: alwaysTrue, labelFor: (*App).treeAutoFitToggleLabel},
			{action: (*App).menuToggleExecMarks, enabled: alwaysTrue, labelFor: (*App).execMarksToggleLabel},
			{action: (*App).menuToggleWordHighlight, enabled: alwaysTrue, labelFor: (*App).wordHighlightToggleLabel},
			{shortcut: "esc `", action: (*App).menuToggleTerminal, enabled: alwaysTrue, labelFor: (*App).termToggleLabel},
			{action: (*App).menuToggleTermDock, enabled: alwaysTrue, labelFor: (*App).termDockToggleLabel},
			// The Find-all list's edge. Here rather than in Search
			// because it's a layout preference like the terminal dock
			// above it — and because it's the only keyboard path to the
			// setting while the list itself is open (a modal owns the
			// keyboard, so the menu is unreachable from inside it).
			{action: (*App).menuToggleFindAllDock, enabled: alwaysTrue, labelFor: (*App).findAllDockToggleLabel},
			// The editor scrollbar (scrollbar.go). It sits with the two
			// dock rows rather than up with the tree/word-highlight
			// toggles because it is the same KIND of setting — a column
			// of the editor's band spent on chrome — and because the
			// rows above it are pinned above the fold on a 24-row
			// window (TestMenuLayout_TerminalRowsAboveTheFold) with no
			// slack left to push them down by. A View toggle rather than
			// a leader key: the flat table is out of mnemonic letters,
			// and this is a once-a-session decision.
			{action: (*App).menuToggleScrollbar, enabled: alwaysTrue, labelFor: (*App).scrollbarToggleLabel},
			// Color themes (theme.go). They live in View rather than in a
			// group of their own for the same above-the-fold reason the
			// terminal rows do: the menu scrolls on short windows, and a
			// theme picker buried under Git is a theme picker nobody
			// finds. The label names the theme in force, so this row also
			// answers "what am I looking at?" without being clicked.
			{action: (*App).menuTheme, enabled: alwaysTrue, labelFor: (*App).themeMenuLabel},
			{label: "Customize theme…", action: (*App).menuThemeCustomize, enabled: alwaysTrue},
			{label: "Reload themes", action: (*App).menuThemeReload, enabled: alwaysTrue},
		}},
		{title: "History", collapsible: true, items: []menuItemDef{
			{label: "Undo", shortcut: "esc u", action: (*App).menuUndo, enabled: (*App).hasUndo},
			{label: "Redo", shortcut: "esc r", action: (*App).menuRedo, enabled: (*App).hasRedo},
			{label: "Revert file", action: (*App).menuRevert, enabled: (*App).hasRevert},
		}},
		// Command palette lives OUTSIDE this group — it's promoted to the
		// pinned top zone in menuLayout so it stays the menu's headline
		// entry even when every section is folded (the startup default).
		{title: "Search", collapsible: true, items: []menuItemDef{
			{label: "Find in file", shortcut: "esc f", action: (*App).menuFind, enabled: (*App).hasFindable},
			{label: "Find all in file", shortcut: "esc F", action: (*App).menuFindAll, enabled: (*App).hasFindAll},
			// Replace opens the same bar with its second row showing —
			// one surface, two rows, rather than a separate dialog.
			{label: "Replace in file", shortcut: "esc e", action: (*App).menuReplace, enabled: (*App).hasReplaceable},
			// The two search modifiers. They have buttons in the bar, but
			// the bar OWNS the keyboard while it's open, so these rows are
			// the only way to set one from the menu — and the only path at
			// all on a terminal that eats clicks (the macOS-Terminal rule).
			{action: (*App).menuToggleFindCase, enabled: alwaysTrue, labelFor: (*App).findCaseToggleLabel},
			{action: (*App).menuToggleFindWord, enabled: alwaysTrue, labelFor: (*App).findWordToggleLabel},
			{label: "Go to line…", shortcut: "esc j", action: (*App).menuGoToLine, enabled: (*App).hasGoToLine},
			{label: "Find file in project", shortcut: "esc p", action: (*App).menuFindFile, enabled: (*App).hasFinder},
			// Text across the whole tree, listed in the same panel the
			// in-file list uses (projectsearch.go). Sits under its
			// filename twin because they answer the same question at two
			// scopes, and the shifted leader says so.
			{label: "Find in project", shortcut: "esc P", action: (*App).menuFindInProject, enabled: (*App).hasProjectSearch},
		}},
		// Navigation — browser-style back/forward through the file
		// history (tree, tabs, finder, and definition jumps all feed it).
		{title: "Navigation", collapsible: true, items: []menuItemDef{
			{label: "Go back", shortcut: "esc o / alt+←", action: (*App).menuNavBack, enabled: (*App).hasNavBack},
			{label: "Go forward", shortcut: "esc O / alt+→", action: (*App).menuNavForward, enabled: (*App).hasNavForward},
		}},
		{title: "Git", collapsible: true, items: []menuItemDef{
			{label: "Next change", shortcut: "esc h", action: (*App).menuNextHunk, enabled: (*App).hasDiffHunks},
			{label: "Previous change", shortcut: "esc H", action: (*App).menuPrevHunk, enabled: (*App).hasDiffHunks},
			// Blame sits with the other two rows that are about the file
			// in front of you rather than about the repository
			// (gitblame.go). The toggle shows the column; the row under
			// it is the keyboard twin of clicking an annotation, and is
			// what makes the verb reachable in a terminal that eats
			// clicks.
			{shortcut: "esc A", action: (*App).menuToggleBlame, enabled: (*App).hasGitRepo, labelFor: (*App).blameToggleLabel},
			{label: "Blame this line…", action: (*App).menuBlameCommit, enabled: (*App).hasGitRepo},
			// git's own report (gitstatusreport.go). It opens the
			// repo-level block because it is the read those verbs act on
			// — and it is the only surface that carries what the tree
			// colors, the changes panel and the status bar all drop: what
			// the sequencer is in the middle of, and what a bare commit
			// would record.
			{label: "Git status…", action: (*App).menuGitStatus, enabled: (*App).hasGitStatusReport},
			{label: "Stage file", action: (*App).menuGitStageFile, enabled: (*App).hasStageableFile},
			{label: "Unstage file", action: (*App).menuGitUnstageFile, enabled: (*App).hasUnstageableFile},
			{label: "Commit staged", action: (*App).menuGitCommit, enabled: (*App).hasGitStaged},
			// Keyboard twin of the panel's Suggest row — the chat agent
			// drafts the message for the panel's selection, or for the
			// index when nothing is ticked.
			{label: "Suggest commit message", action: (*App).menuGitSuggestCommit, enabled: (*App).hasSuggestableCommit},
			// The attribution default for those drafts
			// (gitcommitmsg.go). It sits beside the row that PRODUCES
			// them rather than in Copilot's group because its subject is
			// the commit, and because this is the only place the
			// preference is visible with no draft on screen — the ✦
			// prompt's chip overrides it for one commit, this sets what
			// the chip starts from.
			{action: (*App).menuToggleCommitTrailer, enabled: alwaysTrue, labelFor: (*App).commitTrailerToggleLabel},
			// Push sits right after the commit rows because that is the
			// sequence it belongs to — stage, commit, push — and it is the
			// keyboard twin of the three click targets (both panels' headers
			// and the status bar's arrow) that open the same dialog.
			{label: "Push…", action: (*App).menuGitPush, enabled: (*App).hasGitPushTarget},
			{label: "Stash changes", action: (*App).menuGitStash, enabled: (*App).hasGitChanges},
			{label: "Pop stash", action: (*App).menuGitStashPop, enabled: (*App).hasGitStash},
			{label: "Switch branch", action: (*App).menuGitSwitchBranch, enabled: (*App).hasGitRepo},
			{label: "Recent branches", action: (*App).menuGitRecentBranches, enabled: (*App).hasGitRepo},
			{shortcut: "esc g", action: (*App).menuToggleGitPanel, enabled: (*App).hasGitRepo, labelFor: (*App).gitPanelToggleLabel},
			// The pre-commit survey (gitpanelwalk.go). It opens the panel
			// on the way in — walking the changes and showing the panel
			// are one thought, the same argument that lets "Search
			// history…" open the log it filters.
			{label: "Review all changes ▶", action: (*App).menuGitPanelReview, enabled: (*App).hasGitPanelReview},
			// The keyboard twin of the panel's "Actions ▾" header button:
			// the panel is mouse-driven by design, but macOS Terminal can
			// swallow clicks, so its verbs must be menu-reachable too.
			{label: "Git panel actions", action: (*App).menuGitPanelActions, enabled: (*App).hasGitPanelOpen},
			{shortcut: "esc L", action: (*App).menuToggleGitLog, enabled: (*App).hasGitRepo, labelFor: (*App).gitLogToggleLabel},
			// Search history opens the log panel itself when it's shut —
			// it is one thought, not two — so it is enabled on any repo
			// rather than only while the panel is up.
			{label: "Search history…", shortcut: "esc S", action: (*App).menuGitLogSearch, enabled: (*App).hasGitRepo},
			// Same keyboard-twin rule for the log panel's Actions ▾ button.
			{label: "Git log actions", action: (*App).menuGitLogActions, enabled: (*App).hasGitLogOpen},
			// The way back into a conflict picker that was dismissed —
			// and the only way into one for a conflict ced never started
			// (a `stash pop`, or a rebase run in the terminal beside it).
			// Dim on a clean repo, which is the overwhelming case, so the
			// row doubles as a signal that something is parked.
			{label: "Resolve conflicts…", action: (*App).menuGitResolveConflicts, enabled: (*App).hasGitConflict},
		}},
		// Diff viewer (compare.go). Its own group rather than rows under
		// Git, because none of it needs a repository: the sources are the
		// buffer you're editing, any file in the tree, and text you
		// pasted, and the differ is ced's own. A user outside a repo — or
		// with git absent entirely — gets the whole feature.
		{title: "Compare", collapsible: true, items: []menuItemDef{
			{label: "Compare with file…", action: (*App).menuCompareFile, enabled: (*App).hasComparable},
			{label: "Compare with saved copy", action: (*App).menuCompareSaved, enabled: (*App).hasSavedCopy},
			// Inside cats this reads the HOST's clipboard and opens the panel
			// already populated; outside it, ced's own; and with neither it
			// becomes the row below it (catsclip.go). One row rather than two,
			// because "compare with what I copied" is one question — a second
			// row would make the user choose between two clipboards they have
			// no way to look at.
			{label: "Compare with clipboard", action: (*App).menuCatsCompareClipboard, enabled: (*App).hasComparable},
			{label: "Compare with pasted text", action: (*App).menuComparePaste, enabled: (*App).hasComparable},
			{action: (*App).menuToggleCompare, enabled: (*App).hasCompareResult, labelFor: (*App).compareToggleLabel},
		}},
		// Code intelligence (LSP-backed; rows dim when no server)
		{title: "Code", collapsible: true, items: []menuItemDef{
			// The completion popup (completion.go). It heads the group
			// because it is the row a GoLand user looks for first, and
			// because it is the only one here that also fires ITSELF —
			// the server's trigger characters open it as you type, and
			// this row is the deliberate invocation for everywhere else.
			{label: "Completions", shortcut: "esc spc", action: (*App).menuCompletion, enabled: (*App).hasLSPActions},
			{label: "Go to definition", shortcut: "esc d", action: (*App).menuGoToDefinition, enabled: (*App).hasLSPActions},
			// The inverse question, one row down from the one it inverts:
			// 'd' asks where a symbol comes FROM, this asks who uses it.
			// Results land in the Find-all panel's project mode, so the
			// three cross-file lists read as one instrument
			// (lspreferences.go).
			{label: "Find references…", shortcut: "esc R", action: (*App).menuFindReferences, enabled: (*App).hasLSPActions},
			{label: "Hover info", shortcut: "esc i", action: (*App).menuHoverInfo, enabled: (*App).hasLSPActions},
			// The same tooltip asked a different question: 'i' says what
			// the symbol under the cursor IS, 'I' says where you are in
			// the call you're typing (lspsignature.go). Manual, not
			// automatic — a modal owns the keyboard here.
			{label: "Signature help", shortcut: "esc I", action: (*App).menuSignatureHelp, enabled: (*App).hasLSPActions},
			// The file's outline as a fuzzy picker (lspsymbols.go). Sits
			// under its lowercase twin because it answers the same
			// question at a wider scope: 'd' jumps to the definition of
			// what's under the cursor, 'D' lists every definition in the
			// file — the f/F and p/P convention again.
			{label: "Go to symbol in file…", shortcut: "esc D", action: (*App).menuGoToSymbol, enabled: (*App).hasLSPActions},
			// Clickable terminal output (termdiag.go). It sits with the
			// code-intelligence rows rather than the terminal's View
			// toggles because it answers their question — "take me to the
			// problem" — and it is the one row here that needs no
			// language server at all: `go build` and `grep -n` are the
			// providers.
			{label: "Go to terminal output location…", shortcut: "esc ~", action: (*App).menuTermLocations, enabled: (*App).hasTermOutput},
			// The two rows that WRITE, placed directly above the row that
			// undoes them: rename is the verb the workspace-edit primitive
			// was built for, code actions are the verb that proved it, and
			// the cross-file undo is what a user reaches for next when
			// either went somewhere they didn't expect.
			//
			// Code actions comes first because it is the broader question —
			// "what can you do here?" — and its label says which span it
			// will ask about, since a selection changes the answer
			// completely (lspcodeaction.go).
			{labelFor: (*App).codeActionMenuLabel, shortcut: "esc c", action: (*App).menuCodeActions, enabled: (*App).hasCodeActions},
			{label: "Rename symbol…", shortcut: "esc E", action: (*App).menuRenameSymbol, enabled: (*App).hasLSPActions},
			// The Problems panel (problems.go) and its keyboard twins.
			// They sit at the foot of the group because they are about
			// the whole project rather than the caret, and the toggle
			// stays enabled with no server so the panel can say WHY it
			// is empty — a dimmed row never could.
			{shortcut: "esc !", action: (*App).menuToggleProblems, enabled: alwaysTrue, labelFor: (*App).problemsToggleLabel},
			{label: "Next problem", action: (*App).menuNextProblem, enabled: (*App).hasAnyDiagnostics},
			{label: "Previous problem", action: (*App).menuPrevProblem, enabled: (*App).hasAnyDiagnostics},
			// Undo a server-authored multi-file edit as one gesture
			// (workspaceedit.go). Plain undo already claims the press when
			// the cursor is in one of the touched files; this row is the
			// path for the two cases it can't serve — the active tab isn't
			// a participant, or every touched file went straight to disk
			// and there is no participant tab to stand in. The label names
			// the verb and the file count because this rewrites files that
			// may not be on screen. No leader key: the flat table is out of
			// mnemonic letters and plain undo covers the common case.
			{labelFor: (*App).wsEditUndoLabel, action: (*App).menuUndoWorkspaceEdit, enabled: (*App).wsUndoAvailable},
		}},
		// GitHub Copilot (copilot-language-server sidecar). Rows stay
		// clickable even when the sidecar is unavailable — the action
		// flashes WHY instead of dimming into a dead end, because Sign
		// in is the first thing a new user reaches for. See copilot.go.
		{title: "Copilot", collapsible: true, items: []menuItemDef{
			{action: (*App).menuCopilotAuth, enabled: alwaysTrue, labelFor: (*App).copilotAuthLabel},
			// The chat toggle moved here from the View group (owner
			// preference): every Copilot surface — auth, chat, model,
			// ghost text, the kill switch — reads as one block.
			{shortcut: "esc a c", action: (*App).menuToggleChat, enabled: alwaysTrue, labelFor: (*App).chatToggleLabel},
			// The backend picker (chatagent.go): the chat panel is a
			// generic ACP client, and this row switches which agent
			// binary answers it. Re-picking the current agent is the
			// crash-retry path for non-Copilot backends.
			{shortcut: "esc a b", action: (*App).menuChatAgent, enabled: alwaysTrue, labelFor: (*App).chatAgentLabel},
			{shortcut: "esc a m", action: (*App).menuChatModel, enabled: alwaysTrue, labelFor: (*App).chatModelLabel},
			// Context attachments. The panel's chips carry ✕ buttons, but
			// ATTACHING has no mouse surface of its own — these rows are
			// it (copilot_chat_context.go).
			{action: (*App).menuToggleChatContext, enabled: alwaysTrue, labelFor: (*App).chatContextToggleLabel},
			// The write half of the same trust question the context
			// toggle asks about reads (copilot_chat_perm.go): off is
			// read-only chat, enforced whatever a permission prompt says.
			{action: (*App).menuToggleChatWrite, enabled: alwaysTrue, labelFor: (*App).chatWriteToggleLabel},
			{shortcut: "esc a a", action: (*App).menuChatAttachCurrent, enabled: (*App).hasFileTab, labelFor: (*App).chatAttachActionLabel},
			{label: "Attach file to chat…", shortcut: "esc a f", action: (*App).menuChatAttachFile, enabled: (*App).hasFinder},
			{action: (*App).menuChatClearAttachments, enabled: (*App).hasChatAttachments, labelFor: (*App).chatClearAttachLabel},
			// The one READING verb in a group otherwise made of
			// toggles and pickers (summarize.go). It sits under the
			// attach rows because it is what those attachments are
			// for: "put this in front of the model" then "and tell me
			// what it says". Agent-agnostic like the chat toggle above
			// it — the group is the editor's AI block, not a Copilot
			// feature list — and the label names what it will cover,
			// since a selection changes the question completely.
			{shortcut: "esc a z", action: (*App).menuSummarize, enabled: (*App).canSummarize, labelFor: (*App).summarizeLabel},
			// Keyboard twin of the transcript's trailing ⧉ button, for
			// the same reason the git panel has one: the panel is
			// mouse-driven, but macOS Terminal can swallow clicks.
			{label: "Copy chat transcript", action: (*App).menuChatCopyAll, enabled: (*App).hasChatTranscript},
			{action: (*App).menuToggleSuggestions, enabled: alwaysTrue, labelFor: (*App).suggestionsToggleLabel},
			{action: (*App).menuToggleCopilot, enabled: alwaysTrue, labelFor: (*App).copilotToggleLabel},
		}},
		// Model Context Protocol servers (mcp.go). Its own group, not a
		// row in Copilot's: the inventory is declared to whichever chat
		// agent is active, and ced connects to it independently of any
		// of them. Rows stay clickable with nothing configured — the
		// empty case opens the setup help, which is the answer to the
		// question a user with no servers is actually asking.
		{title: "MCP", collapsible: true, items: []menuItemDef{
			{shortcut: "esc a t", action: (*App).menuMCPServers, enabled: alwaysTrue, labelFor: (*App).mcpServersLabel},
			{label: "Reload MCP config", action: (*App).menuMCPReload, enabled: alwaysTrue},
			{action: (*App).menuMCPCopyResult, enabled: (*App).hasMCPResult, labelFor: (*App).mcpCopyResultLabel},
		}},
		// Agent skills (skills.go) — the folders of markdown instructions
		// kept in ~/.claude/skills and <project>/.claude/skills. Its own
		// group next to MCP for the same reason MCP has one: both are
		// inventories handed to whichever chat agent is running, not
		// features of any one backend. The first row stays clickable with
		// nothing installed — the empty case opens the setup help, which
		// is the answer to the question a user with no skills is asking.
		{title: "Skills", collapsible: true, items: []menuItemDef{
			{shortcut: "esc a s", action: (*App).menuUseSkill, enabled: alwaysTrue, labelFor: (*App).skillsMenuLabel},
			{label: "Open skill…", action: (*App).menuOpenSkill, enabled: (*App).hasSkills},
			{label: "Reload skills", action: (*App).menuReloadSkills, enabled: alwaysTrue},
		}},
		// GoNotes capture (gonotes.go) — the selected text, or the whole
		// file, saved as a new note in the user's running GoNotes
		// server. Its own group for the reason MCP, Skills and Plugins
		// each have one: it is a separate system ced talks to, not a
		// feature of any subsystem here. The row stays clickable
		// whatever state that server is in — availability is discovered
		// by trying, and a row that dimmed whenever GoNotes was
		// restarted would be wrong exactly when the user asked.
		{title: "Notes", collapsible: true, items: []menuItemDef{
			{shortcut: "esc a n", action: (*App).menuSendToNotes, enabled: (*App).canSendToNotes, labelFor: (*App).sendToNotesLabel},
		}},
		// Declarative plugins (plugins.go) — the user's own shell
		// commands bound to menu rows, leader keys, editor events and a
		// decoration overlay. Its own group next to MCP and Skills for
		// the same reason those have one: it's an inventory the user
		// installs, not a feature of any other subsystem. The rows here
		// are MANAGEMENT (what's loaded, reload, kill switch); the
		// plugins' actual commands are spliced in as their own group by
		// visibleMenuGroups, so they reach the palette too. The first
		// row stays clickable with nothing installed — the empty case
		// opens the setup help, which is the answer to the question a
		// user with no plugins is actually asking.
		{title: "Plugins", collapsible: true, items: []menuItemDef{
			{action: (*App).menuPluginsInfo, enabled: alwaysTrue, labelFor: (*App).pluginsMenuLabel},
			{label: "Reload plugins", action: (*App).menuReloadPlugins, enabled: alwaysTrue},
			{action: (*App).menuTogglePlugins, enabled: alwaysTrue, labelFor: (*App).pluginsToggleLabel},
		}},
		{title: "File", collapsible: true, items: []menuItemDef{
			// Workspace rows first — the File>Open Folder convention every
			// editor teaches, and the only surface for a root switch (it
			// gets no leader key: the flat table is out of mnemonic
			// letters and this is a once-an-hour action, not a once-a-
			// minute one). See folder.go for why a switch is a restart.
			{label: "Open folder…", action: (*App).menuOpenFolder, enabled: alwaysTrue},
			{label: "Recent folders…", action: (*App).menuRecentFolders, enabled: (*App).hasRecentFolders},
			{action: (*App).menuToggleSession, enabled: alwaysTrue, labelFor: (*App).sessionToggleLabel},
			// Whether another pane's `ced --remote` / `ced --wait` can
			// hand this instance a file (remote.go). A workspace row, not
			// a View one: what it scopes is which files this ROOT accepts.
			// The label doubles as the feature's only status surface —
			// "unavailable" is how a user learns the socket never bound.
			{action: (*App).menuToggleRemote, enabled: alwaysTrue, labelFor: (*App).remoteToggleLabel},
			{shortcut: "esc n", action: (*App).menuNewFile, enabled: alwaysTrue, labelFor: (*App).newFileLabel},
			{label: "Rename file", action: (*App).menuRename, enabled: (*App).hasFileTab},
			{label: "Delete file", action: (*App).menuDelete, enabled: (*App).hasFileTab},
			{action: (*App).menuRenameFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).renameFolderLabel},
			{action: (*App).menuDeleteFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).deleteFolderLabel},
			{label: "Copy file", action: (*App).menuCopyFile, enabled: (*App).hasFileTab},
			{action: (*App).menuCopyFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).copyFolderLabel},
			{action: (*App).menuPasteItem, enabled: (*App).hasFileClip, labelFor: (*App).pasteItemLabel},
			{label: "Zip file", action: (*App).menuZipFile, enabled: (*App).hasFileTab},
			{action: (*App).menuZipFolder, enabled: alwaysTrue, labelFor: (*App).zipFolderLabel},
			{label: "Copy relative path", action: (*App).menuCopyRelativePath, enabled: (*App).hasFileTab},
			{label: "Copy absolute path", action: (*App).menuCopyAbsolutePath, enabled: (*App).hasFileTab},
			// Running the open file in the terminal panel (runexec.go).
			// A File row rather than a View one — View holds the panel's
			// toggles, this one is a verb about the FILE, and it belongs
			// with the other rows that act on it. Its ≡ twin in the tree
			// is the right-click row; this is the path that survives a
			// terminal which eats right-click. No leader key: the flat
			// table is out of mnemonic letters.
			{action: (*App).menuRunExecutable, enabled: (*App).hasRunnableTab, labelFor: (*App).runExecutableLabel},
		}},
		{title: "Edit", collapsible: true, items: []menuItemDef{
			{label: "Copy selection", shortcut: "cmd+c", action: (*App).menuCopy, enabled: (*App).hasSelection},
			{label: "Cut selection", action: (*App).menuCut, enabled: (*App).hasSelection},
			{label: "Paste", shortcut: "cmd+v", action: (*App).menuPaste, enabled: (*App).hasClipboard},
			{label: "Toggle line comment", shortcut: "esc /", action: (*App).menuToggleLineComment, enabled: (*App).hasCommentableTab},
			{label: "Duplicate line", shortcut: "ctrl+d", action: (*App).menuDuplicateLines, enabled: (*App).hasEditableTab},
			{label: "Move line up", shortcut: "alt+↑", action: (*App).menuMoveLinesUp, enabled: (*App).hasEditableTab},
			{label: "Move line down", shortcut: "alt+↓", action: (*App).menuMoveLinesDown, enabled: (*App).hasEditableTab},
			// Multi-line editing (multicaret.go). The mouse gesture is
			// Alt+click, which has no menu row it could be — these are
			// the keyboard paths, and the Clear row is the way back for
			// anyone who got here by accident.
			{label: "Add caret below", shortcut: "esc m", action: (*App).menuAddCaretBelow, enabled: (*App).hasEditableTab},
			{label: "Add caret above", shortcut: "esc M", action: (*App).menuAddCaretAbove, enabled: (*App).hasEditableTab},
			{label: "Add next occurrence", shortcut: "esc *", action: (*App).menuAddNextOccurrence, enabled: (*App).hasMultiCaretTarget},
			{label: "Select all occurrences", shortcut: "esc &", action: (*App).menuSelectAllOccurrences, enabled: (*App).hasMultiCaretTarget},
			{label: "Clear extra carets", shortcut: "esc", action: (*App).menuClearCarets, enabled: (*App).hasCarets},
		}},
		{title: "Quit", collapsible: false, items: []menuItemDef{
			{label: "Quit editor", shortcut: "esc q", action: (*App).menuQuit, enabled: alwaysTrue},
		}},
	}
}

// alwaysTrue is the default predicate for actions that are always applicable
// (currently just Quit — which has no preconditions).
func alwaysTrue(*App) bool { return true }

// visibleMenuGroups returns the built-in groups with the user's own
// actions spliced in as their own collapsible groups right before Quit,
// so they sit at the bottom of the menu where the user reaches for
// "what do I do with this file" actions. Two sources feed it: plugin
// commands (plugins.go) and actions.json custom actions. Recomputed on
// every call — cheap, and it's what lets the layout react when either
// inventory is reloaded mid-session.
//
// Splicing them here rather than into builtinMenuGroups is what gets
// them into the command palette for free: paletteActionItems flattens
// this function, not the static table.
func (a *App) visibleMenuGroups() []menuGroup {
	groups := builtinMenuGroups()
	// builtinMenuGroups guarantees Quit is last; the tests pinning
	// placement catch anyone who reorders that.
	quit := groups[len(groups)-1]
	groups = groups[: len(groups)-1 : len(groups)-1]

	// The host-multiplexer group, present only INSIDE cats (cats_glue.go).
	// Spliced rather than built in, and conditional rather than dimmed,
	// because every row in it addresses a program that isn't there in a
	// plain terminal — the whole group would be permanently grey for the
	// overwhelming majority of ced users, which is noise, not vocabulary.
	if cg := a.catsMenuItems(); len(cg) > 0 {
		groups = append(groups, menuGroup{title: "Cats", collapsible: true, items: cg})
	}
	if pc := a.pluginMenuItems(); len(pc) > 0 {
		groups = append(groups, menuGroup{
			title: "Plugin commands", collapsible: true, items: pc,
		})
	}
	if len(a.customActions) == 0 {
		return append(groups, quit)
	}
	ca := make([]menuItemDef, 0, len(a.customActions))
	for i := range a.customActions {
		i := i // capture
		// Custom actions are user-defined shell — we don't try to guess
		// from the command string whether it needs $FILE. "Upgrade ced"
		// obviously doesn't; "Open on computer" obviously does. Both
		// should be runnable from the menu; if a $FILE-dependent command
		// is invoked with no tab open it'll fail with a real error and
		// our info modal surfaces it. Better that than getting the
		// heuristic wrong half the time.
		ca = append(ca, menuItemDef{
			label:   a.customActions[i].Label,
			action:  func(app *App) { app.runCustomAction(i) },
			enabled: alwaysTrue,
		})
	}
	custom := menuGroup{title: "Custom", collapsible: true, items: ca}
	return append(groups, custom, quit)
}

// menuLayout flattens the visible menu groups into a single ordered
// slice of rows with relY positions assigned, plus the divider rows and
// the modal's total cell height. Each collapsible group contributes a
// header row (whose action toggles the section) followed by its items —
// unless the section is collapsed, in which case only the header shows.
// A non-collapsible group (Quit) gets a leading divider instead of a
// header. Recomputed on every call so folding a section reflows the
// whole modal.
func (a *App) menuLayout() (items []menuItemDef, dividers []int, modalHeight int) {
	// Title at relY 1, divider under it at relY 2, first row at relY 3.
	dividers = []int{2}
	y := 3

	// Pinned top zone: the command palette (the menu's headline — a fuzzy
	// gateway to every action) followed by the expand/collapse-all toggle.
	// Both sit OUTSIDE the collapsible groups so they stay reachable even
	// when every section is folded, which is the startup default. A divider
	// sets the zone off from the (usually folded) section list below it.
	items = append(items, menuItemDef{
		label:    paletteMenuLabel,
		shortcut: "esc k",
		relY:     y,
		action:   (*App).menuCommandPalette,
		enabled:  alwaysTrue,
	})
	y++
	items = append(items, menuItemDef{
		relY:     y,
		action:   (*App).menuToggleAllSections,
		enabled:  alwaysTrue,
		labelFor: (*App).expandAllToggleLabel,
	})
	y++
	dividers = append(dividers, y)
	y++

	for _, g := range a.visibleMenuGroups() {
		if g.collapsible {
			title := g.title // capture for the toggle closure
			items = append(items, menuItemDef{
				label:   title,
				header:  true,
				relY:    y,
				enabled: alwaysTrue,
				action:  func(app *App) { app.toggleMenuSection(title) },
			})
			y++
			if a.sectionCollapsed(title) {
				continue // items hidden; only the header occupies a row
			}
		} else {
			// Headerless group: a divider stands in for the missing
			// title so it doesn't blend into the section above.
			dividers = append(dividers, y)
			y++
		}
		for _, it := range g.items {
			it.relY = y
			items = append(items, it)
			y++
		}
	}
	// y now points at the bottom border row; height is one beyond.
	modalHeight = y + 1
	return items, dividers, modalHeight
}

// sectionCollapsed reports whether the named menu section is folded.
// Reads a nil map safely, so the default (every section expanded) needs
// no initialization.
func (a *App) sectionCollapsed(title string) bool {
	return a.menuCollapsed[title]
}

// toggleMenuSection folds or unfolds the named section in place — the
// header row's click / Enter action. The menu stays open (unlike an
// action row) so the user can fold several sections in one visit.
func (a *App) toggleMenuSection(title string) {
	if a.menuCollapsed == nil {
		a.menuCollapsed = map[string]bool{}
	}
	a.menuCollapsed[title] = !a.menuCollapsed[title]
}

// menuSectionTitles returns the titles of every collapsible menu section
// in display order — the set the collapse-all startup default and the
// expand/collapse-all toggle operate over. The non-collapsible Quit group
// is excluded (it has no fold state); the synthetic Custom group is
// included when present so "Collapse all" folds it too.
func (a *App) menuSectionTitles() []string {
	var titles []string
	for _, g := range a.visibleMenuGroups() {
		if g.collapsible {
			titles = append(titles, g.title)
		}
	}
	return titles
}

// anyMenuSectionExpanded reports whether at least one collapsible section
// is currently unfolded. It drives the expand/collapse-all toggle: if
// anything is open the button collapses everything, otherwise it expands
// everything.
func (a *App) anyMenuSectionExpanded() bool {
	for _, title := range a.menuSectionTitles() {
		if !a.sectionCollapsed(title) {
			return true
		}
	}
	return false
}

// setAllMenuSections folds (collapsed=true) or unfolds every collapsible
// section at once. It is the shared mechanism behind both the collapse-all
// startup default and the expand/collapse-all menu toggle, so the two can
// never drift on which sections count as foldable.
func (a *App) setAllMenuSections(collapsed bool) {
	if a.menuCollapsed == nil {
		a.menuCollapsed = map[string]bool{}
	}
	for _, title := range a.menuSectionTitles() {
		a.menuCollapsed[title] = collapsed
	}
}

// menuToggleAllSections is the "Expand all / Collapse all" button. Like a
// section header it deliberately leaves the menu open so the reflow is
// visible in place. If anything is expanded it collapses everything;
// only when every section is already folded does it expand them all — so
// after the collapse-all default the button's first press opens the menu
// up in one click.
func (a *App) menuToggleAllSections() {
	a.setAllMenuSections(a.anyMenuSectionExpanded())
}

// expandAllToggleLabel returns the dynamic label for the expand/collapse-
// all row, mirroring what the button will do on the next press.
func (a *App) expandAllToggleLabel() string {
	if a.anyMenuSectionExpanded() {
		return "Collapse all sections"
	}
	return "Expand all sections"
}

// seedMenuFoldDefault contracts every menu section on first run so the
// action menu opens as a compact index of section headers rather than a
// long scroll of rows: the command palette (pinned at the top) is the
// primary entry point, and the folded headers are a quick map into the
// rest. Skipped when fold state already exists so it never overrides a
// choice the user has made — honoring "start collapsed UNLESS a menu
// expand state already exists".
func (a *App) seedMenuFoldDefault() {
	if a.menuCollapsed != nil {
		return
	}
	a.setAllMenuSections(true)
}

// App is the editor's top-level state holder and event-loop owner.
type App struct {
	screen tcell.Screen
	theme  theme.Theme

	// themeName is the registry id of the palette in force, and
	// themeSpecs is the registry it was resolved from (built-ins plus
	// ~/.config/ced/themes/*.json). Both are re-read on demand rather
	// than cached forever — see theme.go — so a theme file added or
	// edited mid-session shows up without a restart.
	themeName  string
	themeSpecs []theme.Spec

	// themePinned records that the user has CHOSEN a theme — config.json
	// named one at startup, or they picked one from the ≡ picker since.
	// Its only consumer is the cats host-theme auto-select (catstheme.go),
	// which must never overrule a choice somebody actually made.
	themePinned bool

	rootDir   string
	tree      *filetree.Tree
	tabs      []*editor.Tab
	activeTab int

	// activeFolder is the directory the editor is currently "working
	// in" — the default target for New File from the main menu. It
	// updates whenever the user clicks a folder in the tree, opens a
	// file (parent dir wins), or right-clicks a folder. See
	// setActiveFolder for the single write path so the file tree's
	// matching highlight stays in sync.
	activeFolder string

	width, height int

	// sidebarShown controls whether the file explorer panel is visible.
	// When false the editor and tab bar fill the whole window.
	sidebarShown bool

	// wordHLEnabled mirrors the persisted matching-word-highlight
	// preference (≡ View toggle, default on). The authoritative copy —
	// every open tab carries its own flag for the decoration source to
	// read, kept in step by applyWordHighlight. See wordhl.go.
	wordHLEnabled bool

	// termDockLeft selects the alternate layout: the terminal panel
	// docks as a vertical strip on the LEFT edge and the file tree
	// flips to the RIGHT edge. False is the classic layout (tree
	// left, terminal a bottom strip). Persisted via userconfig
	// ("termdock"), toggled from the ≡ menu.
	termDockLeft bool

	// findAllDockRight selects the Find-all list's edge: false (default)
	// is the wide strip under the tab bar, true a tall column down the
	// editor's right side. It lives on App rather than on the modal
	// because the popup is transient and the preference isn't — it's
	// persisted via userconfig ("findalldock"), set from the popup's own
	// ◨ button, its `d` key, and the ≡ View toggle. See findall.go.
	findAllDockRight bool

	// sidebarWidth is the live width of the file-explorer block (file tree
	// + 1-cell splitter on its right edge), in screen cells. The user can
	// drag the splitter to change it within [minSidebarWidth, width-minEditorAfterDrag].
	// While treeAutoFit is on it is DERIVED from the tree's own content
	// instead (autoFitSidebar, called from draw) — the drag is what turns
	// that off, since the two can't both own the number.
	sidebarWidth int

	// treeAutoFit lets the sidebar size itself to the file tree's longest
	// row whenever the editor can spare the columns. Persisted as
	// "treeautofit" (default on); dragging the splitter writes it off.
	// See treeautofit.go.
	treeAutoFit bool

	// scrollbarShown reserves the editor body's rightmost column for the
	// draggable scrollbar. Persisted as "scrollbar" (default on), toggled
	// from the ≡ View group. scrollbarGrab is the offset within the thumb
	// at which the current drag grabbed it, so the thumb slides with the
	// pointer instead of snapping under it. See scrollbar.go.
	scrollbarShown bool
	scrollbarGrab  int

	clipBuf string
	// fileClipPath is the absolute path armed by a Copy file/folder
	// action; paste duplicates it under a collision-free name. Empty
	// means nothing has been copied this session. See copypaste.go.
	fileClipPath string
	// clipKind records which clipboard (text selection vs file) was
	// written most recently so Cmd+V pastes the right one —
	// last-write-wins, like a system clipboard.
	clipKind clipboardKind

	// pasting is true between a bracketed-paste start and end marker
	// while an editable tab is the target; pasteBuf accumulates the
	// content verbatim so it can be spliced in as one InsertString. See
	// textpaste.go. Both are zero when no paste is in flight.
	pasting  bool
	pasteBuf []rune
	// pasteCR records that the previous accumulated event was a CR, so a
	// CRLF pair folds into ONE newline. Both halves arrive as separate
	// key events (KeyEnter then KeyCtrlJ), and each is a line break on
	// its own, so without this a Windows-flavored paste would double
	// every line. Reset with the buffer at each start marker.
	pasteCR bool

	statusMsg    string
	statusUntil  time.Time
	dragMode     string // "editor" while a drag-select is active.
	lastClick    clickRecord
	lastTabRects []tabRect

	// statusSegs is the status bar's clickable spans, re-stamped by
	// every drawStatusBar the same way lastTabRects tracks the tab
	// strip: the draw is the one source of geometry, the click handler
	// only reads it. See statusbar.go.
	statusSegs []statusSegment

	// whichKey is the leader cheat-sheet overlay (whichkey.go): shown
	// after a ~350ms hesitation on an armed leader, dismissed by any
	// non-leader key. Not a modal — it documents the leader without
	// claiming its keys.
	whichKey whichKeyState

	// treeFocus hands the keyboard to the file tree (treenav.go) —
	// same single-owner model as term.focused and chat.focused, kept
	// mutually exclusive by the focus and click handlers.
	treeFocus bool

	// findAllPin is the PINNED find-all list (findall.go): the same
	// modal object moved out of the modal slot so it survives editor
	// clicks and edits like the git panels do. nil when no list is
	// pinned; at most one of this and a modal-slot findAllModal exists.
	findAllPin *findAllModal

	// problems is the diagnostics worklist (problems.go): a bottom-strip
	// panel listing every problem the language server has published, and
	// what the status bar's `✗ 2 ⚠ 5` segment opens. Its rows/selection
	// live here even while it is closed, because the next/previous-problem
	// verbs walk the same list without needing it on screen.
	problems problemsState

	// projectSearchSeq generation-stamps "Find in project" runs so a
	// result launched before the user changed their mind can't open a
	// list over whatever they're doing now. See projectsearch.go.
	projectSearchSeq int

	// projectSearchActive counts scans currently off on a goroutine. A
	// COUNT rather than a bool because a superseded run still finishes and
	// still posts its event: every start is balanced by exactly one
	// arrival, so the count returns to zero even when the user re-searches
	// mid-scan. Read by anything that needs to know the editor is busy
	// with something long (see cats_glue.go's state reporter).
	projectSearchActive int

	// tabLabels is what each tab is DRAWN as — the basename, or a
	// directory-prefixed form when another open tab shares it — and
	// tabLabelPaths is the list of open paths those labels were computed
	// from, which is the cache's whole invalidation story. See
	// tablabel.go.
	tabLabels     []string
	tabLabelPaths []string

	// tabScroll is the index of the leftmost tab DRAWN in the tab strip.
	// Derived state, never a preference: layoutTabs re-derives it from
	// the active tab and the available width on every frame. See
	// tabbar.go.
	tabScroll int

	// lastShiftAt is the wall-clock time we last saw any mouse event
	// carrying the Shift modifier. Some terminals (notably Zellij over
	// macOS Terminal) report modifier state in a separate ButtonNone
	// event right before the wheel event, instead of folding the
	// modifier into the wheel event itself. We treat a wheel event as
	// shifted when one of those modifier-state events arrived within
	// modifierStickyWindow. See handleMouse.
	lastShiftAt time.Time

	menuOpen       bool
	hoveredMenuRow int       // index into menuItems of the row under the mouse, or -1.
	lastEscape     time.Time // timestamp of the previous Esc press, for double-tap detection.
	// leaderChained is true when lastEscape was re-armed by a repeatable
	// leader action rather than a real Esc press. In chain mode only
	// repeatable bindings fire ("Esc z z z" undoes three times); any
	// other key drops back to normal typing. See leaderBinding.repeat.
	leaderChained bool

	// leaderChord holds a prefix binding's sub-table while a chord is
	// half-typed (Esc-a waiting for its second rune), with leaderChordAt
	// stamping when it armed so handleChordKey can expire it. nil means
	// no chord pending — the overwhelmingly common state. See leader.go.
	leaderChord   []leaderBinding
	leaderChordAt time.Time
	// leaderChordName labels the pending namespace ("AI", "Plugin") so a
	// missed second rune can say which one it missed. Stamped at arm
	// time rather than looked up later: a dynamic namespace's table is
	// resolved once, and re-deriving the name from the table would mean
	// matching bindings by identity.
	leaderChordName string
	// menuScroll is how many content rows the action menu is scrolled
	// when its layout is taller than the window (the menu outgrew short
	// terminals at ~40 rows). 0 whenever everything fits; reset on every
	// open. Always read through menuScrollOffset(), which re-clamps
	// against the live geometry so a mid-menu terminal resize can't
	// strand the offset past the end.
	menuScroll int

	// menuCollapsed records which ≡-menu sections the user has folded,
	// keyed by group title. Session-only (like menuScroll and the panel
	// sizes) and survives menu close/reopen so a fold sticks for the
	// session. nil until the first fold — sectionCollapsed reads it
	// nil-safely, so every section starts expanded.
	menuCollapsed map[string]bool

	// modal is the active secondary overlay (prompt, confirm,
	// dirty-close, form, tree context menu, file finder) or nil when
	// none is up. Exactly one can be open at a time — openModal
	// enforces it — so the key/mouse routers dispatch to this single
	// slot instead of walking a per-modal precedence chain. See
	// modal.go for the interface and the individual modal files for
	// each implementation.
	modal modal

	// Find bar — opened with Esc-f (or Esc-e for the replace row, or the
	// matching ≡ rows). The bar is a 1-row strip pinned above the status
	// bar, 2 rows once replace is showing; while it's open it owns the
	// keyboard. The active tab carries the query, options and match list
	// (see editor.Tab.SetFindQuery), so each tab remembers its own search
	// across closes / reopens.
	//
	// Both inputs are the shared textField — the house rule for every
	// single-line input. findFocus names which one the keyboard is in.
	findOpen        bool
	findReplaceOpen bool
	findFocus       int
	findField       textField
	replField       textField

	// Search modifiers. The App holds the authoritative copy (the tabs
	// get a pushed copy via applyFindOptions) because they describe how
	// the USER searches, not a property of one file — flipping "match
	// case" then switching tabs must not silently switch it back.
	// Session-only, deliberately not persisted: see applyFindOptions.
	findCase bool
	findWord bool

	// Auto-scroll while drag-selecting past the editor's top/bottom edge.
	// lastDragX/Y is the most recent mouse position so the auto-scroll
	// tick can extend the selection at the user's column even though the
	// mouse hasn't moved.
	autoScrollStop chan struct{}
	autoScrollDir  int // -1 up, 0 idle, +1 down
	lastDragX      int
	lastDragY      int

	// treeRefreshStop signals the background tree-refresh goroutine to exit.
	treeRefreshStop chan struct{}

	// Secondary-caret blink. caretBlinkStop signals the ticker goroutine
	// to exit (nil = not running, which is the state whenever there are
	// no extra carets); caretBlinkOff is the current phase, pushed onto
	// every tab's CaretsHidden. See multicaret.go.
	caretBlinkStop chan struct{}
	caretBlinkOff  bool

	// Auto-save state. autoSaveEnabled mirrors the persisted user
	// preference (≡ menu toggle, default on). autoSaveTimer is the
	// pending idle countdown — armed/reset only from the main loop.
	// autoSaveSig is the last-seen sum of every tab's EditRev, the
	// cheap "did any buffer mutate since we last looked?" signature
	// autoSaveAfterEvent compares against. See autosave.go.
	// autoSaveDelay is the resolved "autosavedelay" preference. Zero
	// means "nothing resolved onto this App yet" (a hand-built App, or a
	// config that never loaded) and reads as the shipped default — see
	// autoSaveInterval, which is the only thing that may read this field.
	autoSaveEnabled bool
	autoSaveTimer   *time.Timer
	autoSaveSig     int
	autoSaveDelay   time.Duration

	// formatRuns counts formatter runs in flight per file path, so the
	// reconcile tick can tell ced's own pending write from an external
	// one. See format.go's formatRunBegin.
	formatRuns map[string]int

	// commitTrailer mirrors the persisted "commitmsgtrailer" preference
	// (≡ Git toggle, default on): whether a commit message the chat
	// agent DRAFTED carries a Co-Authored-By trailer naming it. Read at
	// the moment a prompt or a suggestion opens; the prompt then owns a
	// copy the chip can flip for that one commit without touching this.
	// See gitcommitmsg.go.
	commitTrailer bool

	// conflicts is the open set of "the file changed under you" records,
	// keyed by the tab holding the stale buffer. A tab present here has
	// an unresolved disk conflict: it wears the ⚠ marker in the strip,
	// auto-save is suspended for it, and every save path refuses to
	// write until the user answers the prompt. Keyed by pointer rather
	// than path because the tab IS the thing at risk — a rename would
	// otherwise orphan the record. Owned by reconcile.go; closeTab drops
	// the entry.
	conflicts map[*editor.Tab]*diskConflict

	// syntaxTimer is the pending re-highlight countdown: a typing burst
	// defers the O(file) Chroma pass, and this is what wakes the loop up
	// once the burst ends so the colors land without further input.
	// Armed only while a tab is waiting on it. See syntax.go.
	syntaxTimer *time.Timer

	// gitBranch is the current branch name for the project root (or a
	// short commit SHA when HEAD is detached). Empty when the root isn't
	// a git repo. Updated on the same 10-second tick as refreshGitStatus.
	gitBranch string

	// gitIsRepo / gitHasStaged / gitHasStash / gitStagedFiles mirror the
	// last git-status snapshot: whether the project root sits inside a
	// work tree at all, whether anything is staged for commit, whether a
	// stash entry exists to pop, and which files carry staged changes.
	// They exist so the menu predicates for the git command rows (Stage /
	// Unstage file, Commit staged, Stash, Switch branch) are pure field
	// reads — running `git` inside an enabled() check would fork on every
	// menu draw. gitStagedFiles also feeds the git panel's checkboxes.
	gitIsRepo      bool
	gitHasStaged   bool
	gitHasStash    bool
	gitStagedFiles map[string]bool

	// gitConflicted mirrors the same snapshot's unmerged paths — the
	// repo is parked mid-cherry-pick / revert / merge / rebase with
	// files still to resolve. A field for the same reason as the flags
	// above: it gates the "Resolve conflicts…" menu row, whose enabled()
	// runs on every menu draw. See gitconflict.go.
	gitConflicted map[string]bool

	// gitUpstream / gitAhead / gitBehind / gitHasRemote mirror the
	// tracking half of the same snapshot: HEAD's upstream in short form
	// ("origin/main"), the commit distance either way, and whether the
	// repo has any remote at all. Fields for the same fork-free reason
	// as the flags above — the status bar's push indicator is rebuilt on
	// every draw and the Push row's enabled() runs on every menu draw.
	// See gitpush.go.
	gitUpstream  string
	gitAhead     int
	gitBehind    int
	gitHasRemote bool

	// fileDiffs holds the latest parsed `git diff -U0` hunks per open
	// file path, feeding the gutter marks and hunk navigation. Written
	// only from the main loop (handleGitDiff); nil until the first
	// diff lands, and nil-map reads are safe, so no eager init needed.
	fileDiffs map[string][]diffHunk

	// The blame layer (gitblame.go). blameOn is the toggle; fileBlames
	// holds the finished annotations per path; blameSeq is the
	// per-path request generation that drops an answer overtaken by a
	// newer one; blameTimer/blameStale are the settle debounce that
	// re-blames a buffer after the typing stops. All written from the
	// main loop only.
	blameOn    bool
	fileBlames map[string]*fileBlame
	blameSeq   map[string]int
	blameTimer *time.Timer
	blameSig   string
	// blamePending is a "Blame this line…" asked before any blame for
	// that file existed — answered when the load lands.
	blamePending *blamePendingReveal

	// gitPanel is the collapsible bottom review panel (changed files +
	// selected file's diff vs HEAD). Mutated only on the main loop;
	// diff fetches post gitPanelDiffEvents. See gitpanel.go.
	gitPanel gitPanelState

	// compare is the diff viewer — the active buffer against another
	// file, its own saved copy, or pasted text — and the fourth
	// occupant of the single-occupancy bottom strip. See compare.go.
	compare compareState

	// gitLog is the commit-history browser sharing the same bottom
	// strip (the two swap, never stack). Mutated only on the main loop;
	// detail fetches post gitLogShowEvents. See gitlog.go.
	gitLog gitLogState

	// term is the embedded grsh terminal panel. It shares the bottom
	// strip with the git panel (exactly one may be open) and is the
	// only surface besides modals that takes the keyboard — via its
	// focused flag, not the modal slot. Mutated only on the main loop;
	// Evals run on goroutines and post term*Events. See terminal.go.
	term termPanelState

	// lsp holds the language-server integration state: the connection,
	// per-document sync bookkeeping, and diagnostics. Mutated only on
	// the main loop; background work posts lsp*Events. See lsp.go.
	lsp lspState

	// completion is the LSP completion popup (completion.go). It rides
	// the same connection lspState owns but keeps its own state because
	// it is the one code-intelligence surface that PERSISTS across
	// keystrokes — a list the user types into rather than a fact shown
	// once. Deliberately not a modal; see that file's header.
	completion completionState

	// hoverDwell is the mouse-dwell hover tooltip (hoverdwell.go): the
	// pointer's half of the hover verb, ambient where the keyboard's
	// half is modal. Armed at Tier 1 only; see that file's header.
	hoverDwell hoverDwellState

	// commitReceipt is the transient panel naming the hash and message
	// of the commit that just landed (gitcommitreceipt.go). Passive
	// chrome like the dwell tooltip — it never takes the modal slot,
	// and any keystroke dismisses it without being consumed.
	commitReceipt commitReceiptState

	// copilot holds the GitHub Copilot sidecar state: the
	// copilot-language-server connection and the sign-in machinery.
	// Mutated only on the main loop; background work posts
	// copilot*Events. See copilot.go.
	copilot copilotState

	// chat is the Copilot chat panel (ACP agent + transcript + left
	// strip). Rides the copilot enable toggle but runs its own process
	// in --acp mode. Mutated only on the main loop; the agent posts
	// chat*Events. See copilot_chat.go.
	chat chatState

	// mcp is the Model Context Protocol integration: the inventory read
	// from mcp.json, ced's own connections to those servers, and the
	// declaration handed to the chat agent. Mutated only on the main
	// loop; connects and tool calls post mcp*Events. See mcp.go.
	mcp mcpState

	// skills is the agent-skill inventory scanned from the three standard
	// skills directories. Read-only data: picking a skill attaches its
	// SKILL.md to the next chat turn, and nothing in it is ever executed.
	// See skills.go.
	skills skillsState

	// nav is the app-wide file-navigation history (Go back / Go
	// forward). Recorded centrally in openFile and tabBarClick so every
	// navigation surface — tree, tabs, finder, go-to-definition — feeds
	// the same trail. See nav.go.
	nav navState

	// wsGroup is the single "undo the last multi-file edit" slot — the one
	// thing in this editor that sits ABOVE the per-tab undo stacks. A
	// server-authored workspace edit (rename, a code action) rewrites
	// several files at once, so plain undo in any of them claims the whole
	// group rather than leaving the refactor half applied. nil means no
	// such edit is pending. See workspaceedit.go.
	wsGroup *wsEditGroup

	// plugins is the declarative plugin inventory read from
	// ~/.config/ced/plugins, plus the decorations its providers have
	// painted. Nothing in it runs until a file opens, a file saves, or
	// the user picks a row — see plugins.go for the house rules,
	// plugincmd.go and plugindeco.go for the two execution paths.
	plugins pluginState

	// customActions is the list of user-configured shell-out actions
	// loaded from ~/.config/ced/actions.json at startup. When
	// non-empty they prepend a new group to the action menu — see
	// menuLayout. nil / empty when the user hasn't configured any.
	customActions []customactions.Action

	// finder owns the project-wide file-search index and its
	// background-build goroutine ("Esc p" or ≡ → Find file). The
	// transient UI state of an open finder lives in finderModal.
	finder *finder.Finder

	// sessionStore is the workspace state read from state.json: the
	// recent-folders queue and, per folder, the tabs it had open. Held
	// as a live value so the recent list can be pruned and re-saved
	// without a re-read; written at startup, on a prune, and on Close.
	// See folder.go.
	sessionStore *session.Store

	// sessionEnabled is the "session" config preference: whether opening
	// a folder reopens its tabs. Folders are recorded either way — the
	// recent list is a separate feature reading the same file.
	sessionEnabled bool

	// recentFiles is the live MRU ring for THIS folder, most recent
	// first: every file made active since the editor opened, seeded from
	// the store at startup and written back into it on Close. Held on the
	// App rather than read out of sessionStore on demand because it is
	// touched on every tab switch and pruned in place, and the store
	// hands its entries out by value. See recentfiles.go.
	recentFiles []string

	// remote is the `ced --remote` / `ced --wait` listener: the socket
	// another ced hands a file to, plus the clients blocked waiting for
	// a tab to close. See remote.go.
	remote remoteState

	// cats is the host-multiplexer integration: the capability probe, the
	// control-socket client (nil below Tier 1), the event stream, and the
	// hook reporter that lets the editor page the user through cats'
	// notification story. The zero value is Tier 0 — an editor in any
	// other terminal — so nothing here needs an "enabled" flag. See
	// cats_glue.go and internal/cats.
	cats catsState

	// hostIdentWrite emits an escape sequence on the tty, identifying
	// this editor to the hosting terminal/mux (OSC 7 cwd + OSC 2 title
	// — see hostident.go). nil disables the feature entirely: New()
	// installs the real /dev/tty writer, tests leave it nil or install
	// a capture. The three lastIdent* fields plus identSent are the
	// change key that keeps the after-event check allocation-free.
	hostIdentWrite  func(string) error
	lastIdentPath   string
	lastIdentDirty  bool
	lastIdentHasTab bool
	identSent       bool

	// nextRoot is the folder the user asked to switch to. Setting it
	// alongside quit asks the process to tear this App down and build a
	// fresh one rooted there; main reads it via NextRoot after Run
	// returns. A root switch is a restart because everything derived
	// from rootDir would otherwise have to be re-derived by hand — see
	// the header comment in folder.go.
	nextRoot string

	quit bool
}

// New initialises the screen and mouse, builds the file tree at rootDir,
// and returns an App ready to Run.
func New(rootDir string) (*App, error) {
	// Anchor the workspace to an absolute path up front. Everything
	// derived from rootDir inherits it — tree node paths, tab paths,
	// git invocations — and two subsystems break outright on relative
	// paths: the LSP layer (a rootUri/didOpen built from "." is a
	// malformed file:// URI, and gopls then publishes diagnostics
	// keyed by absolute paths that never match the relative tab
	// paths), and anything that outlives a cwd change (the embedded
	// grsh terminal's `cd` chdirs the whole process by design).
	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}
	scr, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := scr.Init(); err != nil {
		return nil, err
	}
	scr.EnableMouse(tcell.MouseButtonEvents | tcell.MouseDragEvents | tcell.MouseMotionEvents)
	// Bracketed paste: the terminal wraps a paste in start/end markers so
	// the editor can insert the content verbatim instead of replaying it
	// as keystrokes (which would rewrite pasted tabs to IndentUnit and run
	// each rune through the leader table). See textpaste.go. Terminals
	// that don't support it simply ignore the enable sequence — the paste
	// then arrives as raw keys, the same as before, so it degrades safely.
	scr.EnablePaste()
	// Focus reporting: the terminal tells us when the user leaves the
	// window, which is when a pending auto-save should be flushed rather
	// than left sitting on a countdown (autosave.go). Best-effort by
	// construction — tcell only emits CSI ?1004h on a mouse-capable /
	// XTerm-like terminal, macOS Terminal.app never reports focus at all,
	// and tmux needs `focus-events on`. A terminal that doesn't answer
	// simply never sends the event and the idle timer stays the only
	// path, which is exactly why the flush must never BE the only path.
	// No DisableFocus in Close: Fini restores the terminal's modes, the
	// same reason EnablePaste has no counterpart there.
	scr.EnableFocus()

	th := theme.Default()
	scr.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
	scr.Clear()

	tree, err := filetree.New(rootDir)
	if err != nil {
		scr.Fini()
		return nil, err
	}

	a := &App{
		screen:         scr,
		theme:          th,
		rootDir:        rootDir,
		tree:           tree,
		hoveredMenuRow: -1,
		sidebarShown:   true,
		sidebarWidth:   defaultSidebarWidth,
	}
	a.setActiveFolder(tree.Root.Path)
	a.loadUserConfig()
	// Record the visit before anything else can fail: this is what makes
	// `ced --last` and the recent-folders list correct even for a run
	// that ends in a crash. The tab list is written at Close; being HERE
	// at all is written now. See folder.go.
	a.loadSessionStore()
	// The MCP inventory is read at startup but nothing is SPAWNED here:
	// the chat agent needs the declaration at its own start, and ced's
	// own connections wait for a deliberate ≡ action. See mcp.go.
	a.loadMCPConfig()
	// Skills are three directory scans of small markdown files — cheap
	// enough to do eagerly so the ≡ row can carry a count, and (like the
	// MCP inventory) nothing is spawned, connected, or run by reading it.
	a.loadSkills()
	a.refreshGitStatus()
	a.loadCustomActions()
	// Plugin manifests are read at startup, but — like the MCP
	// inventory — nothing in them is RUN here. The first command fires
	// when a file opens, a file saves, or the user picks a row. See
	// plugins.go.
	a.loadPlugins()
	// Seed the fold default AFTER custom actions load so the synthetic
	// "Custom" section is folded too. Every section starts contracted; the
	// pinned command palette and the expand-all button keep everything one
	// click away.
	a.seedMenuFoldDefault()
	a.welcomeIfQuiet()
	a.startTreeRefresh()
	// Bind the remote-open socket before any file is opened, so a
	// `ced --wait` racing this startup finds a listener rather than
	// falling back to a second editor. Nothing is spawned and nothing
	// connects out — see remote.go for the silent-degradation contract.
	a.startRemote()
	// Start the Copilot sidecar eagerly (async, no-op when disabled or
	// not installed) rather than on first file open: its only phase-1
	// job is auth state, and knowing it at startup makes the ≡ labels
	// and Sign in flow honest from the first click.
	a.copilotEnsureStarted()
	// Reopen the tabs this folder had last time (folder.go). After the
	// integrations are up so restored tabs get the same didOpen / hook
	// treatment a clicked one would, and BEFORE main opens any file
	// named on the command line — an explicit `ced foo.go` must end up
	// on foo.go, not on whatever tab was active a week ago.
	a.restoreSession()
	// Identify ourselves to the hosting terminal (OSC 7 cwd + OSC 2
	// title — see hostident.go). After restoreSession so the first
	// title names the restored active file rather than flashing the
	// bare folder name for one frame.
	a.hostIdentInit()
	// And identify ourselves to the cats host, if we are in one: the env
	// sniff is free, the socket probe rides a goroutine, and everything
	// about this degrades to the Tier-0 editor you get in any other
	// terminal. Beside hostIdentInit because they are the same act at two
	// depths — the escape sequences say what file this is, the hook says
	// what the editor is doing about it. See cats_glue.go.
	a.catsInit()
	// Kick off the project file index in the background so that by
	// the time the user hits Esc-p (or ≡ → Find file) the modal can
	// open with results already in hand. On a 50k-file repo this
	// takes ~150ms; the user pays it once at startup instead of
	// when they're trying to navigate.
	a.finder = finder.New(rootDir)
	scr2 := a.screen
	a.finder.Rebuild(func() {
		_ = scr2.PostEvent(&finderRebuiltEvent{when: time.Now()})
	})
	return a, nil
}

// loadCustomActions reads the user's actions.json (if any) and stores
// the parsed list on the App. Failures are surfaced as a status flash
// so a typo in the config file isn't silently swallowed, but they
// don't block startup — the editor still opens with no custom actions
// in the menu.
func (a *App) loadCustomActions() {
	path := customactions.DefaultPath()
	actions, err := customactions.Load(path)
	if err != nil {
		a.flash("custom actions: " + err.Error())
		return
	}
	a.customActions = actions
}

// loadUserConfig reads ~/.config/ced/config.json (if any),
// resolves the Nerd Fonts auto/on/off mode to a concrete bool via
// icons.Resolve, and stamps the result onto the file tree so the
// next render starts drawing glyphs (or doesn't). A malformed
// config flashes a status message but never blocks startup — the
// editor falls back to Defaults() and keeps going.
func (a *App) loadUserConfig() {
	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil {
		a.flash("config: " + err.Error())
	}
	if a.tree != nil {
		a.tree.IconsEnabled = icons.Resolve(cfg.Icons)
		a.tree.ExecMarks = cfg.ExecMarks
	}
	a.treeAutoFit = cfg.TreeAutoFit
	a.scrollbarShown = cfg.Scrollbar
	a.autoSaveEnabled = cfg.AutoSave
	a.autoSaveDelay = cfg.AutoSaveDelay
	a.wordHLEnabled = cfg.WordHL
	a.applyWordHighlight() // no-op at startup; matters when the config is re-read
	a.termDockLeft = cfg.TermDock == userconfig.TermDockLeft
	a.findAllDockRight = cfg.FindAllDock == userconfig.FindAllDockRight
	a.copilot.enabled = cfg.Copilot
	a.copilot.suggest = cfg.Suggestions
	a.chat.modelPref = cfg.ChatModel
	// Resolve the persisted backend id through the registry — an
	// unknown id (newer/older ced) falls back to the default silently,
	// the same stale-preference rule as chatmodel.
	a.chat.agent = chatAgentByID(cfg.ChatAgent)
	a.chat.autoContext = cfg.ChatContext
	a.chat.writeEnabled = cfg.ChatWrite
	a.commitTrailer = cfg.CommitMsgTrailer
	a.plugins.enabled = cfg.Plugins
	a.sessionEnabled = cfg.Session
	a.remote.enabled = cfg.Remote
	// Themes last: loadThemes and applyThemeName both flash on failure,
	// and a color problem is the least urgent thing in this function —
	// letting it land last keeps a more important message visible.
	// persist=false: we're reading the preference, not restating it.
	a.loadThemes()
	// A named theme in the config is a CHOICE; an empty one is the absence
	// of a choice, which is what lets the cats host theme step in
	// (catstheme.go). Recorded before applying, since applyThemeName's own
	// persist path sets the same flag for the picker.
	a.themePinned = strings.TrimSpace(cfg.Theme) != ""
	a.applyThemeName(cfg.Theme, false)
}

// refreshGitStatus re-runs `git status --porcelain` against the project
// root and stamps the resulting dirty-paths sets onto the file tree, so
// changed files render in the Modified color on the next draw. It's
// cheap (a couple of forks) but not free — we only call it from the
// 10-second tree-refresh tick and right after our own file operations,
// not on every keystroke. A non-git project leaves the tree's dirty
// maps empty, which the renderer treats as "everything clean".
func (a *App) refreshGitStatus() {
	if a.tree == nil {
		return
	}
	st := loadGitStatus(a.rootDir)
	a.gitIsRepo = st.IsRepo
	a.gitHasStaged = st.HasStaged
	a.gitHasStash = st.HasStash
	a.gitStagedFiles = st.StagedFiles
	a.gitConflicted = st.ConflictedFiles
	// The tracking facts follow the same snapshot, including the reset
	// on a non-repo: a stale "↑3" outliving the folder it described
	// would be a status bar lying about a repo the user has left.
	a.gitUpstream = st.Upstream
	a.gitAhead = st.Ahead
	a.gitBehind = st.Behind
	a.gitHasRemote = st.HasRemote
	if !st.IsRepo {
		a.tree.DirtyFiles = nil
		a.tree.DirtyFolders = nil
		a.gitBranch = ""
	} else {
		a.tree.DirtyFiles = st.DirtyFiles
		a.tree.DirtyFolders = dirtyFolderSet(st.DirtyFiles, a.rootDir)
		a.gitBranch = st.Branch
	}
	// The git panel mirrors the same status snapshot — refreshing it
	// here (a no-op while collapsed) means every path that keeps the
	// tree's dirty colors honest keeps the panel honest too: the 10s
	// tick, saves, file ops, and finished git commands.
	a.refreshGitPanelFiles()
	// The log panel rides the same pipeline (no-op while collapsed), so
	// a commit, cherry-pick, or outside-the-editor fetch shows up on the
	// next tick without a dedicated refresh path.
	a.refreshGitLogCommits()
}

// startTreeRefresh launches a goroutine that posts a treeRefreshEvent every
// treeRefreshInterval. The main event loop reacts by calling tree.Refresh,
// which keeps the sidebar in sync with on-disk changes from outside the
// editor (git, mv, another tmux pane, etc.).
func (a *App) startTreeRefresh() {
	a.treeRefreshStop = make(chan struct{})
	stop := a.treeRefreshStop
	scr := a.screen
	go func() {
		ticker := time.NewTicker(treeRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case t := <-ticker.C:
				_ = scr.PostEvent(&treeRefreshEvent{when: t})
			}
		}
	}()
}

// stopTreeRefresh signals the background tree-refresh goroutine to exit.
// Safe to call multiple times.
func (a *App) stopTreeRefresh() {
	if a.treeRefreshStop != nil {
		close(a.treeRefreshStop)
		a.treeRefreshStop = nil
	}
}

// Close releases the terminal back to the user. Always call this before exit.
func (a *App) Close() {
	// Capture the workspace before anything is torn down — Close runs on
	// both exits (a plain quit and a folder switch), so recording here
	// means neither path has to remember to. See folder.go.
	a.recordSession()
	// Release any `ced --wait` client first: each one is a shell prompt
	// in another pane that will not come back until we say so, and a
	// folder switch runs this path too.
	a.stopRemote()
	a.stopTreeRefresh()
	a.stopAutoScroll()
	a.stopAutoSave()
	a.stopSyntaxSettle()
	a.stopCaretBlink()
	a.lspShutdown()
	a.copilotShutdown()
	a.chatShutdown()
	a.mcpShutdown()
	// Stop the cats event stream and hand the pane back before the tty
	// work below: the release is a socket write that must not race a
	// screen being finalized, and a pane still badged "ced" after ced
	// exited is a lie the host has no way to correct.
	a.catsClose()
	// Give the terminal its previous title back (the pop matching
	// hostIdentInit's push) while the tty is still ours.
	a.hostIdentClose()
	if a.screen != nil {
		a.screen.Fini()
	}
}

// Run is the editor's main event loop. It blocks on PollEvent, dispatches
// each event, redraws, and exits when a.quit is set.
func (a *App) Run() error {
	a.width, a.height = a.screen.Size()
	a.draw()
	a.screen.Show()

	for !a.quit {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		a.handleEvent(ev)
		a.draw()
		a.screen.Show()
	}
	return nil
}

// handleEvent routes a tcell event to its specific handler.
func (a *App) handleEvent(ev tcell.Event) {
	switch e := ev.(type) {
	case *tcell.EventResize:
		a.width, a.height = a.screen.Size()
		a.screen.Sync()
		// A dwell tooltip describes the cell under the pointer, and a
		// resize moved what is in that cell — so it is now a claim about
		// the wrong symbol. Dismiss rather than reposition.
		a.closeHoverDwell()
	case *tcell.EventKey:
		a.handleKey(e)
	case *tcell.EventPaste:
		a.handlePaste(e)
	case *tcell.EventMouse:
		a.handleMouse(e)
	case *tcell.EventFocus:
		// Read only Focused. tcell's NewEventFocus leaves the embedded
		// *EventTime nil (unlike the key and mouse constructors), so
		// calling When() on one of these panics.
		a.autoSaveOnFocusChange(e.Focused)
	case *autoScrollEvent:
		a.handleAutoScroll()
	case *autoSaveEvent:
		a.handleAutoSave()
	case *syntaxSettleEvent:
		a.handleSyntaxSettle()
	case *projectSearchEvent:
		a.handleProjectSearch(e)
	case *treeRefreshEvent:
		a.refreshTreeNow()
	case *remoteOpenEvent:
		a.handleRemoteOpen(e)
	case *caretBlinkEvent:
		a.handleCaretBlink()
	case *whichKeyEvent:
		a.handleWhichKeyTick(e)
	case *hoverDwellEvent:
		a.handleHoverDwellTick(e)
	case *gitDiffEvent:
		a.handleGitDiff(e)
	case *gitBlameEvent:
		a.handleGitBlame(e)
	case *blameTickEvent:
		a.handleBlameTick()
	case *customActionDoneEvent:
		a.handleCustomActionDone(e)
	case *pluginCmdDoneEvent:
		a.handlePluginCmdDone(e)
	case *pluginHookDoneEvent:
		a.handlePluginHookDone(e)
	case *pluginDecoEvent:
		a.handlePluginDeco(e)
	case *pluginEditEvent:
		a.handlePluginEditTick(e)
	case *zipDoneEvent:
		a.handleZipDone(e)
	case *gonotesDoneEvent:
		a.handleGonotesDone(e)
	case *pasteDoneEvent:
		a.handlePasteDone(e)
	case *gitCmdDoneEvent:
		a.handleGitCmdDone(e)
	case *gitStatusReportEvent:
		a.handleGitStatusReport(e)
	case *gitPanelDiffEvent:
		a.handleGitPanelDiff(e)
	case *gitLogShowEvent:
		a.handleGitLogShow(e)
	case *gitLogFilterTickEvent:
		a.handleGitLogFilterTick(e)
	case *gitLogFilterEvent:
		a.handleGitLogFilterResult(e)
	case *gitCommitDiffEvent:
		a.handleGitCommitDiff(e)
	case *gitCommitReceiptEvent:
		a.handleGitCommitReceipt(e)
	case *commitReceiptExpireEvent:
		a.handleCommitReceiptExpire(e)
	case *gitPushRefsEvent:
		a.handleGitPushRefs(e)
	case *termOutputEvent:
		a.handleTermOutput()
	case *termDoneEvent:
		a.handleTermDone(e)
	case *formatDoneEvent:
		a.handleFormatDone(e)
	case *finderRebuiltEvent:
		// The background indexer just finished. Re-run the visible
		// query so "Indexing…" gives way to real results without
		// the user having to type or wait for the next keystroke.
		// A sourced palette re-collects too — its file rows come from
		// the same index. Pickers (sourced=false) keep their
		// caller-owned items.
		if m, ok := a.modal.(*finderModal); ok {
			m.refresh(a)
		}
		if m, ok := a.modal.(*paletteModal); ok && m.sourced {
			m.collectItems(a)
			m.refresh()
		}
	case *lspReadyEvent:
		a.handleLSPReady(e)
	case *lspExitEvent:
		a.handleLSPExit()
	case *lspDiagsEvent:
		a.handleLSPDiags(e)
	case *lspSyncEvent:
		a.handleLSPSync(e)
	case *lspDefinitionEvent:
		a.handleLSPDefinition(e)
	case *lspHoverEvent:
		a.handleLSPHover(e)
	case *lspSymbolsEvent:
		a.handleLSPSymbols(e)
	case *lspReferencesEvent:
		a.handleLSPReferences(e)
	case *lspRenameEvent:
		a.handleLSPRename(e)
	case *lspCodeActionsEvent:
		a.handleLSPCodeActions(e)
	case *lspApplyEditEvent:
		a.handleLSPApplyEdit(e)
	case *lspCommandEvent:
		a.handleLSPCommand(e)
	case *lspSignatureEvent:
		a.handleLSPSignature(e)
	case *completionTickEvent:
		a.handleCompletionTick(e)
	case *lspCompletionEvent:
		a.handleLSPCompletion(e)
	case *lspCompletionResolveEvent:
		a.handleLSPCompletionResolve(e)
	case *copilotReadyEvent:
		a.handleCopilotReady(e)
	case *copilotExitEvent:
		a.handleCopilotExit()
	case *copilotStatusEvent:
		a.handleCopilotStatus(e)
	case *copilotSignInEvent:
		a.handleCopilotSignIn(e)
	case *copilotSignInDoneEvent:
		a.handleCopilotSignInDone(e)
	case *copilotSignOutDoneEvent:
		a.handleCopilotSignOutDone(e)
	case *copilotCompletionTickEvent:
		a.handleCopilotCompletionTick(e)
	case *copilotCompletionEvent:
		a.handleCopilotCompletion(e)
	case *chatReadyEvent:
		a.handleChatReady(e)
	case *chatExitEvent:
		a.handleChatExit(e)
	case *chatUpdateEvent:
		a.handleChatUpdate(e)
	case *chatTurnDoneEvent:
		a.handleChatTurnDone(e)
	case *chatPermRequestEvent:
		a.handleChatPermRequest(e)
	case *chatFSRequestEvent:
		a.handleChatFSRequest(e)
	case *chatModelSetEvent:
		a.handleChatModelSet(e)
	case *mcpReadyEvent:
		a.handleMCPReady(e)
	case *mcpFailedEvent:
		a.handleMCPFailed(e)
	case *mcpExitEvent:
		a.handleMCPExit(e)
	case *mcpToolResultEvent:
		a.handleMCPToolResult(e)
	case *catsEvent:
		a.handleCatsEvent(e)
	}
	// After every dispatch, let the LSP layer notice buffer edits and
	// (re-)arm its didChange debounce. Runs unconditionally because
	// edits arrive through many paths (keys, paste, modals, reloads on
	// the refresh tick) and this is a few integer compares when idle.
	a.lspAfterEvent()
	// And the completion popup: keep an open list honest against the
	// buffer that just moved (re-filter, or close if the caret left the
	// token), then decide whether the edit that just happened typed a
	// trigger character. See completion.go.
	a.completionAfterEvent()
	// The Copilot twin: drop a ghost the event invalidated and re-arm
	// the inline-completion debounce on fresh edits. See copilot_ghost.go.
	a.copilotAfterEvent()
	// Same trick for auto-save: any event that mutated a buffer
	// re-arms the idle countdown. See autosave.go.
	a.autoSaveAfterEvent()
	// And for syntax highlighting: an intra-line edit defers the re-lex,
	// so something has to wake the loop when the typing stops or the new
	// text would keep the old colors until the next unrelated event.
	// See syntax.go.
	a.syntaxAfterEvent()
	// And for chat permissions: a queued session/request_permission
	// resurfaces as soon as the modal slot frees up. See
	// copilot_chat_perm.go.
	a.chatPermAfterEvent()
	// And plugins: an edit re-arms the debounce that re-runs whatever
	// linters and scanners the user hung on the "edit" event. Returns
	// immediately unless a plugin actually listens for it, so an editor
	// with no edit-triggered plugins never wakes on a timer. See
	// plugindeco.go.
	a.pluginsAfterEvent()
	// And the secondary carets' blink: armed while they exist, disarmed
	// the moment they don't, so an idle single-caret editor never wakes
	// on a timer. See multicaret.go.
	a.caretBlinkAfterEvent()
	// And the host-identity title: the active tab can change through
	// many paths (clicks, leaders, remote opens, closes), so the title
	// is reconciled here instead of at each of them. See hostident.go.
	a.hostIdentAfterEvent()
	// And a deferred disk-conflict prompt: detection happens on a
	// background tick (or inside a blocked save) for whichever tab it
	// happens to, and the question is raised here — once that tab is
	// frontmost and the modal slot is free. See reconcile.go.
	a.conflictAfterEvent()
	// And the blame column: an edit moves the lines out from under the
	// annotations, so the settle timer re-blames the buffer once the
	// typing stops. Cheap to skip — one map read — while the layer is
	// off, which is most of the time. See gitblame.go.
	a.blameAfterEvent()
	// LAST: the cats host's picture of what this editor is doing — idle,
	// working, or blocked on a question the user didn't ask for. Last
	// because the two hooks above it can RAISE that question (a deferred
	// conflict, a queued agent permission), and a report taken before them
	// would describe the editor as idle in the same pass that blocked it.
	// Reported only on a transition, so the notification channel carries
	// events a human would describe rather than one per keystroke. See
	// cats_glue.go.
	a.catsAfterEvent()
}

// workspaceChanged re-syncs every subsystem that mirrors on-disk
// project state — the file tree (preserving expansion state), git
// status, and the finder index — after any mutation: create / rename /
// delete, a formatter-config install, or an external change. Call this
// instead of the individual refreshes; when each call site spelled out
// the trio by hand, forgetting one was an easy stale-UI bug (the
// formatter-install path really did miss the finder for a while).
func (a *App) workspaceChanged() {
	a.tree.Refresh()
	a.refreshGitStatus()
	a.invalidateFinder()
}

// refreshTreeNow re-runs the same refresh pipeline the 10s timer
// fires: everything workspaceChanged covers, plus reconciling open
// tabs with disk (silent reload / dirty warning / DiskGone). Called
// from the periodic event and from runCustomAction's success path so
// a Copy-from-remote action's output is visible immediately instead
// of after the next tick.
func (a *App) refreshTreeNow() {
	a.workspaceChanged()
	a.reconcileOpenTabsWithDisk()
	a.requestOpenTabDiffs()
}

// handleCustomActionDone surfaces the result of an async custom-action
// run. Success flashes a brief confirmation and forces a sidebar
// refresh so a freshly-pulled file appears in the file tree without
// waiting for the 10-second auto-refresh tick. Failure opens an info
// modal with the captured stderr — the prior 1-line flash truncated
// scp's actual diagnostics, which is exactly the case where the user
// most needs to read them.
func (a *App) handleCustomActionDone(e *customActionDoneEvent) {
	if e.err != nil {
		title := "Action failed: " + e.label
		body := splitErrorOutput(e.err, e.output)
		a.openInfo(title, body)
		return
	}
	a.flash(e.label + " — done")
	a.refreshTreeNow()
}

// splitErrorOutput formats the action's captured output for the info
// modal: an opening line summarising the exit error, then up to a
// handful of lines of trimmed stderr, with the actions.log path as
// the closing line so the user knows where to find the full record.
// Pulled out so handleCustomActionDone reads as the routing decision
// it really is.
func splitErrorOutput(runErr error, out []byte) []string {
	body := errorBodyLines(runErr, out, "… (truncated; see actions.log)")
	if logPath := customactions.LogPath(); logPath != "" {
		body = append(body, "", "Full output: "+logPath)
	}
	return body
}

// errorBodyLines is the shared core of failed-subprocess reporting: an
// opening line summarising the exit error, then up to a handful of
// lines of trimmed combined output, with truncNote appended when the
// output overflows the cap. Split from splitErrorOutput so the git
// command handler can reuse the shape without inheriting the
// custom-actions log-path footer (git runs aren't logged there).
func errorBodyLines(runErr error, out []byte, truncNote string) []string {
	const maxLines = 8
	const maxLineWidth = 78

	body := []string{strings.TrimSpace(runErr.Error())}
	captured := strings.TrimRight(string(out), "\n")
	if captured != "" {
		body = append(body, "")
		count := 0
		for _, ln := range strings.Split(captured, "\n") {
			ln = strings.TrimRight(ln, "\r")
			if runeLen(ln) > maxLineWidth {
				ln = string([]rune(ln)[:maxLineWidth-1]) + "…"
			}
			body = append(body, ln)
			count++
			if count >= maxLines {
				body = append(body, truncNote)
				break
			}
		}
	}
	return body
}

// reconcileOpenTabsWithDisk moved to reconcile.go, where the disk /
// buffer conflict matrix, the save guard that shares its measurement,
// and the prompt that resolves both now live together.

// -----------------------------------------------------------------------------
// Layout helpers
// -----------------------------------------------------------------------------

// sidebarW is the effective width of the sidebar block (file tree +
// splitter): zero when hidden, a.sidebarWidth otherwise. Every layout
// helper and click router goes through this so toggling/resizing the
// panel reshapes the entire UI in one place.
func (a *App) sidebarW() int {
	if !a.sidebarShown {
		return 0
	}
	return a.sidebarWidth
}

// treeOnRight reports whether the file tree lives on the RIGHT edge —
// true whenever something else claims the left one: the left-docked
// terminal layout, or the Copilot chat panel (which docks left with
// the tree on the right by explicit owner preference). Every layout
// helper that used to pivot on termDockLeft alone pivots on this, so
// both left-strip features flip the whole UI through one predicate.
func (a *App) treeOnRight() bool {
	return a.termDockLeft || a.chat.open
}

// leftBlockW is how many columns the left-docked block consumes: the
// sidebar in the classic layout, the chat strip or the terminal strip
// (whichever is open — the left edge is single-occupancy) in the
// flipped layouts. The tab bar, editor, find bar, and bottom panels
// all start to the right of this.
func (a *App) leftBlockW() int {
	if a.chat.open {
		return a.chatStripW()
	}
	if a.termDockLeft {
		return a.termStripW()
	}
	return a.sidebarW()
}

// rightBlockW mirrors leftBlockW for the right edge: zero in the
// classic layout, the sidebar block in the flipped ones.
func (a *App) rightBlockW() int {
	if a.treeOnRight() {
		return a.sidebarW()
	}
	return 0
}

// sidebarRect returns the file tree's render rectangle (one column
// narrower than the sidebar block — the column nearest the editor
// belongs to the resize splitter). Zero width when hidden. In the
// flipped layout the block hugs the right edge with the splitter on
// its left.
func (a *App) sidebarRect() (x, y, w, h int) {
	sw := a.sidebarW()
	if sw <= 0 {
		return 0, 0, 0, 0
	}
	if a.treeOnRight() {
		return a.width - sw + 1, 0, sw - 1, a.height - 1
	}
	return 0, 0, sw - 1, a.height - 1
}

// splitterX returns the x coordinate of the sidebar's resize splitter
// column, or -1 when the sidebar is hidden (no splitter to draw or
// click). Classic layout: the block's rightmost column; flipped: its
// leftmost.
func (a *App) splitterX() int {
	if !a.sidebarShown {
		return -1
	}
	if a.treeOnRight() {
		return a.width - a.sidebarWidth
	}
	return a.sidebarWidth - 1
}

// inSidebarBlock reports whether column x falls inside the sidebar
// block (tree + splitter), whichever edge it is docked to. The click
// and scroll routers use this instead of comparing against raw widths
// so they stay layout-agnostic.
func (a *App) inSidebarBlock(x int) bool {
	sw := a.sidebarW()
	if sw <= 0 {
		return false
	}
	if a.treeOnRight() {
		return x >= a.width-sw
	}
	return x < sw
}

// resizeSidebar applies the user's desired sidebar width while clamping it
// to a sensible range — the file tree stays wide enough to read names and
// the editor keeps at least minEditorAfterDrag columns (after whatever a
// left-docked terminal strip already claimed). Tiny windows that can't
// satisfy both fall back to the minimum and let the editor shrink.
func (a *App) resizeSidebar(target int) {
	if target < minSidebarWidth {
		target = minSidebarWidth
	}
	max := a.width - minEditorAfterDrag
	if a.treeOnRight() {
		// Whichever strip owns the left edge (chat or terminal) has
		// first claim on those columns.
		max -= a.leftBlockW()
	}
	if max < minSidebarWidth {
		max = minSidebarWidth
	}
	if target > max {
		target = max
	}
	a.sidebarWidth = target
}

// tabBarRect returns the tab bar's screen rectangle (one row tall),
// spanning the editor column band between the docked blocks.
func (a *App) tabBarRect() (x, y, w, h int) {
	lw := a.leftBlockW()
	return lw, 0, a.width - lw - a.rightBlockW(), 1
}

// editorBandRows is how many rows the editor body gets from the window
// once the pinned strips below it have taken theirs. When the find bar
// is open, one row comes out of the bottom — the bar is pinned directly
// above the status bar. The git panel takes its rows out of the bottom
// too, stacking above the find bar; a bottom-docked terminal does the
// same, while a left-docked one costs columns instead (via leftBlockW).
//
// Split out from editorRect for one reason: the resizable panels clamp
// their own heights against "what would the editor have left?", and so
// does the Find-all popup — none of them can ask editorRect, which
// already subtracts them.
func (a *App) editorBandRows() int {
	h := a.height - 2
	h -= a.findBarRows()
	if a.gitPanel.open {
		h -= a.gitPanelHeight()
	}
	if a.gitLog.open {
		h -= a.gitLogHeight()
	}
	if a.compare.open {
		h -= a.comparePanelHeight()
	}
	if a.problems.open {
		h -= a.problemsHeight()
	}
	if a.term.open && !a.termDockLeft {
		h -= a.termPanelHeight()
	}
	return h
}

// editorBandCols is the editor body's column band before the Find-all
// list (the only surface that can take columns from inside it) claims
// any — the horizontal twin of editorBandRows, and it exists for the
// same reason: the claimant can't ask editorRect, which already
// subtracts it.
func (a *App) editorBandCols() int {
	return a.width - a.leftBlockW() - a.rightBlockW()
}

// editorRect returns the editor body's screen rectangle — the column
// band between the docked side blocks, between the tab bar and the
// status bar, minus whatever the strips around it claimed.
//
// The Find-all popup is the only one that costs the editor rows off the
// TOP or columns off the RIGHT (its two dock modes — see findall.go):
// it displaces the editor rather than floating over it, so the
// shortened viewport still scrolls the line it's previewing into view.
// Everything that positions itself inside the editor reads the x/y
// returned here, so the offsets cost those call sites nothing.
func (a *App) editorRect() (x, y, w, h int) {
	lw := a.leftBlockW()
	top := a.findAllPanelHeight()
	h = a.editorBandRows() - top
	// The scrollbar takes its column from the same edge, INSIDE the
	// Find-all dock's: the bar belongs to the editor, so it stays welded
	// to whichever edge the editor ended up with. See scrollbar.go.
	w = a.editorBandCols() - a.findAllPanelWidth() - a.scrollbarCols()
	return lw, 1 + top, w, h
}

// statusRect returns the status bar's screen rectangle (full-width bottom row).
func (a *App) statusRect() (x, y, w, h int) {
	return 0, a.height - 1, a.width, 1
}

// editorSize returns just the (width, height) of the editor body. Used by
// keyboard handlers that need to compute page-up / page-down deltas.
func (a *App) editorSize() (int, int) {
	_, _, w, h := a.editorRect()
	return w, h
}

// menuButtonRect returns the on-screen rectangle of the ≡ icon in the tab
// bar. Click hit-tests in tabBarClick consult this directly. When the
// left block is absent the icon shifts left to fill the corner.
func (a *App) menuButtonRect() (x, y, w, h int) {
	return a.leftBlockW(), 0, menuButtonWidth, 1
}

// menuPinnedRows is the fixed header of the action modal — top border,
// title, and the divider under it — that never scrolls. Content rows
// start at this relY; the scrollable band on screen runs from
// my+menuPinnedRows to my+mh-2 (the row above the bottom border).
const menuPinnedRows = 3

// menuModalRect returns the on-screen rectangle of the action modal,
// centered in the window. Height is derived from the current layout
// so adding custom actions grows the modal automatically — but clamped
// to the window, with the overflow reachable by scrolling (see
// menuScrollOffset): the menu outgrew small terminals, and drawing rows
// off-screen made the bottom groups unreachable.
func (a *App) menuModalRect() (x, y, w, h int) {
	w = modalWidth
	_, _, h = a.menuLayout()
	if h > a.height {
		h = a.height
	}
	x = (a.width - w) / 2
	y = (a.height - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// menuMaxScroll is the largest useful menu scroll offset: the number of
// layout rows that don't fit the clamped modal. 0 when everything fits.
func (a *App) menuMaxScroll() int {
	_, _, layoutH := a.menuLayout()
	_, _, _, mh := a.menuModalRect()
	if max := layoutH - mh; max > 0 {
		return max
	}
	return 0
}

// menuScrollOffset is the effective scroll — the stored offset re-
// clamped against the current geometry. Draw, hit-testing, and the
// scroll mutators all read this single source so a stale menuScroll
// (window grew mid-menu) can never make them disagree.
func (a *App) menuScrollOffset() int {
	s := a.menuScroll
	if max := a.menuMaxScroll(); s > max {
		s = max
	}
	if s < 0 {
		s = 0
	}
	return s
}

// scrollMenu moves the menu's scroll offset by delta rows, clamped to
// the valid band. Wheel-driven; a no-op when the whole menu fits.
func (a *App) scrollMenu(delta int) {
	a.menuScroll = a.menuScrollOffset() + delta
	if a.menuScroll < 0 {
		a.menuScroll = 0
	}
	if max := a.menuMaxScroll(); a.menuScroll > max {
		a.menuScroll = max
	}
}

// menuItemIndexAt maps a screen position to the index of the menu item
// drawn there, honoring the scroll offset, or -1 for anything that
// isn't an item row (borders, title, dividers, outside the modal).
// The one geometry source for both hover tracking and click dispatch —
// if they computed the mapping separately, a scroll bug would make the
// highlight and the click disagree about which row is which.
func (a *App) menuItemIndexAt(x, y int) int {
	mx, my, mw, mh := a.menuModalRect()
	if x < mx || x >= mx+mw || y < my+menuPinnedRows || y > my+mh-2 {
		return -1
	}
	relY := y - my + a.menuScrollOffset()
	items, _, _ := a.menuLayout()
	for i, item := range items {
		if item.relY == relY {
			return i
		}
	}
	return -1
}

// menuEnsureHoveredVisible scrolls the menu just enough to bring the
// keyboard-selected row into the visible band. Called only when the
// selection itself moves (Down/Up), never from draw — the same
// selection-change-only rule as the editor's cursorMoved flag and the
// git panel's list, so wheel-scrolling away from the highlight doesn't
// snap back.
func (a *App) menuEnsureHoveredVisible() {
	items, _, _ := a.menuLayout()
	if a.hoveredMenuRow < 0 || a.hoveredMenuRow >= len(items) {
		return
	}
	relY := items[a.hoveredMenuRow].relY
	_, _, _, mh := a.menuModalRect()
	scroll := a.menuScrollOffset()
	if relY-scroll < menuPinnedRows {
		scroll = relY - menuPinnedRows
	}
	if relY-scroll > mh-2 {
		scroll = relY - (mh - 2)
	}
	a.menuScroll = scroll
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// handleKey responds to keyboard events. There are intentionally no Ctrl-
// based shortcuts: every action lives behind the ≡ menu so the editor never
// fights the terminal (Ctrl-S/Q flow control) or a tmux/zellij prefix. The
// only "command" key is Esc, which closes the menu and acts as the leader
// for the hotkey table in leader.go (Esc s = Save, Esc u = Undo, etc.).
func (a *App) handleKey(ev *tcell.EventKey) {
	// While a bracketed paste is in flight the content arrives as key
	// events between the start/end markers. Divert them into the paste
	// buffer verbatim instead of interpreting them as shortcuts, leaders,
	// or IndentUnit-expanded tabs. See textpaste.go.
	if a.pasting {
		a.accumulatePaste(ev)
		return
	}
	// Typing dismisses the dwell tooltip and cancels any request behind
	// it: attention has moved to the keyboard, and a tooltip that popped
	// up mid-keystroke because the mouse happened to be resting on an
	// identifier is the exact behaviour that makes people turn hover off.
	// Non-consuming — the key then does what it always did.
	a.closeHoverDwell()
	// The commit receipt goes the same way and for the same reason: it
	// is chrome nobody asked for, so the first thing the user does next
	// takes it down — and, like the ghost text, it must never cost them
	// the keystroke that did it.
	a.closeCommitReceipt()
	// The active modal owns the keyboard while it's up. Each handler
	// understands Esc (cancel), Enter (submit / activate), and the keys
	// relevant to its layout (text editing for the prompt, arrow keys for
	// the context menu, etc.). The find bar isn't a modal — it's a
	// pinned strip — but it owns the keyboard the same way.
	if a.modal != nil {
		a.modal.handleKey(a, ev)
		return
	}
	if a.findOpen {
		a.handleFindKey(ev)
		return
	}
	// A focused field on the PINNED find-all panel owns the keys the
	// same way the find bar does — the panel itself is furniture (the
	// editor keeps the keyboard), but a box the user clicked into is a
	// box they're typing into. Esc inside drops back to the list focus.
	if a.findAllPin != nil && a.findAllPin.focus != findAllFocusList {
		a.findAllPin.handleFieldKey(a, ev)
		return
	}
	// Same rule for the git log's search bar: the panel itself has no
	// keyboard (it is furniture), but a field the user clicked into — or
	// opened with Esc-S — is a field they are typing into. Esc gives the
	// keyboard back, as does a click anywhere outside the panel.
	if a.gitLog.open && a.gitLog.filter.focused {
		a.handleGitLogFilterKey(ev)
		return
	}

	// A half-typed chord (Esc-a waiting for its second rune) claims the
	// next keystroke before anything else can. Checked ahead of the Esc
	// branch so Esc still means "drop that": handleChordKey disarms on a
	// non-rune and reports false, letting the normal Esc handling run.
	if a.handleChordKey(ev) {
		return
	}

	// The completion popup takes navigation, accept and dismiss off the
	// top of the router and lets everything else through — it is not a
	// modal, and the letters the user keeps typing are what narrow it.
	// Checked BEFORE the Esc branch so dismissing the list doesn't also
	// arm the leader, and after the chord check so a half-typed chord
	// still owns its second rune. See completion.go.
	if a.completionKey(ev) {
		return
	}
	if ev.Key() == tcell.KeyEsc {
		// Esc always dismisses a ghost suggestion — the one editing
		// gesture that clears it without moving the cursor. Purely a
		// side effect: the menu/leader behavior below runs regardless.
		a.copilotClearGhost()
		// Same deal for a chat transcript highlight: Esc is the
		// universal "drop that" gesture, and a stale highlight sitting
		// in the panel has no other way out.
		a.chatClearSelection()
		// …and for an armed compare panel: Esc is the universal "drop
		// that", and a mode still claiming the next paste is exactly the
		// thing a user reaches for Esc to get out of.
		a.compareCancelPaste()
		// …and for a column of extra carets. Also a side effect: the
		// menu / leader behavior below still runs, so Esc-Esc opens the
		// menu and Esc-s saves whether or not carets were dropped.
		a.clearCarets()
		// …and for a running pre-commit survey. It belongs with these
		// rather than inside the walk's own key handler because the
		// handler sits BELOW this block and so never sees an Esc — and
		// because a mode that has claimed n / p / space is exactly the
		// thing a user reaches for Esc to get out of. The reviewed
		// marks survive: they record what was read, not the mode that
		// recorded it, and the header button resumes from there.
		a.stopGitPanelWalk()
		// A real Esc always re-opens the full leader table — chain mode
		// (repeatable-only) is an artifact of the previous action. Note
		// whether the window was chain-armed before clearing: a chained
		// lastEscape must not read as the first tap of a double-Esc, or
		// "Esc z, Esc r" (undo then redo) would open the menu instead.
		wasChained := a.leaderChained
		a.leaderChained = false
		// Esc is the editor's only command key. Behavior:
		//   • menu open  → close it
		//   • menu shut  → open it on the SECOND Esc within doubleEscMs;
		//     a SINGLE Esc arms the leader table (see below).
		// A lone Esc that isn't followed by a leader binding within the
		// window is intentionally a no-op so the key still feels harmless
		// to mash.
		//
		// Alt+Esc IS a double-Esc. tmux holds a lone ESC for its
		// escape-time (500ms by default) waiting to disambiguate, so a
		// fast double-tap reaches us as one buffered "\x1b\x1b" write —
		// which tcell's parser folds into a single KeyEsc event carrying
		// ModAlt. Without this branch the menu is unreachable by
		// keyboard inside tmux, the editor's primary habitat.
		if ev.Modifiers()&tcell.ModAlt != 0 {
			if a.menuOpen {
				a.closeMenu()
			} else {
				a.openMenu()
			}
			a.lastEscape = time.Time{}
			return
		}
		if a.menuOpen {
			a.closeMenu()
			a.lastEscape = time.Time{}
			return
		}
		now := time.Now()
		if !wasChained && !a.lastEscape.IsZero() && now.Sub(a.lastEscape) < doubleEscMs {
			a.openMenu()
			a.lastEscape = time.Time{}
			return
		}
		// A visible which-key overlay makes Esc mean "drop that": close
		// it and disarm. Checked AFTER the double-Esc branch so Esc-Esc
		// keeps opening the menu even when the overlay got there first.
		if a.whichKey.open {
			a.closeWhichKey()
			a.clearChord()
			a.lastEscape = time.Time{}
			return
		}
		a.lastEscape = now
		// The hesitation timer: if this armed leader is still waiting in
		// ~350ms, the which-key overlay documents it (whichkey.go).
		a.armWhichKey()
		return
	}
	// Alt+Enter never reaches tcell as itself on a legacy terminal (tmux
	// included): the emulator writes ESC CR, and tcell's fold for
	// ESC + control-char reports it as the RUNE 'm' carrying
	// ModAlt|ModCtrl ('j' for the rare emulator that sends LF). Rewrite
	// that spelling into the KeyEnter+ModAlt event a CSI-u terminal
	// would have delivered, BEFORE the leader branches below — 'm' is a
	// bound leader rune, so without this the chord fired multicaret on
	// the buffer behind whatever surface the user was actually typing
	// into. Downstream, every consumer (the chat composer's newline
	// chord first among them) then sees one spelling of the gesture.
	if ev.Key() == tcell.KeyRune &&
		ev.Modifiers()&tcell.ModAlt != 0 && ev.Modifiers()&tcell.ModCtrl != 0 &&
		(ev.Rune() == 'm' || ev.Rune() == 'j') {
		ev = tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModAlt)
	}
	// Esc-leader hotkey: if Esc was pressed within doubleEscMs and this
	// key is bound in the leader table, fire the action and consume the
	// keystroke. Unbound keys fall through to normal handling so a stray
	// Esc doesn't swallow the next character the user types. A repeatable
	// action re-arms the window in chain mode ("Esc z z z" undoes three
	// times — without this, the extra z's would type into the buffer);
	// chain mode admits only repeatable bindings so quick typing after a
	// leader can't misfire an unrelated action.
	// A visible which-key overlay holds the window open past doubleEscMs:
	// once the editor has shown the table, "I'm reading" must not time
	// out mid-read.
	if (!a.lastEscape.IsZero() && time.Since(a.lastEscape) < doubleEscMs) || a.whichKey.open {
		if ev.Key() == tcell.KeyRune {
			if b := leaderBindingFor(ev.Rune()); b != nil && (!a.leaderChained || b.repeat) {
				a.fireLeader(b)
				return
			}
		}
	}
	// Alt+<leader rune> is an Esc-leader too, for the same tmux reason
	// as Alt+Esc above: "Esc, s" typed inside the escape-time window
	// reaches us as one "\x1bs" write, which tcell reports as Alt+s
	// with no separate Esc event to arm lastEscape. Unbound Alt runes
	// still fall through so Option-as-Meta typists lose nothing they
	// had before (a bound rune firing an action beats it inserting a
	// literal character mid-code).
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModAlt != 0 {
		if b := leaderBindingFor(ev.Rune()); b != nil {
			a.fireLeader(b)
			return
		}
	}
	// Any other key cancels a pending Esc so a stale half-tap doesn't
	// surprise the user later. The which-key overlay goes with it —
	// typing through the cheat sheet is the expert saying "not needed".
	a.lastEscape = time.Time{}
	a.leaderChained = false
	a.closeWhichKey()

	// Cmd+C / Cmd+V. Terminals speaking the kitty keyboard protocol
	// (kitty, Ghostty, WezTerm, iTerm2 with CSI-u) deliver the Cmd/Super
	// key as ModMeta; classic terminals swallow Cmd entirely, so these
	// are a convenience layer — the ≡ menu and tree context menu remain
	// the guaranteed paths, per the house rule. This is not a Ctrl
	// shortcut: Cmd never collides with tmux prefixes or terminal flow
	// control, which is what the no-Ctrl rule actually protects.
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModMeta != 0 {
		// The ⌘ accelerator table (metakeys.go) gets first refusal, for
		// the same reason the leader table above does: it is a global
		// vocabulary, so ⌘S must save whether the keyboard currently
		// belongs to the editor, the chat composer or the terminal
		// panel — all of which return early below. It can never shadow
		// the three chords handled here: c/v/z are on its reserved list
		// and a test pins that they stay off it.
		if a.metaAccelFire(ev) {
			return
		}
		// An armed compare panel claims Cmd+V — the internal clipboard is
		// the only one ced can READ (OSC 52 is write-only), so this is
		// how a copy made inside the editor becomes the old side.
		if a.comparePasteTarget() && ev.Rune() == 'v' {
			a.comparePasteClip()
			return
		}
		// While the chat composer owns the keyboard, Cmd+V pastes the
		// text clipboard into it — same convenience-layer contract as
		// the terminal branch below.
		if a.chat.open && a.chat.focused {
			switch ev.Rune() {
			case 'v':
				a.chatPasteClip()
			case 'c':
				// Cmd+C lifts the transcript selection — the panel
				// captures the mouse, so this is the only copy gesture
				// the terminal can't do for us.
				a.chatCopySelection()
			}
			return
		}
		// While the terminal owns the keyboard, Cmd+V pastes the text
		// clipboard into the command line instead of the editor buffer
		// (Cmd+C is inert — the panel has no selection to copy).
		if a.term.open && a.term.focused {
			if ev.Rune() == 'v' {
				a.termPasteClip()
			}
			return
		}
		switch ev.Rune() {
		case 'c':
			a.cmdCopy()
			return
		case 'v':
			a.cmdPaste()
			return
		// Cmd+Z / Cmd+Shift+Z — the native undo gesture, for hosts
		// that forward Cmd chords (the cats mac app, kitty-protocol
		// terminals). The shifted rune may arrive as 'Z' or as 'z'
		// with ModShift depending on the emitter, so accept both.
		case 'z':
			if ev.Modifiers()&tcell.ModShift != 0 {
				a.menuRedo()
			} else {
				a.menuUndo()
			}
			return
		case 'Z':
			a.menuRedo()
			return
		}
		// A Command chord is never TEXT. Anything still unclaimed here —
		// a chord this build doesn't bind (⌘T, ⌘N), or one the
		// accelerator gate deliberately refused because the host can't
		// be trusted to distinguish Command from Meta — is swallowed
		// rather than allowed to fall through to the editing switch
		// below, where KeyRune inserts the rune. Without this, pressing
		// ⌘S in a terminal ced doesn't recognize types an "s" into the
		// user's code, which is the one outcome nobody wants from a
		// shortcut that "didn't work". ModAlt is deliberately NOT
		// treated this way: Option-as-Meta typists insert real
		// characters with it (see the Alt-leader note above).
		return
	}

	// While the menu is open: Down/Up move the highlight, Enter
	// activates, and TYPING switches the menu into fuzzy-search mode
	// over its whole inventory (palette.go) — the row you can't find in
	// the folds is one keystroke away, and the keystroke that asked is
	// the seed. Editing keys stay blocked as before.
	if a.menuOpen {
		switch ev.Key() {
		case tcell.KeyDown:
			a.menuMoveSelection(1)
		case tcell.KeyUp:
			a.menuMoveSelection(-1)
		case tcell.KeyEnter:
			a.menuActivate()
		case tcell.KeyRune:
			if ev.Modifiers()&(tcell.ModAlt|tcell.ModMeta|tcell.ModCtrl) == 0 {
				a.menuSearchFrom(ev.Rune())
			}
		}
		return
	}

	// Alt+Left / Alt+Right walk the file-navigation history — the arrow
	// twins of Esc-o / Esc-O. Handled up here rather than in the editing
	// switch below so history stays reachable from image tabs, from the
	// focused terminal, and with no tab open at all. tmux delivers
	// Esc-prefixed arrows folded into ModAlt, same as Alt+Up/Down.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch ev.Key() {
		case tcell.KeyLeft:
			a.navBack()
			return
		case tcell.KeyRight:
			a.navForward()
			return
		}
	}

	// The focused chat composer owns everything below this point —
	// same placement rationale as the terminal branch under it: the
	// global command gestures (Esc leaders, double-Esc menu) keep
	// working from inside the panel, only plain editing keys are
	// claimed. Chat is checked first; the two focus flags are kept
	// mutually exclusive by the click handlers, this order is just the
	// deterministic tiebreak.
	if a.chat.open && a.chat.focused {
		a.handleChatKey(ev)
		return
	}

	// The focused terminal panel owns everything below this point. The
	// branch sits AFTER the Esc / leader / menu blocks on purpose: the
	// global command gestures keep working from inside the terminal
	// (Esc-` back out, Esc-Esc for the menu), only plain editing keys
	// are claimed.
	if a.term.open && a.term.focused {
		a.handleTermKey(ev)
		return
	}

	// The focused file tree claims the rest — arrows, Enter, typeahead,
	// the n/d/r verbs (treenav.go). Same placement contract as the two
	// panels above: global gestures already had their chance, and a
	// keystroke aimed at the tree must never leak into the buffer.
	if a.treeFocus && a.sidebarShown {
		a.handleTreeNavKey(ev)
		return
	}

	// A running pre-commit survey owns the keyboard on the same terms:
	// the git panel is furniture until the user starts a walk, and a
	// walk is a pager they are driving with n / p / space. The placement
	// is the point — every global gesture above still works from inside
	// the survey, and a single Esc ends it (see the Esc block), so the
	// mode can never trap the keyboard. See gitpanelwalk.go.
	if a.gitPanel.open && a.gitPanel.walk {
		a.handleGitPanelWalkKey(ev)
		return
	}

	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	// Image-preview tabs are read-only — no cursor, no editing, no
	// caret movement. Drop every key here so the user can mash arrow
	// keys without anything mysterious happening behind the splash.
	if tab.IsImage() {
		return
	}
	extend := ev.Modifiers()&tcell.ModShift != 0

	switch ev.Key() {
	case tcell.KeyUp:
		// Alt+Up drags the current line (or selected block) up a row.
		// tmux may deliver the gesture as ESC-prefixed arrows, which
		// tcell folds into ModAlt the same way as the leader keys.
		if ev.Modifiers()&tcell.ModAlt != 0 {
			tab.MoveLines(-1)
			return
		}
		tab.MoveCursor(-1, 0, extend)
	case tcell.KeyDown:
		if ev.Modifiers()&tcell.ModAlt != 0 {
			tab.MoveLines(1)
			return
		}
		tab.MoveCursor(1, 0, extend)
	case tcell.KeyCtrlD:
		// The one deliberate exception to the no-Ctrl rule: Ctrl-D
		// (duplicate line) doesn't collide with terminal flow control
		// (Ctrl-S/Q), the default tmux prefix (Ctrl-B), or zellij's
		// binds, and shell EOF semantics don't apply in raw mode.
		tab.DuplicateLines()
	case tcell.KeyLeft:
		// ⌘← is Home's twin — the macOS spelling of "start of line",
		// and the one every other editor on the platform answers to.
		// Cmd is safe where Ctrl is not: it collides with no tmux
		// prefix and no terminal flow control, which is what the
		// no-Ctrl rule actually protects. Shift rides along for free
		// (extend is already ModShift), so ⌘⇧← selects to the start.
		// See metaLineMotion for why the arming gate is consulted and
		// why this isn't a row in the ⌘ accelerator table.
		if a.metaLineMotion(ev) {
			tab.MoveLineHome(extend)
			return
		}
		tab.MoveCursor(0, -1, extend)
	case tcell.KeyRight:
		if a.metaLineMotion(ev) {
			tab.MoveLineEnd(extend)
			return
		}
		tab.MoveCursor(0, 1, extend)
	case tcell.KeyHome:
		tab.MoveLineHome(extend)
	case tcell.KeyEnd:
		tab.MoveLineEnd(extend)
	case tcell.KeyPgUp:
		_, h := a.editorSize()
		tab.MoveCursor(-h, 0, extend)
	case tcell.KeyPgDn:
		_, h := a.editorSize()
		tab.MoveCursor(h, 0, extend)
	case tcell.KeyEnter:
		tab.InsertString("\n")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		tab.Backspace()
	case tcell.KeyDelete:
		tab.Delete()
	case tcell.KeyTab:
		// A visible ghost suggestion claims Tab first; with none
		// showing, Tab is plain indentation as ever. The accept only
		// triggers while the ghost is actually painted, so muscle-
		// memory indents can't be hijacked by a stale item.
		if a.copilotAcceptGhost() {
			return
		}
		tab.InsertString(tab.IndentUnit)
	case tcell.KeyRune:
		tab.InsertRune(ev.Rune())
	}
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// handleMouse routes a mouse event to whichever panel the cursor is over,
// tracking drag state so a click-drag inside the editor extends the
// selection. When the action menu is open it absorbs all mouse events:
// clicks inside trigger an action, clicks outside dismiss the menu.
func (a *App) handleMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	btn := ev.Buttons()

	// Remember when we last saw Shift held down on ANY mouse event.
	// Zellij + macOS Terminal split shift+wheel into two events: a
	// ButtonNone+Shift "modifier state" event, then a WheelDown/Up
	// with no modifier. We bridge them via modifierStickyWindow below.
	if ev.Modifiers()&tcell.ModShift != 0 {
		a.lastShiftAt = time.Now()
	}

	// The active modal absorbs all mouse input — same single-slot
	// dispatch the keyboard router uses, so behavior stays predictable.
	if a.modal != nil {
		a.modal.handleMouse(a, x, y, btn)
		return
	}

	if a.menuOpen {
		// Wheel scrolls the menu itself when it's taller than the window;
		// the hover then re-derives from the rows' new positions so the
		// highlight tracks what's actually under the pointer.
		if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
			delta := wheelLines
			if btn&tcell.WheelUp != 0 {
				delta = -wheelLines
			}
			a.scrollMenu(delta)
			a.updateMenuHover(x, y)
			return
		}
		a.updateMenuHover(x, y)
		a.handleMenuMouse(x, y, btn)
		return
	}

	// Dwell bookkeeping runs before any routing below, because it is
	// about the POINTER rather than about what the pointer is over: a
	// motion re-arms the clock, and anything with a button down cancels
	// it. It only claims an event when a press landed inside the drawn
	// tooltip, which must not fall through to the code it covers.
	// See hoverdwell.go.
	if a.notePointer(x, y, btn) {
		return
	}

	// The commit receipt takes a press the same way: it closes on any
	// button, and a press that landed INSIDE the panel is swallowed
	// rather than moving the caret in code the user could not see. Pure
	// motion and the wheel pass through, so reading a receipt is not
	// interrupted by a nudge of the mouse.
	if btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0 && a.commitReceiptOpen() {
		hit := a.commitReceiptContains(x, y)
		a.closeCommitReceipt()
		if hit {
			return
		}
	}

	// The which-key overlay's rows are clickable. A left press on a row
	// fires it; inside the band, it's swallowed; anywhere else the
	// overlay dismisses and the press falls through to normal routing
	// (whichKeyClick reports which happened).
	if a.whichKey.open && btn&tcell.Button1 != 0 && a.dragMode == "" {
		if a.whichKeyClick(x, y) {
			return
		}
	}

	// The completion popup's rows are clickable too, on the same
	// contract: a row accepts, the box swallows, outside dismisses and
	// falls through to whatever the click was actually aimed at. It sits
	// after which-key because the two never coexist (an edit closes the
	// overlay) and before everything else because the popup is drawn on
	// top of it. See completion.go.
	if a.completion.open && a.dragMode == "" {
		if a.completionMouse(x, y, btn) {
			return
		}
	}

	// Right-click handling. Over a file-tree row it opens a small context
	// menu with file-management actions for that node; over the editor
	// body it opens the editor's own context menu (contextmenu.go);
	// everywhere else it falls through to the main action menu so users
	// have a redundant mouse-only path to it. Note: macOS Terminal + tmux
	// often swallow the right button, which is why every action here also
	// lives in the main ≡ menu.
	//
	// ButtonSecondary (Button2), NOT Button3. tcell v1 numbered the
	// buttons the X11 way (3 = right); tcell v2 deliberately reversed
	// them — Button2 is the right/secondary button and Button3 is the
	// MIDDLE one (see tcell's mouse.go). This branch checked Button3 for a
	// long time, so right-click never reached it in any terminal and a
	// middle-click opened the menu instead; the "Terminal eats Button3"
	// lore was masking it. Traced live under cats, whose PTY carried the
	// SGR right press (`\x1b[<2;x;yM`) exactly as it should.
	if btn&tcell.ButtonSecondary != 0 {
		if a.tryTreeContextClick(x, y) {
			return
		}
		// The problems panel's rows carry their own verbs (quick fix,
		// go to, copy), so it claims the gesture before the editor menu
		// — which would otherwise answer for a click that never landed
		// in the editor at all. The git log's commit rows claim it on
		// exactly the same grounds.
		if a.tryProblemsContextClick(x, y) {
			return
		}
		if a.tryGitLogContextClick(x, y) {
			return
		}
		if a.tryEditorContextClick(x, y) {
			return
		}
		a.openMenu()
		return
	}

	// Wheel events take priority — they fire even with no button held.
	// Shift+wheel rotates the vertical wheel into horizontal scrolling
	// (the VS Code convention). Most terminals never emit native
	// WheelLeft/WheelRight, so this is the path that actually fires in
	// practice; the dedicated horizontal-wheel branch below is a bonus
	// for terminals that do.
	//
	// We accept "shift was just seen" within modifierStickyWindow as
	// equivalent to shift-on-this-event, because Zellij and friends
	// strip the modifier from the actual wheel event.
	shift := ev.Modifiers()&tcell.ModShift != 0 ||
		(!a.lastShiftAt.IsZero() && time.Since(a.lastShiftAt) < modifierStickyWindow)
	if btn&tcell.WheelUp != 0 {
		if shift {
			a.scrollAtH(x, y, -wheelCols)
		} else {
			a.scrollAt(x, y, -wheelLines)
		}
		return
	}
	if btn&tcell.WheelDown != 0 {
		if shift {
			a.scrollAtH(x, y, wheelCols)
		} else {
			a.scrollAt(x, y, wheelLines)
		}
		return
	}
	if btn&tcell.WheelLeft != 0 {
		a.scrollAtH(x, y, -wheelCols)
		return
	}
	if btn&tcell.WheelRight != 0 {
		a.scrollAtH(x, y, wheelCols)
		return
	}

	leftDown := btn&tcell.Button1 != 0

	// Drag continuation: while we're mid-drag in the editor, every event
	// with the button held extends the selection — even if the cursor has
	// wandered out of the editor pane.
	if leftDown && a.dragMode == "editor" {
		a.editorDrag(x, y)
		return
	}

	// Sidebar resize drag: keep the splitter glued to the mouse x so the
	// panel reshapes live as the user drags. The width the mouse implies
	// depends on which edge the block hugs.
	if leftDown && a.dragMode == "sidebar" {
		want := x + 1
		if a.treeOnRight() {
			want = a.width - x
		}
		// A drag is the user stating a width, so it takes ownership of the
		// number back from auto-fit (which would otherwise overwrite it on
		// the next expand). Gated on the splitter actually MOVING: a press
		// with a pixel of jitter isn't a statement about anything, and this
		// writes a preference to disk.
		if want != a.sidebarWidth {
			a.lockTreeAutoFit()
		}
		a.resizeSidebar(want)
		return
	}

	// Left-docked terminal resize drag: same gesture as the sidebar
	// splitter, opposite edge.
	if leftDown && a.dragMode == "termsplit" {
		a.resizeTermPanelWidth(x + 1)
		return
	}

	// Chat strip resize drag — same gesture, same edge as the
	// left-docked terminal (the two are never open together).
	if leftDown && a.dragMode == "chatsplit" {
		a.resizeChatPanelWidth(x + 1)
		return
	}

	// Editor scrollbar drag: the thumb follows the mouse row, carrying
	// the viewport with it. Deliberately handled wherever the pointer has
	// wandered to — dragging off either end parks at that end, which is
	// what every scrollbar does.
	if leftDown && a.dragMode == "scrollbar" {
		a.dragScrollbarTo(y)
		return
	}

	// Chat transcript drag-select: the panel captures the mouse, so the
	// terminal's own selection never reaches it — this is the editor's
	// replacement, same shape as the editor pane's drag.
	if leftDown && a.dragMode == "chatsel" {
		a.chatPanelDrag(x, y)
		return
	}

	// Git panel resize drag: the header rule follows the mouse row.
	if leftDown && a.dragMode == "gitpanel" {
		a.dragGitPanelTo(y)
		return
	}

	// Git panel list/diff divider drag: the internal seam follows the
	// mouse x, reshaping the file-list column against the diff pane.
	if leftDown && a.dragMode == "gitlistdiv" {
		a.dragGitListDivTo(x)
		return
	}

	// Git log resize / divider drags — same two gestures, other panel.
	if leftDown && a.dragMode == "gitlog" {
		a.dragGitLogTo(y)
		return
	}
	if leftDown && a.dragMode == "gitlogdiv" {
		a.dragGitLogDivTo(x)
		return
	}

	// Compare panel resize drag — same gesture, other bottom panel.
	if leftDown && a.dragMode == "comparepanel" {
		a.dragComparePanelTo(y)
		return
	}

	// Problems panel resize drag — same gesture, same strip.
	if leftDown && a.dragMode == "problems" {
		a.dragProblemsPanelTo(y)
		return
	}

	// Terminal panel resize drag — same gesture, other bottom panel.
	if leftDown && a.dragMode == "termpanel" {
		a.dragTermPanelTo(y)
		return
	}

	// Initial press dispatch.
	if leftDown && a.dragMode == "" {
		// Any press outside the terminal panel hands the keyboard back
		// to the editor — the mouse-first focus model: you click where
		// you want to type. The chat composer follows the same rule.
		if a.term.open && a.term.focused && !a.termPanelContains(x, y) {
			a.term.focused = false
		}
		if a.chat.open && a.chat.focused && !a.chatPanelContains(x, y) {
			a.chat.focused = false
		}
		if a.findAllPin != nil && !a.findAllPinContains(x, y) {
			a.findAllPin.focus = findAllFocusList
		}
		// The git log's search field follows the same rule. It matters
		// more here than for the panels above: the field sits over a
		// docked panel, so leaving it focused after a click into the
		// editor would silently type the user's code into a search box.
		if a.gitLog.filter.focused && !a.gitLogContains(x, y) {
			a.gitLog.filter.focused = false
		}
		// The tree follows the same click-where-you-want-to-type rule:
		// a press outside the sidebar hands the keyboard back; a press
		// on a tree row (sidebarClick) takes it, and moves the cursor.
		if a.treeFocus && !a.inSidebarBlock(x) {
			a.treeFocus = false
		}
		// So does a running survey — and it matters most here, because
		// the whole reason to click out of the panel is to go fix the
		// thing you just spotted, and a walk still holding n / p would
		// type its verbs into that fix. The reviewed marks stay, so the
		// header button says "Resume 3/7 ▶" when you come back.
		if a.gitPanel.walk && !a.gitPanelContains(x, y) {
			a.stopGitPanelWalk()
		}
		switch {
		case a.splitterX() >= 0 && x == a.splitterX():
			a.dragMode = "sidebar"
		case a.termSplitterX() >= 0 && x == a.termSplitterX():
			a.dragMode = "termsplit"
		case a.chatSplitterX() >= 0 && x == a.chatSplitterX():
			a.dragMode = "chatsplit"
		case a.inSidebarBlock(x):
			a.sidebarClick(x, y)
		// The chat strip spans y==0 like a left-docked terminal, so its
		// hit-test also runs before the tab-bar row case.
		case a.chatPanelContains(x, y):
			a.dragMode = a.chatPanelPress(x, y)
		// A left-docked terminal strip spans y==0, so its hit-test must
		// run before the tab-bar row case; a bottom-docked strip never
		// includes y==0, so the early check is harmless in that layout.
		case a.term.open && a.termPanelContains(x, y):
			if a.termPanelPress(x, y) {
				a.dragMode = "termpanel"
			}
		case y == 0:
			a.tabBarClick(x, y)
		// The git panel sits inside the editor's former y-range, so its
		// hit-test must run before the catch-all editor case. A press on
		// the header rule or the list/diff divider starts a resize drag
		// instead of a click (gitPanelPress names which mode).
		case a.gitPanel.open && a.gitPanelContains(x, y):
			a.dragMode = a.gitPanelPress(x, y)
		case a.gitLog.open && a.gitLogContains(x, y):
			a.dragMode = a.gitLogPress(x, y)
		case a.compare.open && a.comparePanelContains(x, y):
			a.dragMode = a.comparePanelPress(x, y)
		// The problems panel shares that strip, so its hit-test sits with
		// its neighbours — before the catch-all editor case, for the same
		// reason theirs do.
		case a.problemsContains(x, y):
			a.dragMode = a.problemsPress(x, y)
		// The pinned find-all panel took its rows/columns out of the
		// editor band, so its hit-test runs before the catch-all — the
		// same reason the git panel's does. Clicks elsewhere first drop
		// any focused field it holds (click-where-you-want-to-type).
		case a.findAllPinContains(x, y):
			a.findAllPin.handleMouse(a, x, y, btn)
		// The scrollbar's column sits inside the editor's y-band, so it
		// asks before the catch-all for the same reason every panel does
		// — an unasked press would move the caret to whatever line the
		// user happened to grab the thumb on. A press on the track pages
		// instead of dragging, hence the empty mode. See scrollbar.go.
		case a.scrollbarContains(x, y):
			a.dragMode = a.scrollbarPress(x, y)
		// The find bar sits inside the editor's former y-range too, so
		// its hit-test runs before the catch-all — otherwise a click on
		// the Aa toggle would land in the file behind it and move the
		// cursor there.
		case a.findBarPress(x, y):
		// The status bar owns the bottom row; its segments carry their
		// own click targets (statusbar.go). Placed after every panel
		// case — none of them reaches the bottom row — and before the
		// editor catch-all, which already excluded it.
		case y == a.height-1:
			a.statusBarClick(x, y)
		case y > 0 && y < a.height-1:
			// Alt+click drops (or lifts) an extra caret instead of
			// moving the one you have — the multi-line editing gesture.
			// It deliberately starts no drag: the press placed a caret,
			// and a stray mouse wiggle afterwards must not turn that
			// into a selection that wipes the whole caret set.
			if ev.Modifiers()&tcell.ModAlt != 0 && a.editorAltPress(x, y) {
				return
			}
			// A press on a blame annotation reveals its commit instead of
			// moving the caret, and starts no drag — for the same reason
			// Alt+click doesn't: the gesture was aimed at the margin, and
			// a stray wiggle afterwards must not turn it into a selection
			// of the code it was pointing past (gitblame.go).
			if a.blameColumnPress(x, y) {
				return
			}
			a.editorPress(x, y)
			a.dragMode = "editor"
		}
		return
	}

	// Button released — exit any drag mode we were in.
	a.dragMode = ""
	a.stopAutoScroll()
}

// handleMenuMouse processes mouse events while the action menu is open.
// Left-click outside the modal closes it; left-click on a row runs that
// row's action (if it is currently enabled).
func (a *App) handleMenuMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.Button1 == 0 {
		return
	}
	mx, my, mw, mh := a.menuModalRect()
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeMenu()
		return
	}
	idx := a.menuItemIndexAt(x, y)
	if idx < 0 {
		return
	}
	items, _, _ := a.menuLayout()
	if items[idx].enabled(a) {
		items[idx].action(a)
	}
}

// scrollAt scrolls whichever panel the (x, y) cursor is over.
func (a *App) scrollAt(x, y, delta int) {
	if a.inSidebarBlock(x) {
		a.tree.Scroll(delta)
		return
	}
	if a.gitPanel.open && a.gitPanelContains(x, y) {
		a.gitPanelScroll(x, y, delta)
		return
	}
	if a.gitLog.open && a.gitLogContains(x, y) {
		a.gitLogScroll(x, y, delta)
		return
	}
	if a.compare.open && a.comparePanelContains(x, y) {
		a.comparePanelScroll(delta)
		return
	}
	if a.problemsContains(x, y) {
		a.problemsScroll(delta)
		return
	}
	if a.chatPanelContains(x, y) {
		a.chatPanelScroll(delta)
		return
	}
	if a.term.open && a.termPanelContains(x, y) {
		a.termPanelScroll(delta)
		return
	}
	if a.findAllPinContains(x, y) {
		a.findAllPin.scrollList(a, delta)
		return
	}
	if y > 0 && y < a.height-1 {
		if t := a.activeTabPtr(); t != nil {
			t.Scroll(delta)
		}
	}
}

// scrollAtH scrolls the panel under (x, y) horizontally by delta cells.
// The file tree has no useful horizontal axis (each row is a single label)
// and neither does the terminal strip, so we only honor horizontal wheel
// events when they fall inside the editor pane.
func (a *App) scrollAtH(x, y, delta int) {
	if a.inSidebarBlock(x) {
		return
	}
	if a.term.open && a.termPanelContains(x, y) {
		return
	}
	if y > 0 && y < a.height-1 {
		if t := a.activeTabPtr(); t != nil {
			t.ScrollH(delta)
		}
	}
}

// tryTreeContextClick opens the right-click context menu when (x, y) lands
// on a tree row. Returns true if it consumed the event so the caller knows
// not to fall back to the main action menu. Right-clicking a node also
// counts as "I'm working here" — the active folder updates so the main
// menu's New File defaults to a sensible target even after the context
// menu closes.
func (a *App) tryTreeContextClick(x, y int) bool {
	sx, sy, sw, sh := a.sidebarRect()
	if sw <= 0 || x < sx || x >= sx+sw || y < sy || y >= sy+sh {
		return false
	}
	n, ok := a.tree.HitTest(x-sx, y-sy)
	if !ok {
		return false
	}
	if n.IsDir {
		a.setActiveFolder(n.Path)
	} else {
		a.setActiveFolder(filepath.Dir(n.Path))
	}
	a.openTreeContext(n, x, y)
	return true
}

// sidebarClick toggles a directory or opens a file when the user clicks a
// row in the file tree. Either action also updates the editor's "active
// folder" so the next New File from the main menu defaults to wherever
// the user is currently focused. Clicking the project-root row only
// resets the active folder — it never toggles the root's expansion
// since the root is always shown and there's no useful "collapsed
// root" state.
func (a *App) sidebarClick(x, y int) {
	sx, sy, _, _ := a.sidebarRect()
	n, ok := a.tree.HitTest(x-sx, y-sy)
	if !ok {
		return
	}
	// A click is also a focus gesture (the panels' shared rule) and
	// moves the keyboard cursor, so a mouse-then-arrows mix just works.
	a.treeFocus = true
	if n != a.tree.Root {
		a.tree.Selected = n
	}
	if n == a.tree.Root {
		a.setActiveFolder(a.rootDir)
		return
	}
	if n.IsDir {
		a.setActiveFolder(n.Path)
		a.tree.Toggle(n)
		return
	}
	a.setActiveFolder(filepath.Dir(n.Path))
	a.openFile(n.Path)
}

// setActiveFolder records path as the editor's current working folder and
// mirrors it onto the file tree so the matching row renders with the
// "active" highlight. All writes to a.activeFolder go through here.
func (a *App) setActiveFolder(path string) {
	a.activeFolder = path
	if a.tree != nil {
		a.tree.ActiveFolder = path
	}
}

// editorPress handles the initial mouse press inside the editor — placing
// the caret, optionally selecting a word on double-click. Image tabs
// have no caret, so the press is dropped.
func (a *App) editorPress(x, y int) {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	ex, ey, ew, eh := a.editorRect()
	pos, ok := tab.HitTest(x-ex, y-ey, ew, eh)
	if !ok {
		return
	}

	now := time.Now()
	if a.lastClick.x == x && a.lastClick.y == y && now.Sub(a.lastClick.when) < doubleClickMs {
		a.selectWordAt(tab, pos)
		a.lastClick = clickRecord{} // prevent triple-click from selecting nothing.
		return
	}
	a.lastClick = clickRecord{x: x, y: y, when: now}
	tab.MoveCursorTo(pos, false)
}

// editorDrag extends the selection during a click-drag inside the editor.
// (x, y) is clamped to the editor rect so dragging into another pane still
// extends the selection sensibly. When the mouse passes above or below the
// editor we engage auto-scroll so the user can select content that's not
// yet on screen — same feel as VS Code or any GUI text editor. Image tabs
// drop the drag entirely.
func (a *App) editorDrag(x, y int) {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	ex, ey, ew, eh := a.editorRect()

	// Remember where the mouse is so the auto-scroll tick can extend the
	// selection at this column even while the mouse stops moving.
	a.lastDragX = x
	a.lastDragY = y

	// Edge detection: outside the editor's vertical bounds turns on
	// auto-scroll; back inside turns it off.
	switch {
	case y < ey:
		a.startAutoScroll(-1)
	case y >= ey+eh:
		a.startAutoScroll(1)
	default:
		a.stopAutoScroll()
	}

	// Clamp the mouse into the editor and extend the selection there.
	localX := x - ex
	localY := y - ey
	if localX < 0 {
		localX = 0
	}
	if localY < 0 {
		localY = 0
	}
	if localX >= ew {
		localX = ew - 1
	}
	if localY >= eh {
		localY = eh - 1
	}
	pos, ok := tab.HitTest(localX, localY, ew, eh)
	if !ok {
		return
	}
	tab.MoveCursorTo(pos, true)
}

// startAutoScroll begins a timer goroutine that posts autoScrollEvents at
// autoScrollTick intervals so the editor keeps scrolling while the user
// holds the mouse past an edge. dir is -1 (up) or +1 (down). Calling with
// the same direction is a no-op so we don't restart the timer on every
// drag motion event.
func (a *App) startAutoScroll(dir int) {
	if a.autoScrollDir == dir {
		return
	}
	a.stopAutoScroll()
	a.autoScrollDir = dir
	a.autoScrollStop = make(chan struct{})
	stop := a.autoScrollStop
	scr := a.screen
	go func() {
		ticker := time.NewTicker(autoScrollTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case t := <-ticker.C:
				_ = scr.PostEvent(&autoScrollEvent{when: t})
			}
		}
	}()
}

// stopAutoScroll signals the auto-scroll goroutine to exit (idempotent).
func (a *App) stopAutoScroll() {
	if a.autoScrollStop != nil {
		close(a.autoScrollStop)
		a.autoScrollStop = nil
	}
	a.autoScrollDir = 0
}

// handleAutoScroll runs once per autoScrollEvent: nudge the viewport in the
// armed direction and extend the selection to the edge row at the user's
// last known mouse column. Bails out (and stops the timer) if anything
// suggests the user is no longer drag-selecting (button released, menu
// opened, no active tab).
func (a *App) handleAutoScroll() {
	if a.autoScrollDir == 0 || a.dragMode != "editor" || a.anyModalOpen() {
		a.stopAutoScroll()
		return
	}
	tab := a.activeTabPtr()
	if tab == nil {
		a.stopAutoScroll()
		return
	}
	tab.Scroll(a.autoScrollDir)

	ex, _, ew, eh := a.editorRect()
	localX := a.lastDragX - ex
	if localX < 0 {
		localX = 0
	}
	if localX >= ew {
		localX = ew - 1
	}
	localY := eh - 1
	if a.autoScrollDir < 0 {
		localY = 0
	}
	pos, ok := tab.HitTest(localX, localY, ew, eh)
	if !ok {
		return
	}
	tab.MoveCursorTo(pos, true)
}

// selectWordAt selects the word under the buffer position p (or does
// nothing if p sits in whitespace / punctuation).
func (a *App) selectWordAt(tab *editor.Tab, p editor.Position) {
	line := tab.Buffer.LineRunes(p.Line)
	if len(line) == 0 {
		return
	}
	start := p.Col
	if start > len(line) {
		start = len(line)
	}
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := p.Col
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	if start == end {
		return
	}
	tab.Anchor = editor.Position{Line: p.Line, Col: start}
	tab.Cursor = editor.Position{Line: p.Line, Col: end}
}

// isWordChar reports whether r is part of a "word" for double-click select.
// Intentionally simple ASCII-ish definition; covers the common cases.
func isWordChar(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// -----------------------------------------------------------------------------
// Tab + clipboard actions
// -----------------------------------------------------------------------------

// activeTabPtr returns the currently active *editor.Tab, or nil when there
// are no tabs open.
func (a *App) activeTabPtr() *editor.Tab {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.activeTab]
}

// flash sets a transient status message that displays for statusFlashFor
// before the status bar reverts to the active file's info.
func (a *App) flash(msg string) {
	a.statusMsg = msg
	a.statusUntil = time.Now().Add(statusFlashFor)
}

// welcomeIfQuiet greets a new workspace, but only when startup has
// nothing more important to say.
//
// The guard is the point. userconfig deliberately REPORTS a typo'd key
// rather than ignoring it — a silently dropped value is one the user
// believes is in effect — and loadUserConfig flashes that report. An
// unconditional greeting here overwrote it, which made the whole
// contract a no-op: the reader never saw the message, and the setting
// they misspelled stayed quietly inactive.
func (a *App) welcomeIfQuiet() {
	if a.statusMsg != "" {
		return
	}
	a.flash("Welcome — click a file to open · click  ≡  for the menu")
}

// OpenFile opens the file at path in a new tab — or switches to it if
// it is already open. Exported so main.go can seed the editor with the
// file the user named on the command line ("ced foo.go"). Thin
// wrapper around openFile so internal callers keep using the lowercase
// name and the public surface stays small.
func (a *App) OpenFile(path string) { a.openFile(path) }

// openFile opens the file at path in a new tab — or switches to it if it is
// already open in another tab. Errors are surfaced as a flash message.
// Whatever the path resolves to, its parent becomes the active folder so
// the next New File from the main menu lands next to it.
func (a *App) openFile(path string) {
	// Tabs are keyed by absolute path everywhere downstream — the
	// LSP diagnostics map, the diff cache, the open-tab dedupe — so
	// normalize a relative path (a CLI arg like "ced foo.go") here,
	// at the single entry point, rather than teaching every consumer
	// to cope. New() already anchored rootDir, so tree-driven opens
	// arrive absolute and this is a no-op for them.
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	// Snapshot the departure point for the navigation history up front,
	// but record it only on the success paths below — a failed open
	// moves nothing, and a recorded entry would then make Go back a
	// no-op press. Same-path opens (re-clicking the active file in the
	// tree) aren't navigation, so they don't record.
	from, hasFrom := a.currentNavLoc()
	hasFrom = hasFrom && from.path != path
	a.setActiveFolder(filepath.Dir(path))
	for i, t := range a.tabs {
		if t.Path == path {
			if hasFrom {
				a.recordNav(from)
			}
			a.activeTab = i
			a.touchRecentFile(path)
			return
		}
	}
	t, err := editor.NewTab(path)
	if err != nil {
		a.flash(fmt.Sprintf("Error: %v", err))
		return
	}
	if hasFrom {
		a.recordNav(from)
	}
	a.wireTab(t)
	a.tabs = append(a.tabs, t)
	a.activeTab = len(a.tabs) - 1
	// After the tab exists, so the ring only ever names files the editor
	// actually got open — a NewTab that failed above returned already.
	a.touchRecentFile(path)
	a.announceTab(t)
	a.flash(fmt.Sprintf("Opened %s", filepath.Base(path)))
}

// wireTab attaches the app-owned decoration sources and per-tab
// preferences to a freshly-created Tab and kicks off its first diff.
// Everything a tab needs BEFORE it joins a.tabs.
//
// It exists because openFile is no longer the only way a tab is born —
// session restore (folder.go) creates them too — and a second copy of
// this wiring would drift: a tab opened one way would quietly lack the
// git gutter, or the word highlight, or a plugin's marks. One writer,
// two callers.
func (a *App) wireTab(t *editor.Tab) {
	// Wire the diff gutter in and kick off the first diff so marks
	// appear as soon as the async result lands, not at the next tick.
	// The diagnostics source registers after git so on a line that is
	// both changed and broken, the diagnostic dot wins the mark cell.
	// Precedence in the single gutter cell runs git < plugin < LSP: a
	// plugin's mark outranks the ambient git change bar because the
	// user installed it deliberately, and loses to gopls because a
	// compile error is the more urgent thing to say. See plugindeco.go.
	// The blame source rides with them: it paints no spans and claims no
	// mark cell, so its position in the precedence order is irrelevant —
	// what it owns is the annotation column, which nothing else asks for.
	t.DecoSources = append(t.DecoSources,
		gitDiffSource{app: a}, gitBlameSource{app: a},
		pluginDecoSource{app: a}, lspDiagSource{app: a})
	// The matching-word highlight is a built-in source gated by a per-tab
	// flag, so the preference has to ride along at open time (wordhl.go).
	t.WordHighlight = a.wordHLEnabled
	a.requestFileDiff(t.Path)
	// Blame the newly opened file too, but only while the layer is on —
	// this is a fork per file, unlike the diff, and nobody has asked to
	// see authorship until they switch it on.
	if a.blameOn {
		a.requestFileBlame(t)
	}
}

// announceTab tells the integrations a document is now open. The other
// half of wireTab, split off because these three all want a tab that is
// already IN a.tabs — a provider's marks arrive asynchronously and must
// land on a tab the render walk can find.
func (a *App) announceTab(t *editor.Tab) {
	a.lspOpenDoc(t)
	a.copilotOpenDoc(t)
	// Fire the plugin "open" event last, once the tab is fully wired:
	// a provider's marks arrive asynchronously and land on a tab that
	// already has its decoration source attached.
	a.pluginsOnEvent(plugins.EventOpen, t)
}

// saveActiveTab writes the active tab's buffer to disk.
func (a *App) saveActiveTab() {
	a.saveTabAt(a.activeTab)
}

// saveTabAt saves the tab at idx. Returns true on success, false on
// any kind of failure (no tab, untitled, IO error). Failures flash a
// status message so the caller doesn't have to. Pulled out from
// saveActiveTab so the dirty-close modal can save a specific tab and
// branch on success — saving and then closing must not eat the user's
// work when the save itself failed.
func (a *App) saveTabAt(idx int) bool {
	if idx < 0 || idx >= len(a.tabs) {
		return false
	}
	tab := a.tabs[idx]
	if tab.Path == "" {
		a.flash("Saving untitled tabs is not supported yet")
		return false
	}
	// Interception point B (reconcile.go): if the file grew newer than
	// the copy this tab loaded, the write is aborted and the clobber
	// prompt is raised instead, with this very save armed to re-run if
	// the user answers "Keep mine". Reporting failure is correct — the
	// caller (dirty-close, save-all-and-quit) must not proceed as though
	// the bytes landed.
	if !a.saveGuard(tab, func(app *App) { app.saveTabAt(idx) }) {
		return false
	}
	if err := tab.Save(); err != nil {
		a.flash(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	a.refreshGitStatus()
	a.requestFileDiff(tab.Path)
	// A save is the one moment blame can change without the buffer
	// moving — `git blame --contents` compares against history, and a
	// commit made in the pane next door lands here as new authorship for
	// lines the settle timer would never re-ask about.
	if a.blameOn {
		a.requestFileBlame(tab)
	}
	a.lspDidSave(tab)
	// Saving a file under ~/.config/ced/themes re-reads the registry and
	// repaints — the save-to-preview loop that stands in for a settings
	// modal (theme.go). A no-op for every other path.
	a.themeAfterSave(tab.Path)
	a.flash(fmt.Sprintf("Saved %s", filepath.Base(tab.Path)))
	// Format-on-save runs after the disk write succeeds, so a broken
	// formatter never blocks the user's save from landing. The
	// formatter (when configured + trusted, or the builtin Go pass)
	// reloads the buffer asynchronously via formatDoneEvent — see
	// format.go. Explicit saves are loud: prompts and flashes allowed.
	a.runFormatOnSave(idx, false)
	// Plugin save hooks run after the write lands, same reasoning as
	// format-on-save: a broken hook must never stop the user's save
	// from reaching disk.
	a.pluginsOnEvent(plugins.EventSave, tab)
	return true
}

// saveAllDirty walks every open tab and saves each dirty one. Returns
// true when every dirty tab saved successfully — used by the quit flow
// to decide whether it's safe to actually exit. The first failure
// short-circuits because there's no point cascading more failed saves
// past one we've already flashed about, and the user needs to react to
// the first error before deciding what to do with the rest.
func (a *App) saveAllDirty() bool {
	for i, tab := range a.tabs {
		if !tab.Dirty {
			continue
		}
		if !a.saveTabAt(i) {
			return false
		}
	}
	return true
}

// dirtyTabCount returns the number of tabs with unsaved changes.
// Used by the quit flow to decide whether to skip the modal entirely.
func (a *App) dirtyTabCount() int {
	n := 0
	for _, tab := range a.tabs {
		if tab.Dirty {
			n++
		}
	}
	return n
}

// requestCloseTab closes the tab at idx. A clean tab closes immediately;
// a dirty tab opens the unsaved-changes modal so the user can pick
// Save / Discard / Cancel. The Save path saves the buffer first and only
// closes the tab on success — a save error would otherwise silently lose
// the user's work.
func (a *App) requestCloseTab(idx int) {
	if idx < 0 || idx >= len(a.tabs) {
		return
	}
	tab := a.tabs[idx]
	if !tab.Dirty {
		a.closeTab(idx)
		return
	}
	name := filepath.Base(tab.Path)
	if name == "" || name == "." {
		name = "untitled"
	}
	a.openDirtyClose(
		"Unsaved changes",
		name+" has unsaved changes.",
		func(app *App) {
			// Save → close. saveTabAt flashes its own error, in which
			// case we keep the tab around so the user can react.
			if app.saveTabAt(idx) {
				app.closeTab(idx)
			}
		},
		func(app *App) { app.closeTab(idx) },
	)
}

// closeTab removes the tab at idx without any dirty-check.
func (a *App) closeTab(idx int) {
	if idx < 0 || idx >= len(a.tabs) {
		return
	}
	// Drop the closed file's cached diff — openFile dedupes by path,
	// so no other tab can still be showing it. Same for the LSP
	// bookkeeping, which also tells the server the document is gone.
	delete(a.fileDiffs, a.tabs[idx].Path)
	a.lspCloseDoc(a.tabs[idx].Path)
	a.copilotCloseDoc(a.tabs[idx].Path)
	// Closing the tab is the gesture that means "I'm done with this
	// file", so it is what releases a `ced --wait` client blocked on it
	// (remote.go). A save deliberately doesn't: $EDITOR callers expect
	// the editor to be finished, not merely to have written once.
	a.releaseRemote(a.tabs[idx].Path)
	// A closed tab takes its undo stack with it, so a multi-file edit that
	// touched this file can no longer be unwound as one gesture. Dropping
	// the journal is the honest answer — a group that silently skipped a
	// participant is exactly the half-applied refactor it exists to prevent
	// (workspaceedit.go).
	a.wsForgetTab(a.tabs[idx])
	// Likewise the disk-conflict record: keyed by tab pointer, it would
	// otherwise outlive the buffer it describes (reconcile.go).
	a.reconcileForgetTab(a.tabs[idx])
	// And a completion popup anchored into the buffer that is about to
	// disappear. completionSync would close it on the next event anyway,
	// but the list would draw once against a tab that no longer exists
	// in between (completion.go).
	if a.completion.open && a.completion.path == a.tabs[idx].Path {
		a.completionClose()
	}
	a.tabs = append(a.tabs[:idx], a.tabs[idx+1:]...)
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if a.activeTab < 0 {
		a.activeTab = 0
	}
}

// copySelection puts the active tab's selection on the system clipboard
// (via OSC 52) and into the editor's internal clipboard.
func (a *App) copySelection() {
	tab := a.activeTabPtr()
	if tab == nil || !tab.HasSelection() {
		return
	}
	txt := tab.SelectionText()
	a.clipBuf = txt
	a.clipKind = clipText
	if err := clipboard.CopyToSystem(txt); err != nil {
		a.flash("Copied (system clipboard unavailable)")
		return
	}
	a.flash("Copied")
}

// cutSelection copies the selection then deletes it.
func (a *App) cutSelection() {
	tab := a.activeTabPtr()
	if tab == nil || !tab.HasSelection() {
		return
	}
	a.copySelection()
	tab.DeleteSelection()
}

// pasteClipboard inserts the editor's internal clipboard at the cursor.
// We can't read the system clipboard from a TUI, so external pastes have
// to come in through the user's terminal paste (Cmd-V / right-click paste).
func (a *App) pasteClipboard() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	if a.clipBuf == "" {
		a.flash("Internal clipboard empty — paste from your terminal (Cmd-V)")
		return
	}
	tab.InsertString(a.clipBuf)
}

// -----------------------------------------------------------------------------
// Action menu
// -----------------------------------------------------------------------------

// openMenu shows the action modal. While it is up, the editor doesn't
// receive typed keys, and clicks outside the modal dismiss it. We pre-
// select the first enabled action row so Down/Up/Enter keyboard
// navigation has somewhere sensible to start — deliberately NOT a
// section header, so a reflex Enter runs an action rather than folding
// the first section.
func (a *App) openMenu() {
	a.closeAllModals()
	a.menuOpen = true
	a.menuScroll = 0
	a.hoveredMenuRow = -1
	items, _, _ := a.menuLayout()
	for i, it := range items {
		if !it.header && it.enabled(a) {
			a.hoveredMenuRow = i
			a.menuEnsureHoveredVisible()
			return
		}
	}
}

// menuMoveSelection advances hoveredMenuRow to the next (dir=+1) or
// previous (dir=-1) enabled menu item, wrapping around at the ends so the
// list feels continuous. Disabled items and dividers are skipped. If no
// item is currently enabled hoveredMenuRow stays -1.
func (a *App) menuMoveSelection(dir int) {
	items, _, _ := a.menuLayout()
	n := len(items)
	if n == 0 {
		return
	}
	start := a.hoveredMenuRow
	if start < 0 {
		// No current selection — start one step before the first row (for
		// Down) or one past the last (for Up) so the loop lands on the
		// first/last enabled item.
		if dir > 0 {
			start = -1
		} else {
			start = n
		}
	}
	for i := 1; i <= n; i++ {
		idx := ((start+dir*i)%n + n) % n
		if items[idx].enabled(a) {
			a.hoveredMenuRow = idx
			// Keyboard navigation must be able to reach rows the clamped
			// modal has scrolled out of view (wrap-around included).
			a.menuEnsureHoveredVisible()
			return
		}
	}
	a.hoveredMenuRow = -1
}

// menuActivate runs the currently-highlighted menu item, if any. It's the
// keyboard-Enter equivalent of clicking a row.
func (a *App) menuActivate() {
	items, _, _ := a.menuLayout()
	if a.hoveredMenuRow < 0 || a.hoveredMenuRow >= len(items) {
		return
	}
	item := items[a.hoveredMenuRow]
	if !item.enabled(a) {
		return
	}
	item.action(a)
}

// closeMenu hides the action modal without running any action.
func (a *App) closeMenu() {
	a.menuOpen = false
	a.hoveredMenuRow = -1
	a.menuScroll = 0
}

// updateMenuHover sets hoveredMenuRow to the index of the enabled menu row
// at (x, y), or to -1 when the mouse is over a disabled row, a divider, the
// title, or anywhere outside the modal.
func (a *App) updateMenuHover(x, y int) {
	a.hoveredMenuRow = -1
	idx := a.menuItemIndexAt(x, y)
	if idx < 0 {
		return
	}
	items, _, _ := a.menuLayout()
	if items[idx].enabled(a) {
		a.hoveredMenuRow = idx
	}
}

// hasTab reports whether there is an active tab to act on.
func (a *App) hasTab() bool { return a.activeTabPtr() != nil }

// hasSavableTab reports whether the active tab is one we can persist —
// it must exist, have a path on disk, and not be a read-only image
// preview. Used by Save and Save & Close.
func (a *App) hasSavableTab() bool {
	t := a.activeTabPtr()
	return t != nil && t.Path != "" && !t.IsImage()
}

// hasFileTab reports whether the active tab is backed by a real file
// (text or image). Used by Rename / Delete which act on the file
// regardless of how the tab is rendered.
func (a *App) hasFileTab() bool {
	t := a.activeTabPtr()
	return t != nil && t.Path != ""
}

// hasSelection reports whether the active tab has a non-empty selection.
func (a *App) hasSelection() bool {
	t := a.activeTabPtr()
	return t != nil && t.HasSelection()
}

// hasCommentableTab reports whether the active tab is editable text with a
// known single-line comment marker.
func (a *App) hasCommentableTab() bool {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		return false
	}
	_, ok := editor.LineCommentPrefix(t.Path)
	return ok
}

// hasClipboard reports whether the editor's internal clipboard has content
// to paste.
func (a *App) hasClipboard() bool { return a.clipBuf != "" }

// hasEditableTab reports whether the active tab is editable text — the
// gate for line-editing actions (duplicate / move line).
func (a *App) hasEditableTab() bool {
	t := a.activeTabPtr()
	return t != nil && !t.IsImage()
}

// menuDuplicateLines duplicates the cursor's line (or the selected line
// block) below itself — the menu twin of Ctrl-D.
func (a *App) menuDuplicateLines() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.DuplicateLines()
	}
}

// menuMoveLinesUp shifts the current line block up one row — the menu
// twin of Alt-Up.
func (a *App) menuMoveLinesUp() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.MoveLines(-1)
	}
}

// menuMoveLinesDown shifts the current line block down one row — the
// menu twin of Alt-Down.
func (a *App) menuMoveLinesDown() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.MoveLines(1)
	}
}

// hasUndo reports whether the active tab has anything to undo. Used to
// enable / disable the Undo row in the action menu.
func (a *App) hasUndo() bool {
	t := a.activeTabPtr()
	return t != nil && t.CanUndo()
}

// hasRedo reports whether the active tab has anything to redo.
func (a *App) hasRedo() bool {
	t := a.activeTabPtr()
	return t != nil && t.CanRedo()
}

// hasRevert reports whether the active tab differs from its on-open
// (or last-reload) baseline — i.e. there is something to revert.
func (a *App) hasRevert() bool {
	t := a.activeTabPtr()
	return t != nil && t.CanRevert()
}

// menuUndo rolls the active tab back one undo step.
func (a *App) menuUndo() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	// A multi-file edit claims the press when the cursor is in one of the
	// files it touched. Undoing just this tab would roll back one file of a
	// rename and leave the rest — a half-applied refactor with nothing on
	// screen to say so. When the group no longer holds together it degrades
	// LOUDLY and falls through, because an undo that does nothing reads as
	// broken. See workspaceedit.go.
	if a.wsGroupClaimsUndo(t) {
		ok, moved := a.wsGroupValid()
		if ok {
			a.undoWorkspaceGroup()
			return
		}
		a.wsDegradeToTabUndo(moved)
	}
	if !t.Undo() {
		a.flash("Nothing to undo")
	}
}

// menuRedo re-applies the most recently undone step.
func (a *App) menuRedo() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	// The mirror of menuUndo's claim, so a redo can't re-apply a rename in
	// one file while the other eleven stay unwound.
	if a.wsGroupClaimsRedo(t) {
		ok, moved := a.wsGroupValid()
		if ok {
			a.redoWorkspaceGroup()
			return
		}
		a.wsDegradeToTabUndo(moved)
	}
	if !t.Redo() {
		a.flash("Nothing to redo")
	}
}

// menuRevert rewinds the active tab all the way back to the buffer
// state we captured the moment the file was opened (or last reloaded).
// The pre-revert state goes onto the undo stack so an accidental click
// is recoverable with one Undo.
func (a *App) menuRevert() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	if !t.RevertFile() {
		a.flash("File matches its on-open state — nothing to revert")
		return
	}
	a.flash("Reverted to on-open state — Undo to recover")
}

// runCustomAction executes the custom action at idx. When the action
// declares prompts, the form modal opens first and the shell command
// runs only after the user submits — values are exported as KEY=VALUE
// env vars named after each prompt's Key. When prompts is empty the
// command runs immediately (the historical behaviour) and the action
// requires an open tab so $FILE / $FILENAME aren't blank.
//
// Either path runs in a goroutine so a slow scp / ssh can't freeze
// the UI; completion fires a customActionDoneEvent the main loop
// turns into a flash on success or an info modal on failure.
func (a *App) runCustomAction(idx int) {
	a.closeMenu()
	if idx < 0 || idx >= len(a.customActions) {
		return
	}
	act := a.customActions[idx]

	// No "is a file open?" guard: custom actions are user-defined
	// shell, and we don't second-guess what their command line
	// needs. A $FILE-dependent command run without a tab open will
	// fail with a real error and route through the info modal,
	// which is more honest than disabling actions like
	// "brew upgrade ..." that don't touch FILE at all.
	if len(act.Prompts) == 0 {
		a.execCustomAction(act, nil)
		return
	}

	a.openForm(act.Label, act.Prompts, func(app *App, values map[string]string) {
		app.execCustomAction(act, values)
	})
}

// execCustomAction is the actual shell-out. Pulled out of
// runCustomAction so both the prompt-less and prompted paths share
// the env-var, logging, and event-posting wiring without diverging.
func (a *App) execCustomAction(act customactions.Action, promptValues map[string]string) {
	vars := a.captureActionVars()
	env := append(os.Environ(), vars.envSlice()...)
	env = append(env, promptValuesEnv(act.Prompts, promptValues)...)

	a.flash(act.Label + "…")
	scr := a.screen
	go func() {
		started := time.Now()
		cmd := exec.Command("sh", "-c", act.Command)
		cmd.Env = env
		out, runErr := cmd.CombinedOutput()
		duration := time.Since(started)

		// Always log — success or failure — so the user can scroll
		// back through actions.log when something goes sideways.
		// Best-effort: a log-write failure must not eat the action's
		// real error.
		_ = customactions.AppendLog(customactions.LogPath(), customactions.RunRecord{
			Time:     started,
			Duration: duration,
			Label:    act.Label,
			Command:  act.Command,
			File:     vars.File,
			Filename: vars.Filename,
			ExitErr:  runErr,
			Output:   out,
		})

		_ = scr.PostEvent(&customActionDoneEvent{
			when:   time.Now(),
			label:  act.Label,
			err:    runErr,
			output: out,
		})
	}()
}

// menuSave runs the Save action and dismisses the menu.
func (a *App) menuSave() {
	a.closeMenu()
	a.saveActiveTab()
}

// menuSaveAndClose saves the active tab and then closes it. If the save
// fails the close is aborted so we don't lose the user's edits.
func (a *App) menuSaveAndClose() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	// Guarded like every other write (reconcile.go). The resume re-runs
	// the whole gesture — save AND close — because that is what the user
	// asked for; answering the prompt should not leave them with a saved
	// file and an open tab.
	if !a.saveGuard(tab, func(app *App) { app.menuSaveAndClose() }) {
		return
	}
	if err := tab.Save(); err != nil {
		a.flash(fmt.Sprintf("Save failed: %v", err))
		return
	}
	a.refreshGitStatus()
	a.flash(fmt.Sprintf("Saved %s — closed", filepath.Base(tab.Path)))
	a.closeTab(a.activeTab)
}

// menuClose closes the active tab via the same dirty-tab confirmation flow
// used by clicking the × on the tab.
func (a *App) menuClose() {
	a.closeMenu()
	a.requestCloseTab(a.activeTab)
}

// menuCopy copies the current selection.
func (a *App) menuCopy() {
	a.closeMenu()
	a.copySelection()
}

// menuCut cuts the current selection.
func (a *App) menuCut() {
	a.closeMenu()
	a.cutSelection()
}

// menuPaste pastes the editor's internal clipboard at the cursor.
func (a *App) menuPaste() {
	a.closeMenu()
	a.pasteClipboard()
}

// menuToggleLineComment comments or uncomments the active line selection.
func (a *App) menuToggleLineComment() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	changed, ok := tab.ToggleLineComment()
	if !ok {
		a.flash("No line comment syntax for this file")
		return
	}
	if !changed {
		a.flash("No non-blank lines to comment")
		return
	}
	a.flash("Toggled line comment")
}

// menuRefreshTree forces an immediate sidebar reload. Currently unwired
// from the menu — the 10s background poller covers the common case — but
// the method is kept so re-adding the menu row (see menuItems) only
// requires uncommenting one line.
func (a *App) menuRefreshTree() {
	a.closeMenu()
	a.tree.Refresh()
	a.flash("File tree refreshed")
}

// menuToggleSidebar shows or hides the file explorer panel. The editor and
// tab bar reflow to fill the freed cells when the panel is hidden, and
// snap back when it returns.
func (a *App) menuToggleSidebar() {
	a.closeMenu()
	a.sidebarShown = !a.sidebarShown
}

// sidebarToggleLabel returns the label the toggle row should display given
// the current sidebar state. Drawn dynamically by drawMenu.
func (a *App) sidebarToggleLabel() string {
	if a.sidebarShown {
		return "Hide file explorer"
	}
	return "Show file explorer"
}

// menuQuit exits the editor. When any tab has unsaved changes, opens the
// dirty-close modal so the user can pick Save (save all then quit),
// Discard (quit anyway), or Cancel. With no dirty tabs we exit straight
// away.
func (a *App) menuQuit() {
	a.closeMenu()
	dirty := a.dirtyTabCount()
	if dirty == 0 {
		a.quit = true
		return
	}
	var message string
	if dirty == 1 {
		// Find the one dirty tab so we can name it in the modal.
		for _, tab := range a.tabs {
			if tab.Dirty {
				name := filepath.Base(tab.Path)
				if name == "" || name == "." {
					name = "untitled"
				}
				message = name + " has unsaved changes. Save before quitting?"
				break
			}
		}
	} else {
		message = fmt.Sprintf("%d files have unsaved changes. Save all before quitting?", dirty)
	}
	a.openDirtyClose(
		"Unsaved changes",
		message,
		func(app *App) {
			// Only quit if every save succeeded — a half-saved exit
			// would silently lose work on whichever tab failed.
			if app.saveAllDirty() {
				app.quit = true
			}
		},
		func(app *App) { app.quit = true },
	)
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// draw paints the entire screen. Called once per event in the main loop.
// The action modal — if open — is drawn last so it sits on top of everything.
func (a *App) draw() {
	a.screen.Clear()

	if a.width < minWidth || a.height < minHeight {
		a.drawTooSmall()
		return
	}

	if a.sidebarShown {
		// Auto-fit first: it may change sidebarWidth, and every rect
		// helper below (tab bar, editor, panels, splitter) reads that. A
		// width derived after the fact would be one frame late — the row
		// the user just expanded would sit truncated until the next event.
		a.autoFitSidebar()
		// Re-sync the active-file highlight from the current tab every
		// draw so the bold row stays correct no matter which path
		// switched tabs (open, tab-bar click, close, nav back/forward).
		if tab := a.activeTabPtr(); tab != nil {
			a.tree.ActiveFile = tab.Path
		} else {
			a.tree.ActiveFile = ""
		}
		// Same re-sync for the keyboard-focus flag: the render is the
		// only consumer, and pushing it here beats chasing every place
		// treeFocus flips.
		a.tree.Focused = a.treeFocus
		sx, sy, sw, sh := a.sidebarRect()
		a.tree.Render(a.screen, a.theme, sx, sy, sw, sh)
		a.drawSplitter()
	}

	a.drawTabBar()

	if tab := a.activeTabPtr(); tab != nil {
		ex, ey, ew, eh := a.editorRect()
		tab.Render(a.screen, a.theme, ex, ey, ew, eh)
		// After Render, never before: Render is where EnsureVisible and
		// clampScroll settle ScrollY, and a bar drawn ahead of them would
		// report the previous frame's position.
		a.drawScrollbar()
	} else {
		a.drawEmptyEditor()
	}

	if a.gitPanel.open {
		a.drawGitPanel()
	}
	if a.compare.open {
		a.drawComparePanel()
	}
	if a.gitLog.open {
		a.drawGitLog()
	}
	if a.problems.open {
		a.drawProblems()
	}
	if a.term.open {
		a.drawTermPanel()
		a.drawTermSplitter()
	}
	if a.chat.open {
		a.drawChatPanel()
		a.drawChatSplitter()
	}
	if a.findOpen {
		a.drawFindBar()
	}
	// The pinned find-all panel draws with the other panels — it took
	// its rows out of the editor band exactly as the modal form does,
	// so the editor above/beside it has already been shortened.
	if a.findAllPin != nil {
		a.findAllPin.draw(a)
	}
	a.drawStatusBar()

	// The which-key overlay sits above the panels but below the menu
	// and modals — it never coexists with either (opening them closes
	// it), so the order here is belt and braces.
	if a.whichKey.open {
		a.drawWhichKey()
	}

	// The completion popup sits one layer higher again: it is anchored
	// INTO the editor body, so anything drawn after it would cover the
	// list the user is choosing from. Still below the menu and modals,
	// which close it (completionAvailable refuses while either is up).
	if a.completion.open {
		a.drawCompletion()
	}

	// The dwell tooltip shares that layer for the same reason — it is
	// anchored into the editor body — and never coexists with the
	// completion popup (hoverDwellEligible refuses while one is up).
	// It is always given the chance to draw, because that call is also
	// what clears the stamped rect when it is NOT visible.
	a.drawHoverDwell()

	// The commit receipt shares that passive layer: above the panels
	// (it is a report about what just happened, so nothing may cover
	// it) and below the menu and modals, which suppress it outright —
	// handleGitCommitReceipt declines to open under either, since a
	// receipt nobody can see would expire unread.
	a.drawCommitReceipt()

	// Overlay layer. The menu and the active modal are mutually
	// exclusive (closeAllModals enforces it), so at most one of these
	// draws — last, above everything else.
	if a.menuOpen {
		a.drawMenu()
	}
	if a.modal != nil {
		a.modal.draw(a)
	}
}

// iconsOn reports whether Nerd Font glyphs should render in places
// outside the file tree (e.g. the tab bar). The single source of
// truth is the file tree — App.loadUserConfig stamped the resolved
// auto/on/off decision onto t.IconsEnabled there, so consulting the
// tree keeps tabs and tree perfectly in sync (turning icons off via
// config.json hides them everywhere at once).
func (a *App) iconsOn() bool {
	return a.tree != nil && a.tree.IconsEnabled
}

// drawSplitter paints a 1-column vertical line at the editor-facing edge
// of the sidebar. Idle it sits in Subtle grey; while the user is dragging
// it brightens to Accent so the active grab handle is unmistakable.
func (a *App) drawSplitter() {
	x := a.splitterX()
	if x < 0 {
		return
	}
	fg := a.theme.Subtle
	if a.dragMode == "sidebar" {
		fg = a.theme.Accent
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(fg)
	for y := 0; y < a.height-1; y++ {
		a.screen.SetContent(x, y, '│', nil, style)
	}
}

// drawTermSplitter paints the left-docked terminal strip's resize
// handle — the same visual language as the sidebar splitter, on the
// strip's editor-facing (right) edge. No-op in the bottom-dock layout,
// where the header rule is the grab handle instead.
func (a *App) drawTermSplitter() {
	x := a.termSplitterX()
	if x < 0 {
		return
	}
	fg := a.theme.Subtle
	if a.dragMode == "termsplit" {
		fg = a.theme.Accent
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(fg)
	for y := 0; y < a.height-1; y++ {
		a.screen.SetContent(x, y, '│', nil, style)
	}
}

// drawMenuButton paints the ≡ icon in the leftmost cells of the tab bar.
// It's deliberately big and accent-coloured so it reads as a button.
func (a *App) drawMenuButton() {
	mx, my, mw, _ := a.menuButtonRect()
	bg := a.theme.SidebarBG
	fg := a.theme.Accent
	if a.menuOpen {
		// Visually press the button while the menu is up.
		bg = a.theme.Accent
		fg = a.theme.BG
	}
	style := tcell.StyleDefault.Background(bg).Foreground(fg).Bold(true)
	for cx := mx; cx < mx+mw; cx++ {
		a.screen.SetContent(cx, my, ' ', nil, style)
	}
	// Center the ≡ glyph in the button's mw cells.
	a.screen.SetContent(mx+mw/2, my, '≡', nil, style)
}

// drawEmptyEditor paints the placeholder shown when no tabs are open.
func (a *App) drawEmptyEditor() {
	ex, ey, ew, eh := a.editorRect()
	bg := a.theme.BG
	muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	bold := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text).Bold(true)
	for cy := ey; cy < ey+eh; cy++ {
		for cx := ex; cx < ex+ew; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil, muted)
		}
	}
	cy := ey + eh/2
	msg1 := "No file open"
	msg2 := "Click a file in the tree, or  ≡  for the menu"
	cx1 := ex + (ew-len([]rune(msg1)))/2
	for i, r := range msg1 {
		a.screen.SetContent(cx1+i, cy-1, r, nil, bold)
	}
	cx2 := ex + (ew-len([]rune(msg2)))/2
	for i, r := range msg2 {
		a.screen.SetContent(cx2+i, cy+1, r, nil, muted)
	}
	a.screen.HideCursor()
}

// drawTooSmall paints a centred error message when the terminal window is
// smaller than the editor's minimum supported size.
func (a *App) drawTooSmall() {
	style := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Error).Bold(true)
	for cy := 0; cy < a.height; cy++ {
		for cx := 0; cx < a.width; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil,
				tcell.StyleDefault.Background(a.theme.BG))
		}
	}
	msg := "Window too small — please resize"
	cy := a.height / 2
	cx := (a.width - len([]rune(msg))) / 2
	if cx < 0 {
		cx = 0
	}
	for i, r := range msg {
		if cx+i >= a.width {
			break
		}
		a.screen.SetContent(cx+i, cy, r, nil, style)
	}
	a.screen.HideCursor()
}

// drawMenu renders the action modal centered in the window. The
// item / divider / height layout comes from menuLayout so adding
// custom actions or new built-in groups doesn't require touching this
// function.
func (a *App) drawMenu() {
	mx, my, mw, mh := a.menuModalRect()
	items, dividers, _ := a.menuLayout()

	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	chevronStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.AccentSoft)

	// Fill the entire modal rect with the modal bg.
	for cy := my; cy < my+mh; cy++ {
		for cx := mx; cx < mx+mw; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Outer border.
	a.screen.SetContent(mx, my, '┌', nil, borderStyle)
	a.screen.SetContent(mx+mw-1, my, '┐', nil, borderStyle)
	a.screen.SetContent(mx, my+mh-1, '└', nil, borderStyle)
	a.screen.SetContent(mx+mw-1, my+mh-1, '┘', nil, borderStyle)
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, my, '─', nil, borderStyle)
		a.screen.SetContent(cx, my+mh-1, '─', nil, borderStyle)
	}
	for cy := my + 1; cy < my+mh-1; cy++ {
		a.screen.SetContent(mx, cy, '│', nil, borderStyle)
		a.screen.SetContent(mx+mw-1, cy, '│', nil, borderStyle)
	}

	// Horizontal dividers between action groups. The dy list comes from
	// menuLayout — including the always-on row under the title — so it
	// stays in sync with whatever rows are actually being drawn. The
	// title divider (dy 2) is part of the pinned header; the rest scroll
	// with the content and are skipped when they land outside the band.
	scroll := a.menuScrollOffset()
	for _, dy := range dividers {
		cy := my + dy
		if dy >= menuPinnedRows {
			cy -= scroll
			if cy < my+menuPinnedRows || cy > my+mh-2 {
				continue
			}
		}
		a.screen.SetContent(mx, cy, '├', nil, borderStyle)
		a.screen.SetContent(mx+mw-1, cy, '┤', nil, borderStyle)
		for cx := mx + 1; cx < mx+mw-1; cx++ {
			a.screen.SetContent(cx, cy, '─', nil, borderStyle)
		}
	}

	// Title row: " Menu" on the left, "esc " on the right.
	drawAt(a.screen, mx+1, my+1, " Menu", titleStyle)
	hint := "esc "
	drawAt(a.screen, mx+mw-1-len([]rune(hint)), my+1, hint, mutedStyle)

	// Version stamp baked into the bottom border, right-aligned. A small
	// pad of dashes is left between the version text and the corner so it
	// reads as part of the frame rather than a label awkwardly butted up
	// against the border.
	verLabel := " v" + version.Version + " "
	verLen := len([]rune(verLabel))
	verX := mx + mw - 2 - verLen
	if verX > mx+1 {
		drawAt(a.screen, verX, my+mh-1, verLabel, mutedStyle)
	}

	// Action rows. Hovered (enabled) rows get a tinted full-width
	// background so they read like a hovered button in a GUI menu.
	// Section headers carry a fold chevron (▾ open / ▸ folded) in the
	// mx+2 gutter and a bold accent title; item rows leave that gutter
	// empty so they read as nested under the header above them.
	hoverBg := a.theme.Selection
	hoverStyle := tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.Text).Bold(true)
	hoverChevStyle := tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.AccentSoft).Bold(true)
	headerStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	for i, item := range items {
		cy := my + item.relY - scroll
		if cy < my+menuPinnedRows || cy > my+mh-2 {
			continue // scrolled out of the clamped modal
		}
		enabled := item.enabled(a)
		hovered := enabled && i == a.hoveredMenuRow

		var labelStyle, chevStyle tcell.Style
		switch {
		case hovered:
			// Paint the row's interior with the hover background first.
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, cy, ' ', nil, hoverStyle)
			}
			labelStyle = hoverStyle
			chevStyle = hoverChevStyle
		case item.header:
			labelStyle = headerStyle
			chevStyle = chevronStyle
		case enabled:
			labelStyle = bgStyle
			chevStyle = chevronStyle
		default:
			labelStyle = mutedStyle
			chevStyle = mutedStyle
		}
		// Dynamic label (e.g. the file-explorer toggle row) takes precedence
		// over the static one when present.
		label := item.label
		if item.labelFor != nil {
			label = item.labelFor(a)
		}
		// A header owns the fold chevron and never shows a shortcut; an
		// item's gutter stays blank so the hierarchy reads at a glance.
		if item.header {
			chev := "▾"
			if a.sectionCollapsed(item.label) {
				chev = "▸"
			}
			drawAt(a.screen, mx+2, cy, chev, chevStyle)
			drawAt(a.screen, mx+4, cy, label, labelStyle)
			continue
		}
		drawAt(a.screen, mx+4, cy, label, labelStyle)
		// Shortcut hint, right-aligned like a GUI menu's accelerator
		// column. Always muted — the label carries the row's state
		// (enabled / hovered); the hint is a whisper either way — but
		// on the hover background so it doesn't punch a hole in the
		// highlight bar. Skipped when a long label would collide.
		if item.shortcut != "" {
			scX := mx + mw - 2 - len([]rune(item.shortcut))
			if scX > mx+4+len([]rune(label))+1 {
				scStyle := mutedStyle
				if hovered {
					scStyle = tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.Muted)
				}
				drawAt(a.screen, scX, cy, item.shortcut, scStyle)
			}
		}
	}

	// Scroll indicators: ▲ on the pinned title divider when rows are
	// hidden above, ▼ on the bottom border when rows are hidden below —
	// without them a clipped menu reads as the whole menu.
	if scroll > 0 {
		drawAt(a.screen, mx+(mw-3)/2, my+2, " ▲ ", chevronStyle)
	}
	if scroll < a.menuMaxScroll() {
		drawAt(a.screen, mx+(mw-3)/2, my+mh-1, " ▼ ", chevronStyle)
	}

	a.screen.HideCursor()
}

// drawStatusText writes s left-aligned into the status bar at (x, y) with a
// max width of maxW cells. Truncates rather than wraps.
func drawStatusText(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		if col >= maxW {
			return
		}
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// drawAt writes s starting at (x, y) without bounds checking. Callers are
// expected to keep the string within the rectangle they're drawing into.
func drawAt(scr tcell.Screen, x, y int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// detectLangLabel returns a short label for the active file's language —
// just the file extension, or "text" when there is no path or extension.
func detectLangLabel(path string) string {
	if path == "" {
		return "text"
	}
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return "text"
	}
	return ext
}
