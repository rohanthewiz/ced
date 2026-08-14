// =============================================================================
// File: internal/app/workspaceedit.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// workspaceedit.go applies a server-authored WorkspaceEdit — a set of text
// edits spanning files the user may never have opened — as ONE gesture the
// user can undo with one press.
//
// It is a PRIMITIVE, not a verb. Rename and code actions are the two things
// that will call it, and both reduce to about thirty lines once this exists,
// which is the argument for building it properly rather than special-casing
// rename: it pays for two.
//
//	verb ──► applyServerEdit(edit, label, req)
//	              │
//	              ├── planWorkspaceEdit   validate + read + convert. WRITES NOTHING.
//	              ├── confirm             only when it touches files with no tab
//	              ├── applyWorkspaceEdit  buffers first, then disk, rollback on failure
//	              └── arm wsGroup + report into the Find-all panel
//
// Four decisions shape the whole file.
//
// IT OPENS NO TABS. A rename can touch a dozen files, and opening each one
// would fire didOpen for the LSP and Copilot, every plugin's open hook and a
// syntax pass, then leave a DIRTY tab behind — twelve modal round-trips at
// quit, most of them not even laid out on the tab strip. That is the cost
// find-in-project already refuses to pay for the same reason. Files with no
// tab are loaded into a DETACHED editor.Tab, edited, and written; files with
// a tab are edited in their BUFFER and left dirty for the user to save.
//
// THE OPEN BUFFER OUTRANKS DISK, ALWAYS. The server answered from the text
// ced synced to it, which for an open tab includes unsaved edits. Writing
// that file's disk copy behind a dirty buffer would apply coordinates to
// text they were never measured against — the same rule referenceHits and
// the chat attachments follow, and here it is the difference between a
// correct rename and a corrupted file.
//
// VALIDATE EVERYTHING, THEN APPLY. A half-applied rename does not compile,
// and the file that failed is the one nobody notices. So planning does every
// read, every guard and every conversion while writing nothing, and a single
// refusal kills the whole edit naming the file and the reason. The disk
// writes that follow are individually atomic but not collectively so, which
// is what the rollback is for.
//
// THE UNDO JOURNAL IS ONE SLOT. Undo is per-Tab everywhere else in this
// codebase and there is no transaction concept to build on, so the group
// lives here: one participant per file, each carrying what has to still be
// true for the group to be honest. Plain undo in a participant tab claims
// the WHOLE group — because the alternative is a reflex Esc-u silently
// leaving eleven files renamed and one not.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/search"
)

// maxWorkspaceEditBytes caps the total source a single workspace edit may
// hold in memory. Every touched file's detached Tab is RETAINED so the group
// undo can rewrite it, so this is really a budget on what one undoable
// gesture costs — mirroring maxUndoBytes, which bounds the same thing per
// tab. A rename big enough to blow it is one ced would rather refuse than
// apply irreversibly.
const maxWorkspaceEditBytes = 32 << 20

// -----------------------------------------------------------------------------
// The staleness contract
// -----------------------------------------------------------------------------

// wsRequest is what a verb captures BEFORE it asks the server, so the apply
// can prove the answer still describes the workspace it was measured
// against. The empty value makes no claim, which is right for a
// server-INITIATED edit (workspace/applyEdit): there is no origin position
// to have gone stale, only the per-participant sync checks.
type wsRequest struct {
	// path/editRev are the document the question was asked about and that
	// tab's EditRev at ask time — the captured-(path, EditRev) rule the
	// plugin runs and the ghost text both follow.
	path    string
	editRev int
	// synced is a copy of lsp.syncedRev at ask time. It generalises the
	// same question to every open document the server could have consulted,
	// including ones the request never mentioned.
	synced map[string]int
}

// captureWSRequest snapshots the staleness contract for a verb about to ask
// a question. Callers flush first (lspFlushChange), so the captured
// revisions are the ones the server is about to answer from.
func (a *App) captureWSRequest(t *editor.Tab) wsRequest {
	req := wsRequest{synced: make(map[string]int, len(a.lsp.syncedRev))}
	if t != nil {
		req.path, req.editRev = t.Path, t.EditRev
	}
	for p, rev := range a.lsp.syncedRev {
		req.synced[p] = rev
	}
	return req
}

