// =============================================================================
// File: internal/app/conflictsides.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// conflictsides.go NAMES THE TWO SIDES of a merge conflict for every
// surface that offers a choice between them: the lens on each block, the
// `=======` label, the right-click rows, the ≡ pickers, the Conflicts
// panel's sides line and buttons, the whole-file verbs, the presence
// labels and the compare headers. One namer, so every door says the same
// thing about the same side.
//
// The model underneath stays POSITIONAL (editor/conflict.go): current is
// above the separator, incoming below, and git's --ours / --theirs index
// stages follow the same positions in every operation. What changes is
// only the WORDS. "Current" and "incoming" say where a side sits, not
// whose it is, and the question a resolver is asking is "which of these
// is my work?". So each side gets
//
//   - a ROLE that says whose it is in THIS operation, and
//   - a NAME that says where it came from (a branch, a short hash):
//
//	operation     above (current)        below (incoming)
//	───────────   ────────────────────   ────────────────────
//	merge         yours (main)           theirs (topic)
//	cherry-pick   yours (main)           picked (2a0370b)
//	revert        yours (main)           revert (2a0370b)
//	rebase        upstream (abc1234)     yours (2a0370b)
//	none known    current                incoming
//
// The rebase row is why this file exists. A rebase replays YOUR commits
// onto the upstream, so HEAD — the side above — is the upstream (plus
// your commits already replayed), and your own commit is the one coming
// in below. Positional words leave the user to know that; the role says
// it.
//
// Decisions worth knowing:
//
//   - THE ROLE IS COMPUTED, NOT git's "ours/theirs". git's words flip in
//     a rebase; "yours" here always points at the user's work, whichever
//     side that is.
//   - NO OPERATION, NO OWNERSHIP CLAIM. Unmerged files with no operation
//     in progress (a conflicted `stash pop`, `checkout -m`) keep the old
//     positional words exactly — guessing "yours" there could be wrong.
//   - NAMES FALL BACK TO THE MARKERS. The operation's own record (the
//     branch, MERGE_MSG, the stopped commit) names a side first; a block's
//     marker label fills in what that record lacks, and a side with
//     neither is just its role. "HEAD" is never shown as a name — it is
//     the jargon this file exists to translate.
//   - COLOURS STAY POSITIONAL. The lens buttons and the `=======` label
//     colour each side like its wash (green above, the incoming hue
//     below), so a word and the region it names always match. The label
//     sits on the separator's neutral grey, where some themes' greens
//     drop near 2.5:1 (Darcula, Solarized Light), so its runs are lifted
//     to 4.5:1 (liftToContrast) — same hue, readable.

package app

