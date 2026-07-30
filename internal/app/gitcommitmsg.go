// =============================================================================
// File: internal/app/gitcommitmsg.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitcommitmsg.go is the git panel's commit surface: committing the
// files you ticked (not just whatever happens to be in the index), and
// asking the CURRENT chat agent to draft the message for exactly those
// files.
//
// The two halves are deliberately separable — the commit prompt works
// with no agent installed, and a suggestion is only ever a pre-filled
// message the user can edit or discard. Nothing commits without an
// explicit Enter on the prompt.
//
// House patterns in play:
//
//   - Async git (gitcmd.go's rule): the diff for the agent is collected
//     off-loop and arrives as a gitCommitDiffEvent; the commit itself
//     goes through runGitCmdSeq so stage-then-commit is one gesture,
//     one flash, one failure modal.
//   - The chat panel is the ONE agent surface (copilot_chat.go's rule):
//     a suggestion is a normal visible turn in the panel, not a hidden
//     side-channel session. The user watches it stream, can stop it,
//     and the request stays in the transcript afterwards as the record
//     of what the message was drafted from.
//   - Answers are claimed by generation + transcript position, the same
//     staleness discipline as every other chat result: a suggestion
//     that outlives its connection (or its turn) is dropped, never
//     applied to whatever text happens to be trailing.

package app

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// commitDiffMaxBytes caps what the agent is shown. A commit message
	// comes from the SHAPE of a change, which the stat plus the first
	// pages of patch already carry; feeding a 500KB refactor costs
	// premium requests to describe what the truncated head said.
	commitDiffMaxBytes = 32 << 10

	// commitNewFileMaxBytes caps ONE untracked file's contents. New
	// files have no diff at all, so their text is what tells the agent
	// what was added — but a vendored blob shouldn't eat the budget.
	commitNewFileMaxBytes = 4 << 10

	// commitSubjectMaxRunes trims a runaway suggestion. The prompt asks
	// for a subject line; an agent that answers with a paragraph gets
	// its first line clipped rather than overflowing the input field.
	commitSubjectMaxRunes = 120
)

// -----------------------------------------------------------------------------
// Committing the panel's selection
// -----------------------------------------------------------------------------

// gitCommitLabel names what a commit will touch: the target set when
// the panel has one, "staged changes" when the caller means the index.
// One helper so the picker row, the prompt title, and the flash can't
// drift into three different descriptions of the same commit.
func gitCommitLabel(files []gitPanelFile) string {
	if len(files) == 0 {
		return "staged changes"
	}
	return gitPanelTargetLabel(files)
}

// openCommitPrompt asks for the message and commits on Enter. initial
// pre-fills the field — empty for a hand-written message, the agent's
// draft when a suggestion landed. promptModal rejects an empty submit,
// so there's no empty-message guard here.
func (a *App) openCommitPrompt(files []gitPanelFile, initial string) {
	a.openPrompt("Commit "+gitCommitLabel(files), "message", initial, func(app *App, msg string) {
		app.gitCommitFiles(files, msg)
	})
}

// gitCommitFiles commits the given targets, or the index when there are
// none. The target form stages first (`add` then `commit -- paths`)
// because the panel's selection is a work-tree statement — "commit
// these files" has to include the edits sitting outside the index, or
// the row would silently commit a stale version. Pathspec-limited
// commit leaves anything else already staged alone, which is what makes
// the row safe to use on a half-staged tree.
func (a *App) gitCommitFiles(files []gitPanelFile, msg string) {
	if len(files) == 0 {
		a.runGitCmd("Commit", "commit", "-m", msg)
		return
	}
	what := gitPanelTargetLabel(files)
	paths := gitPanelPaths(files)
	a.runGitCmdSeq("Commit "+what, [][]string{
		append([]string{"add", "--"}, paths...),
		append([]string{"commit", "-m", msg, "--"}, paths...),
	})
}

// -----------------------------------------------------------------------------
// Asking the agent
// -----------------------------------------------------------------------------

// commitSuggestReq is the in-flight "draft me a commit message" turn.
// It remembers what the message is FOR (so the prompt commits the same
// set the diff came from), which connection generation asked (a torn
// down connection's answer is not ours), and where the transcript stood
// at send time — the answer is whatever agent prose appears after that
// mark, never an older message that happens to be trailing.
type commitSuggestReq struct {
	files []gitPanelFile
	seq   int
	mark  int
}

// gitCommitDiffEvent carries a collected diff from the goroutine that
// forked git to the main loop, which is the only place App.chat may be
// touched.
type gitCommitDiffEvent struct {
	when  time.Time
	files []gitPanelFile
	diff  string
}

// When satisfies the tcell.Event interface.
func (e *gitCommitDiffEvent) When() time.Time { return e.when }