// -----------------------------------------------------------------------------
// The undo journal
// -----------------------------------------------------------------------------

// wsParticipant is one file an applied workspace edit touched, plus exactly
// what has to still be true for the group undo to be honest about it.
type wsParticipant struct {
	path string
	// tab is the OPEN tab, or the detached one loaded for this apply. Either
	// way it holds the undo snapshot, which is why the detached ones are
	// retained rather than thrown away after the write.
	tab  *editor.Tab
	open bool

	// undoDepth and editRev are checked together. EditRev alone can't say
	// the snapshot is still on top — trimUndo evicts from the BOTTOM, so a
	// stack shrinks without the buffer changing — and depth alone can't
	// either, since a push plus an eviction nets to zero.
	undoDepth int
	editRev   int

	// mtime is what ced wrote, for detached files only. Somebody else
	// touching the file since is a reason to leave it alone.
	mtime time.Time
}

// wsEditGroup is the single "undo the whole verb" slot.
//
// One slot, not a stack: the promise is that the gesture you just made is
// one press away, the same one-shot framing undoOriginal has. A second
// workspace edit replaces it — the first is still undoable per tab, it just
// stops being one gesture.
type wsEditGroup struct {
	label  string // "Rename foo → bar" — what the row and the flash say
	when   time.Time
	parts  []wsParticipant
	undone bool // flipped by a group undo, so the slot becomes a redo
}

// -----------------------------------------------------------------------------
// Planning
// -----------------------------------------------------------------------------

// wsFilePlan is one file's validated, converted share of a workspace edit.
type wsFilePlan struct {
	path  string
	tab   *editor.Tab
	open  bool
	edits []editor.Edit // rune coordinates, sorted, overlap-free
}

// wsEditPlan is a fully validated workspace edit. Building one performs
// every read and every check; applying one can only fail on a disk write.
type wsEditPlan struct {
	label  string
	files  []wsFilePlan
	count  int // total text edits
	toDisk int // files with no tab — the ones ced writes on the user's behalf
}

// applyServerEdit is the ONE call a verb makes. It plans, confirms when the
// edit reaches files the user has not opened, applies, reports, and arms the
// one-gesture undo.
//
// It reports whether the edit was ACCEPTED, not whether bytes have changed
// yet: an edit that writes files the user hasn't opened returns true with a
// confirmation on screen, and applies when they say yes. Callers use the
// result to decide whether they still owe the user a message, which is the
// question they actually have.
func (a *App) applyServerEdit(we *lsp.WorkspaceEdit, label string, req wsRequest) bool {
	return a.applyServerEditWith(we, label, req, nil)
}