import (
	"strings"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// conflictNameMax caps a side's name in labels. A merge's names can be
// whole branch paths, and a lens set has to fit three of them on one row.
const conflictNameMax = 20

// conflictSide is one side as the UI names it.
type conflictSide struct {
	role string // "yours", "theirs", "upstream", "picked", "revert"; or "current" / "incoming"
	name string // "main", "topic", "2a0370b"; "" when nothing names it
}

// tag is the side's full name: "yours (main)", or the bare role.
func (s conflictSide) tag() string {
	if s.name == "" {
		return s.role
	}
	return s.role + " (" + elide(s.name, conflictNameMax) + ")"
}

// title is the role capitalised — the lens's short label and the start
// of a sentence.
func (s conflictSide) title() string {
	if s.role == "" {
		return ""
	}
	return strings.ToUpper(s.role[:1]) + s.role[1:]
}

// phrase names the side inside a sentence: "your side", "their side",
// "the picked side". The possessives read as English; every other role is
// an adjective.
func (s conflictSide) phrase() string {
	switch s.role {
	case "yours":
		return "your side"
	case "theirs":
		return "their side"
	}
	return "the " + s.role + " side"
}

// conflictSides is the pair, plus what the sides line needs to be exact.
type conflictSides struct {
	cur, inc conflictSide

	// known reports an operation in progress — the roles are ownership
	// words rather than the positional fallback.
	known bool

	// replayed marks a rebase past its first commit: the side above holds
	// the user's own already-replayed commits as well as the upstream.
	replayed bool
}

// positionalSides is the no-operation fallback: today's words, unchanged.
func positionalSides() conflictSides {
	return conflictSides{cur: conflictSide{role: "current"}, inc: conflictSide{role: "incoming"}}
}

// conflictSidesFromInfo names the sides from an operation's record and
// the checked-out branch. Pure, so the table above is testable without a
// repository.
func conflictSidesFromInfo(info gitOpInfo, branch string) conflictSides {
	if branch == "HEAD" {
		branch = "" // detached: "HEAD" is not a name worth showing
	}
	s := conflictSides{known: true}
	switch info.op {
	case "merge":
		s.cur = conflictSide{role: "yours", name: branch}
		s.inc = conflictSide{role: "theirs", name: nonEmpty(mergeMsgBranch(info.mergeMsg), info.short)}
	case "cherry-pick":
		s.cur = conflictSide{role: "yours", name: branch}
		s.inc = conflictSide{role: "picked", name: info.short}
	case "revert":
		s.cur = conflictSide{role: "yours", name: branch}
		s.inc = conflictSide{role: "revert", name: info.short}
	case "rebase":
		s.cur = conflictSide{role: "upstream", name: info.onto}
		s.inc = conflictSide{role: "yours", name: info.short}
		s.replayed = info.step > 1
	default:
		return positionalSides()
	}
	return s
}

// mergeMsgBranch pulls the merged ref's name out of MERGE_MSG's first
// line — "Merge branch 'topic'", "Merge remote-tracking branch
// 'origin/topic'", "Merge branch 'topic' of github.com:x/y" — or "" for
// a message that quotes nothing (an edited message, "Merge commit …"
// still quotes, so it names the commit).
func mergeMsgBranch(msg string) string {
	i := strings.IndexByte(msg, '\'')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(msg[i+1:], '\'')
	if j <= 0 {
		return ""
	}
	return msg[i+1 : i+1+j]
}

// conflictSides names the sides of the repository's current stop. The op
// comes from the status snapshot (a.gitOp), which every tick refreshes;
// the panel's recorded info supplies names only while it describes the
// same op — a stale record would name the wrong commit, and a role with
// no name is better than a wrong name.
func (a *App) conflictSides() conflictSides {
	info := a.conflictPanel.info
	if info.op != a.gitOp {
		info = gitOpInfo{op: a.gitOp}
	}
	return conflictSidesFromInfo(info, a.gitBranch)
}

// conflictSidesFor names the sides of one block: the operation's names,
// with any gap filled from the block's own marker labels.
func (a *App) conflictSidesFor(b editor.ConflictBlock) conflictSides {
	s := a.conflictSides()
	if !s.known {
		return s
	}
	if s.cur.name == "" {
		s.cur.name = markerName(b.CurrentLabel)
	}
	if s.inc.name == "" {
		s.inc.name = markerName(b.IncomingLabel)
	}
	return s
}

// markerName is a marker label's first word — the ref or short hash git
// wrote ("2a0370b" from "2a0370b (Fix parser crash)") — or "" for HEAD
// and for a label with nothing in it.
func markerName(label string) string {
	f := strings.Fields(label)
	if len(f) == 0 || f[0] == "HEAD" {
		return ""
	}
	return f[0]
}

// choiceLabel names a resolution in the sides' words: "Accept yours
// (main)", "Accept both, picked first". The choices that name no side
// keep the editor's label. With positional sides every label is exactly
// the editor's own, so the fallback changes nothing.
func (s conflictSides) choiceLabel(c editor.ConflictChoice) string {
	switch c {
	case editor.ChooseCurrent:
		return "Accept " + s.cur.tag()
	case editor.ChooseIncoming:
		return "Accept " + s.inc.tag()
	case editor.ChooseBothIncomingFirst:
		return "Accept both, " + s.inc.role + " first"
	}
	return c.Label()
}

// choiceShort is a choice's lens short label: the side's role ("Yours",
// "Picked") — when a narrow pane drops the names, the role is the word
// that still answers "which one is mine".
func (s conflictSides) choiceShort(c editor.ConflictChoice) string {
	switch c {
	case editor.ChooseCurrent:
		return s.cur.title()
	case editor.ChooseIncoming:
		return s.inc.title()
	}
	return ""
}

// note is the one-line statement of which side is which: "▲ yours (main)
// · ▼ picked (2a0370b)". The arrows mean above / below the separator in
// every block — the same direction the washes show.
func (s conflictSides) note() string {
	cur := s.cur.tag()
	if s.replayed {
		cur += " + your commits replayed so far"
	}
	return "▲ " + cur + " · ▼ " + s.inc.tag()
}

// separatorNotes are the `=======` label's runs for block b: above in the
// current side's colour, below in the incoming side's. A diff3 block has
// TWO separators with the base between them, so each gets its own pair —
// the `=======` of a diff3 block has the base above it, not the current
// side, and a label claiming otherwise would be the one wrong thing on
// the screen.
func (s conflictSides) separatorNotes(b editor.ConflictBlock, th theme.Theme) map[int][]editor.NoteRun {
	// Both marker lines share one wash (conflictRegionColor's neutral
	// grey), so one background answers the contrast question for all runs.
	bg := conflictRegionColor(th, editor.RegionMidMarker)
	above := editor.NoteRun{Text: "▲ " + s.cur.tag(), FG: liftToContrast(th.GitAdded, bg)}
	below := editor.NoteRun{Text: "  ▼ " + s.inc.tag(), FG: liftToContrast(th.AccentSoft, bg)}
	base := liftToContrast(th.Text, bg)
	if !b.HasBase() {
		return map[int][]editor.NoteRun{b.Mid: {above, below}}
	}
	return map[int][]editor.NoteRun{
		b.Base: {above, {Text: "  ▼ base", FG: base}},
		b.Mid:  {{Text: "▲ base", FG: base}, below},
	}
}