// canSuggestCommitMsg gates the picker row: an agent has to be allowed
// to run (the Copilot kill switch) and not already known-unavailable.
// A dead agent is still offered while it has never been started — the
// first attempt is what discovers the binary.
func (a *App) canSuggestCommitMsg() bool {
	return a.gitIsRepo && a.chatAgentEnabled() && !a.chat.dead
}

// hasSuggestableCommit gates the ≡ row: an agent that can run, plus
// something to describe — the panel's selection, or the index. Without
// either there is no change to draft a message for.
func (a *App) hasSuggestableCommit() bool {
	return a.canSuggestCommitMsg() && (len(a.gitPanelTargets()) > 0 || a.gitHasStaged)
}

// menuGitSuggestCommit is the ≡ / command-palette entry point. It takes
// the same targets the panel's Actions row would, so both surfaces
// describe the same change.
func (a *App) menuGitSuggestCommit() {
	a.closeMenu()
	a.gitPanelSuggestCommit(a.gitPanelTargets())
}

// gitPanelSuggestCommit opens the chat panel, collects the diff for the
// targets off-loop, and lets handleGitCommitDiff send the turn. The
// panel opens FIRST so the user sees where the answer is coming from —
// a request that streams into a hidden panel looks like a hang.
func (a *App) gitPanelSuggestCommit(files []gitPanelFile) {
	if !a.gitIsRepo {
		return
	}
	if a.chat.turnActive {
		a.flash(a.chatAgent().name + " is answering — ⏹ to stop it first")
		return
	}
	a.chatOpenPanel() // flashes its own reason when unavailable
	if a.chat.dead || !a.chatAgentEnabled() {
		return
	}
	if a.screen == nil || a.rootDir == "" {
		return
	}
	a.flash("Asking " + a.chatAgent().name + " for a commit message…")
	scr := a.screen
	root := a.rootDir
	targets := append([]gitPanelFile(nil), files...)
	go func() {
		diff := loadCommitDiff(root, targets)
		_ = scr.PostEvent(&gitCommitDiffEvent{when: time.Now(), files: targets, diff: diff})
	}()
}

// handleGitCommitDiff turns a collected diff into a chat turn. Runs on
// the main loop only. The transcript gets a short human-readable ask
// (not the whole diff — the panel is a narrow strip), while the wire
// prompt carries the patch; the pending request is what ties the
// eventual answer back to this commit.
func (a *App) handleGitCommitDiff(e *gitCommitDiffEvent) {
	if strings.TrimSpace(e.diff) == "" {
		a.flash("Nothing to describe — no diff for " + gitCommitLabel(e.files))
		return
	}
	a.chatAppendMsg(chatMsg{
		role: chatRoleUser,
		text: "Suggest a commit message for " + gitCommitLabel(e.files),
	})
	switch {
	case a.chatReady():
		a.chat.commitSuggest = &commitSuggestReq{
			files: e.files,
			seq:   a.chat.connSeq,
			mark:  len(a.chat.msgs),
		}
		a.chatSendPrompt(commitSuggestPrompt(e.files, e.diff))
	case a.chat.starting:
		// The handshake is still in flight. Queue like the composer
		// does — but without a pending request, because the queued
		// send happens under a generation we can't name yet; the
		// answer then simply lands in the transcript to copy.
		a.chat.queuedPrompt = commitSuggestPrompt(e.files, e.diff)
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "starting " + a.chatAgent().name + " chat — the request will send shortly"})
	default:
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: a.chatAgent().name + " chat is not running — " + chatAgentRetryHint + " to restart"})
	}
}