// applyServerEditWith is applyServerEdit for a caller that needs the
// OUTCOME rather than the acceptance — done fires exactly once, with
// whether bytes actually changed and, when they didn't, a reason that reads
// as a sentence.
//
// It exists for the one caller that has somebody waiting on the answer: a
// server-initiated workspace/applyEdit is a REQUEST, and its response field
// is literally `applied`. Every other verb here asks a question and is told
// the answer, so "accepted" is all it needs; this one is told the answer and
// must report back, and the gap between the two is the confirmation prompt —
// the very case where acceptance and outcome are not the same thing yet.
//
// The three exits that make "exactly once" non-obvious are all here rather
// than in the caller: a refusal answers immediately, the no-confirm path
// answers after the commit, and the confirmation answers from whichever of
// its two hooks runs — which is what confirmModal.cancelHook is for.
func (a *App) applyServerEditWith(we *lsp.WorkspaceEdit, label string, req wsRequest, done func(*App, bool, string)) bool {
	// A nil done saves every exit below a nil check, and the closure is
	// dead code the compiler removes for the common caller.
	if done == nil {
		done = func(*App, bool, string) {}
	}
	if we == nil || we.IsEmpty() {
		a.flash(label + ": nothing to change")
		done(a, false, "nothing to change")
		return false
	}
	// Resource operations are refused BY NAME, never skipped. The capability
	// ced declares in initialize should stop a conforming server sending
	// them at all; this is what happens when one does anyway. Applying the
	// text edits and dropping the file moves would rewrite every identifier
	// in a package rename and never move the directory — a tree that no
	// longer builds, with nothing on screen to say why.
	if len(we.Resources) > 0 {
		reason := fmt.Sprintf("ced can't perform %s", describeResourceOps(we.Resources))
		a.flash(fmt.Sprintf("%s also needs %s — ced can't do that yet",
			label, describeResourceOps(we.Resources)))
		done(a, false, reason)
		return false
	}

	plan, err := a.planWorkspaceEdit(we, label, req)
	if err != nil {
		a.flash(label + ": " + err.Error())
		done(a, false, err.Error())
		return false
	}
	// Nothing has been touched at this point, which is what makes the
	// confirmation meaningful rather than decorative.
	if plan.toDisk == 0 {
		ok, reason := a.commitWorkspaceEdit(plan)
		done(a, ok, reason)
		return true
	}
	m := a.openConfirm("Apply "+label, plan.confirmMessage(), func(a *App) {
		ok, reason := a.commitWorkspaceEdit(plan)
		done(a, ok, reason)
	})
	m.cancelHook = func(a *App) { done(a, false, "the user declined the edit") }
	return true
}

// confirmMessage is what the prompt says before ced writes files the user
// never opened. It names the asymmetry rather than hiding it: open tabs are
// left dirty for the user to save, everything else lands on disk now.
func (p *wsEditPlan) confirmMessage() string {
	openN := len(p.files) - p.toDisk
	msg := fmt.Sprintf("%d edits in %d files.", p.count, len(p.files))
	if openN > 0 {
		msg += fmt.Sprintf(" %s edited in the buffer (unsaved).", plural(openN, "open tab", "open tabs"))
	}
	msg += fmt.Sprintf(" %s written to disk now.", plural(p.toDisk, "file", "files"))
	return msg
}

// describeResourceOps names what a server asked for that ced won't do, so
// the refusal is specific enough to act on.
func describeResourceOps(ops []lsp.ResourceOp) string {
	counts := map[lsp.ResourceOpKind]int{}
	for _, op := range ops {
		counts[op.Kind]++
	}
	var parts []string
	for _, k := range []lsp.ResourceOpKind{lsp.ResourceCreate, lsp.ResourceRename, lsp.ResourceDelete} {
		if counts[k] > 0 {
			parts = append(parts, plural(counts[k], string(k)+" file", string(k)+" files"))
		}
	}
	if len(parts) == 0 {
		return "filesystem changes"
	}
	return strings.Join(parts, " + ")
}

// planWorkspaceEdit validates a server edit against the workspace and
// converts it into buffer coordinates, reading every file it will touch.
// It writes nothing; every refusal names the file and the reason.
//
// It reads files ON THE MAIN LOOP, which is a deliberate exception to this
// codebase's "IO goes off-loop" rule. Which files are open, which buffers
// are dirty and which revisions are synced is main-loop-only state, and
// splitting the read from the validation across the loop boundary would
// re-open exactly the staleness window this function exists to close. The
// cost is bounded: tens of small source files, once per deliberate gesture,
// never per frame — saveAllDirty already writes every dirty tab inline.
func (a *App) planWorkspaceEdit(we *lsp.WorkspaceEdit, label string, req wsRequest) (*wsEditPlan, error) {
	// The origin claim first: if the document the question was asked about
	// has moved, every coordinate in the answer describes text that is gone.
	if req.path != "" {
		t := a.tabByPath(req.path)
		if t == nil || t.EditRev != req.editRev {
			return nil, fmt.Errorf("%s changed while the server was thinking — try again",
				filepath.Base(req.path))
		}
	}

	plan := &wsEditPlan{label: label}
	budget := 0
	for _, doc := range we.Documents {
		if len(doc.Edits) == 0 {
			continue
		}
		fp, size, err := a.planDocument(doc, req)
		if err != nil {
			return nil, err
		}
		budget += size
		if budget > maxWorkspaceEditBytes {
			return nil, fmt.Errorf("too large to apply as one undoable step (over %s)",
				humanSize(maxWorkspaceEditBytes))
		}
		plan.files = append(plan.files, fp)
		plan.count += len(fp.edits)
		if !fp.open {
			plan.toDisk++
		}
	}
	if len(plan.files) == 0 {
		return nil, fmt.Errorf("nothing to change")
	}
	// Path order for the disk writes, so a partial failure is reproducible
	// and the receipt reads the way search results do.
	sort.SliceStable(plan.files, func(i, j int) bool {
		return plan.files[i].path < plan.files[j].path
	})
	return plan, nil
}