// commitSuggestPrompt is the wire prompt. It over-specifies the output
// shape on purpose: the message lands in a SINGLE-LINE input field, so
// a chatty answer ("Here's a good commit message:") or a fenced block
// would have to be stripped back off by commitSubject anyway. Asking
// for one bare line is cheaper and more reliable than parsing prose.
func commitSuggestPrompt(files []gitPanelFile, diff string) string {
	var b strings.Builder
	b.WriteString("Write a git commit message for the change below.\n")
	b.WriteString("Answer with ONLY the commit subject: one line, imperative mood ")
	b.WriteString("(\"Add\", \"Fix\", \"Move\"), at most 72 characters, no quotes, ")
	b.WriteString("no code fences, no explanation, no trailing period.\n")
	if len(files) > 0 {
		b.WriteString("\nFiles:\n")
		for _, f := range files {
			b.WriteString("- " + f.Rel + " (" + strings.TrimSpace(f.Code) + ")\n")
		}
	}
	b.WriteString("\nDiff:\n```diff\n")
	b.WriteString(diff)
	if !strings.HasSuffix(diff, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// loadCommitDiff assembles what the agent is shown. `diff HEAD` so the
// staged and unstaged halves of a file arrive as ONE change — the panel
// row is one file to the user, and a message drafted from half of it
// would describe half the commit. Untracked targets have no diff at
// all, so their contents are appended under a marker; without that, a
// commit that is entirely new files reads to the agent as an empty
// change. Best-effort throughout (gitstatus.go's rule): git failures
// yield whatever was gathered, and an empty result is reported by the
// caller as "nothing to describe" rather than an error modal.
func loadCommitDiff(rootDir string, files []gitPanelFile) string {
	var parts []string

	tracked := gitPanelFilter(files, gitPanelTracked)
	if len(files) == 0 || len(tracked) > 0 {
		args := []string{"-C", rootDir, "diff", "--no-color", "--stat", "--patch"}
		if len(files) == 0 {
			args = append(args, "--cached")
		} else {
			args = append(args, "HEAD", "--")
			args = append(args, gitPanelPaths(tracked)...)
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil && len(files) > 0 {
			// No HEAD yet (unborn branch): every tracked path is new,
			// so compare against the index instead of a commit that
			// doesn't exist.
			retry := []string{"-C", rootDir, "diff", "--no-color", "--stat", "--patch", "--"}
			retry = append(retry, gitPanelPaths(tracked)...)
			out, _ = exec.Command("git", retry...).Output()
		}
		if s := strings.TrimRight(string(out), "\n"); s != "" {
			parts = append(parts, s)
		}
	}

	for _, f := range gitPanelFilter(files, func(f gitPanelFile) bool {
		return !gitPanelTracked(f) && gitPanelOnDisk(f)
	}) {
		body, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		text := truncateAtLine(string(body), commitNewFileMaxBytes)
		parts = append(parts, "--- new file: "+f.Rel+" ---\n"+strings.TrimRight(text, "\n"))
	}

	return truncateAtLine(strings.Join(parts, "\n\n"), commitDiffMaxBytes)
}

// -----------------------------------------------------------------------------
// Claiming the answer
// -----------------------------------------------------------------------------

// chatCommitSuggestDone is handleChatTurnDone's tail: if this turn was a
// commit-message request, lift the draft out of the transcript and open
// the commit prompt with it. Returns having consumed the request either
// way — a suggestion is for the turn that asked, and re-using it on a
// later answer would put an unrelated sentence in a commit.
func (a *App) chatCommitSuggestDone(e *chatTurnDoneEvent) {
	req := a.chat.commitSuggest
	if req == nil || req.seq != a.chat.connSeq {
		return
	}
	a.chat.commitSuggest = nil
	if e.err != nil || e.stopReason == "cancelled" {
		return // handleChatTurnDone already wrote the reason
	}
	subject := commitSubject(a.chatAgentTextSince(req.mark))
	if subject == "" {
		a.flash("No commit message suggested")
		return
	}
	// Never steal the modal slot from a permission prompt the agent is
	// blocked on — that request has to be answered for anything to
	// proceed. The draft stays readable in the transcript.
	if a.chat.permModal != nil || len(a.chat.permQueue) > 0 {
		a.flash("Commit message suggested — see the chat panel")
		return
	}
	a.openCommitPrompt(req.files, subject)
}

// chatAgentTextSince returns the agent prose written after transcript
// position mark, newest message last. Position, not "the last message",
// because a turn can end with a tool note; and a trim (chatAppendMsg's
// cap) shifts every index, which the clamp absorbs by returning
// nothing rather than someone else's text.
func (a *App) chatAgentTextSince(mark int) string {
	if mark < 0 || mark > len(a.chat.msgs) {
		return ""
	}
	var parts []string
	for _, m := range a.chat.msgs[mark:] {
		if m.role == chatRoleAgent && strings.TrimSpace(m.text) != "" {
			parts = append(parts, m.text)
		}
	}
	return strings.Join(parts, "\n")
}

// commitSubject reduces an agent's answer to the one line that goes in
// the input field: fences dropped, a leading label ("Commit message:")
// dropped, bullets and surrounding quotes stripped. Agents mostly obey
// the prompt's "one bare line", but the field is single-line and this
// is the difference between Enter committing a message and Enter
// committing a markdown fence.
func commitSubject(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "```") {
			continue
		}
		if i := strings.Index(strings.ToLower(ln), "commit message:"); i >= 0 {
			ln = strings.TrimSpace(ln[i+len("commit message:"):])
			if ln == "" {
				continue
			}
		}
		ln = strings.TrimLeft(ln, "-*• \t")
		ln = strings.Trim(ln, "`")
		ln = strings.TrimSpace(ln)
		if len(ln) >= 2 {
			if (ln[0] == '"' && ln[len(ln)-1] == '"') || (ln[0] == '\'' && ln[len(ln)-1] == '\'') {
				ln = strings.TrimSpace(ln[1 : len(ln)-1])
			}
		}
		if ln == "" {
			continue
		}
		if r := []rune(ln); len(r) > commitSubjectMaxRunes {
			ln = string(r[:commitSubjectMaxRunes])
		}
		return ln
	}
	return ""
}