// planDocument validates and converts one document's edits, returning the
// file plan and roughly what retaining it costs.
func (a *App) planDocument(doc lsp.DocumentEdit, req wsRequest) (wsFilePlan, int, error) {
	if doc.Path == "" {
		return wsFilePlan{}, 0, fmt.Errorf("server named a document ced can't open (%s)", doc.URI)
	}
	path, err := a.resolveInRoot(doc.Path)
	if err != nil {
		return wsFilePlan{}, 0, err
	}

	// An OPEN tab is edited in its buffer. Its guard is the sync contract:
	// the buffer has to be the text the server actually answered from.
	if t := a.tabByPath(path); t != nil {
		if err := a.checkSynced(t, doc, req); err != nil {
			return wsFilePlan{}, 0, err
		}
		edits, err := convertEdits(t.Buffer, doc.Edits)
		if err != nil {
			return wsFilePlan{}, 0, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		return wsFilePlan{path: path, tab: t, open: true, edits: edits},
			len(t.Buffer.String()), nil
	}

	// No tab: load a detached one. NewTab is what applies the open guards —
	// the size ceiling, the binary sniff, the BOM and line-ending detection
	// that Save re-emits — so a file ced would refuse to open is refused
	// here too, for the same reasons.
	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		return wsFilePlan{}, 0, fmt.Errorf("%s is not a file ced can edit", filepath.Base(path))
	}
	t, err := editor.NewTab(path)
	if err != nil {
		return wsFilePlan{}, 0, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	edits, err := convertEdits(t.Buffer, doc.Edits)
	if err != nil {
		return wsFilePlan{}, 0, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return wsFilePlan{path: path, tab: t, open: false, edits: edits}, int(info.Size()), nil
}

// checkSynced proves an open tab's buffer is the text the server answered
// from. Two comparisons, because they fail differently:
//
//   - EditRev == syncedRev says the buffer is what the server currently has.
//   - syncedRev == what it was at ask time says nothing was re-synced in
//     between, which a keystroke plus a fired 300ms debounce would do while
//     leaving the first comparison true again.
//
// The server's own version claim, when it makes one, is checked against
// ced's didChange counter — the exact number a server echoes back.
func (a *App) checkSynced(t *editor.Tab, doc lsp.DocumentEdit, req wsRequest) error {
	name := filepath.Base(t.Path)
	synced, announced := a.lsp.syncedRev[t.Path]
	if !announced {
		// A tab the server was never told about can still be a legitimate
		// target (a server may edit a file it read off disk), but only if
		// the buffer matches what is on disk — otherwise the coordinates
		// were measured against text nobody has.
		if t.Dirty {
			return fmt.Errorf("%s has unsaved changes the server hasn't seen", name)
		}
		return nil
	}
	if t.EditRev != synced || (req.synced != nil && req.synced[t.Path] != synced) {
		return fmt.Errorf("%s changed while the server was thinking — try again", name)
	}
	if doc.Version != nil && *doc.Version != a.lsp.versions[t.Path] {
		return fmt.Errorf("%s is at a different revision than the server's answer", name)
	}
	return nil
}

// convertEdits turns one document's UTF-16 ranges into rune coordinates
// against the buffer that is actually going to be edited, then normalises
// and checks the set.
//
// Each endpoint converts against ITS OWN line, which is the only thing that
// works for a multi-line range: there is no single line to measure both ends
// against, and clamping the far end to the near line's length would silently
// shrink the range. A line past the end of the buffer converts to no column
// at all and is left to ClampEnd, which reads it as "through the end".
func convertEdits(buf *editor.Buffer, edits []lsp.TextEdit) ([]editor.Edit, error) {
	out := make([]editor.Edit, 0, len(edits))
	for _, e := range edits {
		out = append(out, editor.Edit{
			Start:   runePos(buf, e.Range.Start),
			End:     runePos(buf, e.Range.End),
			NewText: e.NewText,
		})
	}
	editor.NormalizeEdits(out)
	if i := editor.OverlapIndex(out); i >= 0 {
		// The spec forbids overlapping edits within one document. Applying
		// them anyway wouldn't fail — it would produce plausible-looking
		// garbage, which is the only outcome worse than a refusal.
		return nil, fmt.Errorf("server sent overlapping edits at line %d", out[i].Start.Line+1)
	}
	return out, nil
}

// runePos converts one LSP position against buf. A line past the end keeps
// its number so ClampEnd can recognise it; only the column needs the line's
// runes, and there are none.
func runePos(buf *editor.Buffer, p lsp.Position) editor.Position {
	if p.Line < 0 {
		return editor.Position{}
	}
	if p.Line >= buf.LineCount() {
		return editor.Position{Line: p.Line, Col: 0}
	}
	return editor.Position{Line: p.Line, Col: lsp.RuneCol(buf.LineRunes(p.Line), p.Character)}
}

// humanSize renders a byte ceiling the way a refusal should read.
func humanSize(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%dMB", n>>20)
	}
	return fmt.Sprintf("%dKB", n>>10)
}

// -----------------------------------------------------------------------------
// Applying
// -----------------------------------------------------------------------------

// commitWorkspaceEdit applies a plan and reports, on the main loop. Split
// from applyServerEdit so the confirmation callback and the no-confirm path
// run exactly the same code.
//
// It returns whether bytes changed, and the reason when they didn't, so a
// server-initiated edit can answer its own request truthfully (see
// applyServerEditWith). The flash is unconditional either way — the user
// hears about it whether or not anybody else is listening.
func (a *App) commitWorkspaceEdit(p *wsEditPlan) (bool, string) {
	results, err := a.applyWorkspaceEdit(p)
	if err != nil {
		a.flash(p.label + ": " + err.Error())
		return false, err.Error()
	}
	// Shell out to the ordinary reconciliation rather than teaching this
	// path about the tree, git status and the finder index — the chat
	// filesystem's write rule.
	a.refreshTreeNow()
	a.reportWorkspaceEdit(p, results)

	openN := len(p.files) - p.toDisk
	msg := fmt.Sprintf("%s: %d edits in %s", p.label, p.count, plural(len(p.files), "file", "files"))
	if openN > 0 && p.toDisk > 0 {
		msg += fmt.Sprintf(" (%d written, %d unsaved)", p.toDisk, openN)
	} else if openN > 0 {
		msg += " (unsaved)"
	}
	a.flash(msg)
	return true, ""
}

// applyWorkspaceEdit applies a validated plan and arms the undo journal.
//
// ORDER IS THE ATOMICITY STORY. Buffer edits go first because they are pure
// memory and cannot fail, so by the time anything touches a disk the only
// remaining failure mode is a write. A failed write then rolls back
// everything already applied — through the same per-tab snapshots the group
// undo uses, which is what those retained detached Tabs are for.
func (a *App) applyWorkspaceEdit(p *wsEditPlan) (map[string][]editor.EditResult, error) {
	results := make(map[string][]editor.EditResult, len(p.files))
	group := &wsEditGroup{label: p.label, when: time.Now()}

	for _, fp := range p.files {
		res, ok := fp.tab.ApplyMultiEdit(fp.edits)
		if !ok {
			// Planning already rejected overlaps and empty sets, so this is
			// a bug rather than bad input — but a partially-applied set is
			// not something to continue from either.
			a.rollbackGroup(group)
			return nil, fmt.Errorf("%s: edits could not be applied", filepath.Base(fp.path))
		}
		results[fp.path] = res
		group.parts = append(group.parts, wsParticipant{
			path:      fp.path,
			tab:       fp.tab,
			open:      fp.open,
			undoDepth: fp.tab.UndoDepth(),
			editRev:   fp.tab.EditRev,
		})
	}

	// Now the disk half. Open tabs are deliberately NOT saved: writing them
	// would also commit whatever else the user had unsaved in that file, and
	// would bypass format-on-save's prompts and the dirty-close contract.
	// Auto-save picks them up two seconds later anyway.
	for i := range group.parts {
		part := &group.parts[i]
		if part.open {
			continue
		}
		// The clobber guard, in its non-interactive flavour (reconcile.go).
		// A detached participant was read moments ago during planning, so
		// a newer mtime means someone wrote the file inside that window —
		// and this is the one write path that cannot stop to ask, because
		// half of a cross-file rename is worse than none of it. Refusing
		// unwinds the whole group through the rollback below, which is
		// exactly the atomicity story this function is built around.
		if _, newer := diskNewerThanTab(part.tab); newer {
			a.rollbackGroup(group)
			return nil, fmt.Errorf("%s changed on disk — nothing was written",
				filepath.Base(part.path))
		}
		if err := part.tab.Save(); err != nil {
			a.rollbackGroup(group)
			return nil, fmt.Errorf("%s: %w", filepath.Base(part.path), err)
		}
		part.mtime = part.tab.Mtime
	}

	a.wsGroup = group
	return results, nil
}

// rollbackGroup unwinds a partially-applied edit, best effort. It runs the
// same per-tab Undo the group gesture does; a detached file is written back
// as well, since its change already reached the disk.
//
// A rollback that itself fails leaves the journal ARMED rather than clearing
// it, so the ≡ row is one more attempt rather than a dead end.
func (a *App) rollbackGroup(group *wsEditGroup) {
	var failed []string
	for i := len(group.parts) - 1; i >= 0; i-- {
		part := group.parts[i]
		if !part.tab.Undo() {
			failed = append(failed, filepath.Base(part.path))
			continue
		}
		if !part.open && !part.mtime.IsZero() {
			if err := part.tab.Save(); err != nil {
				failed = append(failed, filepath.Base(part.path))
			}
		}
	}
	if len(failed) > 0 {
		a.wsGroup = group
		a.openInfo("Rollback incomplete", []string{
			"These files could not be restored automatically:",
			"  " + strings.Join(failed, ", "),
			"",
			"The ≡ Code menu's undo row is still armed.",
		})
	}
}

// -----------------------------------------------------------------------------
// The receipt
// -----------------------------------------------------------------------------

// reportWorkspaceEdit opens the applied edits in the Find-all panel's
// project mode — the receipt that replaces opening a tab per file.
//
// Reusing that panel rather than building a list is the same argument
// lspreferences.go made: it already answers every question a cross-file
// result list raises (path rows, a strip that displaces the editor instead
// of covering it, the right dock, scrolling, the Esc contract, the mouse
// story), and heading is the one field a non-search producer may change.
func (a *App) reportWorkspaceEdit(p *wsEditPlan, results map[string][]editor.EditResult) {
	// Never steal the modal slot from something the user is typing into.
	if a.modal != nil || a.menuOpen {
		return
	}
	var hits []search.Hit
	for _, fp := range p.files {
		for _, r := range results[fp.path] {
			// The result is where the NEW text landed, so the row shows the
			// line as it is now with the change lit — which is what makes
			// the list a receipt rather than a record of what used to be.
			if fp.tab.Buffer == nil || r.Start.Line < 0 || r.Start.Line >= fp.tab.Buffer.LineCount() {
				continue
			}
			width := 0
			if r.End.Line == r.Start.Line {
				width = r.End.Col - r.Start.Col
			}
			hits = append(hits, search.Hit{
				Path:  fp.path,
				Line:  r.Start.Line,
				Col:   r.Start.Col,
				Width: max(width, 0),
				Text:  fp.tab.Buffer.Lines[r.Start.Line],
			})
		}
	}
	if len(hits) == 0 {
		return
	}
	m := &findAllModal{
		query:   p.label,
		tabIdx:  -1, // no single tab behind this list
		project: true,
		heading: "Changed by",
		rows:    a.projectSearchRows(hits),
		// No filter seed: this list's query is a LABEL ("Rename to x"), not
		// text the rows contain, so pre-filling would put a phrase in the
		// box that narrows to nothing the moment it is touched.
	}
	// The display order is derived, not implied by rows — see the note in
	// handleLSPReferences.
	m.rebuildView()
	a.openModal(m)
	m.ensureRowVisible(a)
}

// -----------------------------------------------------------------------------
// Group undo / redo
// -----------------------------------------------------------------------------

// wsGroupValid reports whether every participant is still exactly one step
// past the snapshot the edit filed — the precondition for calling the whole
// thing one gesture. The first file that fails is named, because "it isn't
// one step any more" is only actionable if you know which file moved.
func (a *App) wsGroupValid() (bool, string) {
	g := a.wsGroup
	if g == nil || len(g.parts) == 0 {
		return false, ""
	}
	for _, p := range g.parts {
		if p.tab == nil {
			return false, filepath.Base(p.path)
		}
		if p.tab.EditRev != p.editRev || p.tab.UndoDepth() != p.undoDepth {
			return false, filepath.Base(p.path)
		}
		if !p.open && !p.mtime.IsZero() {
			// Somebody else wrote the file after ced did. Rewriting it would
			// discard their change, which is not what undo means.
			if info, err := os.Stat(p.path); err != nil || !info.ModTime().Equal(p.mtime) {
				return false, filepath.Base(p.path)
			}
		}
	}
	return true, ""
}

// wsUndoAvailable gates the ≡ row: a group exists, hasn't been undone, and
// still holds together.
func (a *App) wsUndoAvailable() bool {
	if a.wsGroup == nil || a.wsGroup.undone {
		return false
	}
	ok, _ := a.wsGroupValid()
	return ok
}

// wsEditUndoLabel is the ≡ row's dynamic label. It names the verb and the
// file count, because "Undo workspace edit" says nothing about what would
// change — and this row rewrites files that may not be on screen.
func (a *App) wsEditUndoLabel() string {
	if a.wsGroup == nil {
		return "Undo multi-file edit"
	}
	return fmt.Sprintf("Undo %s (%s)", a.wsGroup.label, plural(len(a.wsGroup.parts), "file", "files"))
}

// wsGroupClaimsUndo reports whether plain undo, pressed with t active,
// should unwind the whole group instead of just this tab.
//
// This is the question the whole journal exists to answer. A user WILL press
// Esc-u in one of the renamed files; per-tab undo there would roll back one
// file and leave the other eleven renamed — a half-applied refactor, with
// nothing on screen to say so.
func (a *App) wsGroupClaimsUndo(t *editor.Tab) bool {
	if t == nil || a.wsGroup == nil || a.wsGroup.undone {
		return false
	}
	for _, p := range a.wsGroup.parts {
		if p.tab == t {
			return true
		}
	}
	return false
}

// wsGroupClaimsRedo is the mirror, so a redo can't re-apply the edit in one
// file while the other eleven stay unwound.
func (a *App) wsGroupClaimsRedo(t *editor.Tab) bool {
	if t == nil || a.wsGroup == nil || !a.wsGroup.undone {
		return false
	}
	for _, p := range a.wsGroup.parts {
		if p.tab == t {
			return true
		}
	}
	return false
}

// menuUndoWorkspaceEdit is the ≡ Code row — the path for the two cases plain
// undo can't serve: the active tab isn't a participant, or no participant is
// open at all because every touched file went straight to disk.
func (a *App) menuUndoWorkspaceEdit() {
	a.closeMenu()
	if a.wsGroup == nil {
		a.flash("No multi-file edit to undo")
		return
	}
	// The row's enabled predicate already excludes an undone group, and the
	// palette honours it — but a second undo here would pop a DIFFERENT step
	// out of every participant tab, which is the one failure mode worth a
	// belt-and-braces guard rather than a comment.
	if a.wsGroup.undone {
		a.flash(a.wsGroup.label + " is already undone")
		return
	}
	a.undoWorkspaceGroup()
}

// undoWorkspaceGroup unwinds the whole group and reports. Returns whether it
// ran; a group that no longer holds together refuses HERE and lets the
// caller degrade, rather than silently doing half of it.
func (a *App) undoWorkspaceGroup() bool {
	ok, moved := a.wsGroupValid()
	if !ok {
		a.flash(fmt.Sprintf("%s is no longer one step — %s changed since",
			a.wsGroup.label, moved))
		a.wsGroup = nil
		return false
	}
	g := a.wsGroup
	// Bottom-up over the participants for symmetry with the apply; each
	// tab's own Undo restores the snapshot filed before its edits.
	for i := len(g.parts) - 1; i >= 0; i-- {
		p := g.parts[i]
		if !p.tab.Undo() {
			continue
		}
		if !p.open {
			if err := p.tab.Save(); err != nil {
				a.flash(fmt.Sprintf("Undo %s: %s: %v", g.label, filepath.Base(p.path), err))
			}
		}
	}
	// Re-stamp what undo just produced, so a redo can verify the same way.
	for i := range g.parts {
		g.parts[i].undoDepth = g.parts[i].tab.UndoDepth()
		g.parts[i].editRev = g.parts[i].tab.EditRev
		if !g.parts[i].open {
			g.parts[i].mtime = g.parts[i].tab.Mtime
		}
	}
	g.undone = true
	a.refreshTreeNow()
	a.flash(fmt.Sprintf("Undid %s (%s)", g.label, plural(len(g.parts), "file", "files")))
	return true
}

// redoWorkspaceGroup re-applies a group undo, under the same validity rule.
func (a *App) redoWorkspaceGroup() bool {
	ok, moved := a.wsGroupValid()
	if !ok {
		a.flash(fmt.Sprintf("%s is no longer one step — %s changed since",
			a.wsGroup.label, moved))
		a.wsGroup = nil
		return false
	}
	g := a.wsGroup
	for i := range g.parts {
		p := g.parts[i]
		if !p.tab.Redo() {
			continue
		}
		if !p.open {
			if err := p.tab.Save(); err != nil {
				a.flash(fmt.Sprintf("Redo %s: %s: %v", g.label, filepath.Base(p.path), err))
			}
		}
	}
	for i := range g.parts {
		g.parts[i].undoDepth = g.parts[i].tab.UndoDepth()
		g.parts[i].editRev = g.parts[i].tab.EditRev
		if !g.parts[i].open {
			g.parts[i].mtime = g.parts[i].tab.Mtime
		}
	}
	g.undone = false
	a.refreshTreeNow()
	a.flash(fmt.Sprintf("Redid %s (%s)", g.label, plural(len(g.parts), "file", "files")))
	return true
}

// wsDegradeToTabUndo is what plain undo does when the active tab is a
// participant but the group no longer holds together. It says so and clears
// the slot, then lets the ordinary per-tab undo run.
//
// Falling through beats refusing — an undo that does nothing reads as broken
// — and announcing beats silence, which is the whole reason the group
// exists. Clearing is what stops a partially-unwound group from later
// half-applying the rest against text that has moved.
func (a *App) wsDegradeToTabUndo(moved string) {
	label := "multi-file edit"
	if a.wsGroup != nil {
		label = a.wsGroup.label
	}
	a.wsGroup = nil
	a.flash(fmt.Sprintf("%s is no longer one step — %s changed; undid this file only",
		label, moved))
}

// wsForgetTab drops a group the moment one of its participants is closed.
// A closed tab's undo stack goes with it, so the group could no longer be
// unwound as one gesture — and a journal that silently skips a file is the
// half-rename this design exists to prevent.
func (a *App) wsForgetTab(t *editor.Tab) {
	if a.wsGroup == nil || t == nil {
		return
	}
	for _, p := range a.wsGroup.parts {
		if p.tab == t {
			a.wsGroup = nil
			return
		}
	}
}
