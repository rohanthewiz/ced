// =============================================================================
// File: internal/app/gitcommitmsg_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCommitSubject_StripsAgentDressing pins the parser that turns an
// agent's answer into something a single-line field can hold: fences,
// labels, bullets, and quotes come off, a paragraph yields its first
// real line, and a runaway answer is clipped.
func TestCommitSubject_StripsAgentDressing(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Add commit suggestions", "Add commit suggestions"},
		{"```\nFix panic on empty diff\n```", "Fix panic on empty diff"},
		{"Commit message: Move parser into its own file", "Move parser into its own file"},
		{"- \"Drop the stale index guard\"", "Drop the stale index guard"},
		{"\n\n`Rename tab helper`\n\nBecause it read badly.", "Rename tab helper"},
		{"   \n\t\n", ""},
		{"```diff\n```", ""},
	}
	for _, c := range cases {
		if got := commitSubject(c.in); got != c.want {
			t.Errorf("commitSubject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("x", commitSubjectMaxRunes+40)
	if got := commitSubject(long); len([]rune(got)) != commitSubjectMaxRunes {
		t.Errorf("long subject kept %d runes, want %d", len([]rune(got)), commitSubjectMaxRunes)
	}
}

// TestCommitSuggestPrompt_CarriesFilesAndDiff pins the wire prompt: the
// agent is told to answer with one bare line, and both the file list
// and the patch travel with it — the whole point of the feature is that
// the draft describes THESE files.
func TestCommitSuggestPrompt_CarriesFilesAndDiff(t *testing.T) {
	files := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}
	got := commitSuggestPrompt(files, "@@ -1 +1 @@\n-old\n+new")
	for _, want := range []string{"ONLY the commit subject", "a.go (M)", "+new", "```diff"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}

// TestGitPanelActionItems_CommitAndSuggest pins the two new rows: a
// Commit row for the ticked set is always offered, and the Suggest row
// appears only when an agent could actually answer.
func TestGitPanelActionItems_CommitAndSuggest(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitPanel.files = []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}
	a.gitPanel.selected = 0

	// Chat dead (newTestApp's default): no suggestion is possible.
	got := actionLabels(a.gitPanelActionItems(a.gitPanelTargets()))
	if !strings.Contains(got, "Commit a.go…") {
		t.Errorf("commit row missing: %s", got)
	}
	if strings.Contains(got, "Suggest commit message") {
		t.Errorf("suggest row offered with a dead agent: %s", got)
	}

	wireChat(a)
	got = actionLabels(a.gitPanelActionItems(a.gitPanelTargets()))
	if !strings.Contains(got, "Suggest commit message for a.go (Copilot)") {
		t.Errorf("suggest row missing: %s", got)
	}
}

// TestGitCommitFiles_CommitsOnlyTheTargets pins the panel's commit
// semantics end to end: the ticked file is staged and committed with
// its work-tree edit, and an unrelated staged file is left in the index
// rather than swept into the commit.
func TestGitCommitFiles_CommitsOnlyTheTargets(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	other := filepath.Join(repo, "other.txt")
	writeFileT(t, other, "staged elsewhere\n")
	gitRun(t, repo, "add", "other.txt")
	writeFileT(t, file, "one\nCHANGED\n")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.gitCommitFiles([]gitPanelFile{{Path: file, Rel: "f.txt", Code: " M"}}, "Change f")
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(gitOut(t, repo, "log", "-1", "--pretty=%s"), "Change f")
	})

	if names := gitOut(t, repo, "show", "--name-only", "--pretty=", "HEAD"); names != "f.txt" {
		t.Errorf("commit touched %q, want just f.txt", names)
	}
	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "other.txt" {
		t.Errorf("index after commit = %q, want other.txt still staged", staged)
	}
}

// TestLoadCommitDiff_TrackedAndUntracked pins what the agent is shown:
// a tracked file's edit arrives as a patch even when only half of it is
// staged, and an untracked target contributes its contents (it has no
// diff at all, so without this the change would look empty).
func TestLoadCommitDiff_TrackedAndUntracked(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	writeFileT(t, file, "one\nSTAGED\n")
	gitRun(t, repo, "add", "f.txt")
	writeFileT(t, file, "one\nSTAGED\nUNSTAGED\n")
	fresh := filepath.Join(repo, "new.txt")
	writeFileT(t, fresh, "brand new line\n")

	diff := loadCommitDiff(repo, []gitPanelFile{
		{Path: file, Rel: "f.txt", Code: "MM"},
		{Path: fresh, Rel: "new.txt", Code: "??"},
	})
	for _, want := range []string{"STAGED", "UNSTAGED", "new file: new.txt", "brand new line"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}
}

// TestHandleGitCommitDiff_SendsTurn pins the send side: a collected
// diff becomes a real session/prompt carrying the patch, the transcript
// shows a short human-readable ask (not the whole diff), and the
// pending request remembers the files so the answer can commit them.
func TestHandleGitCommitDiff_SendsTurn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	files := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}

	a.handleGitCommitDiff(&gitCommitDiffEvent{when: time.Now(), files: files, diff: "@@ -1 +1 @@\n+new"})

	// The turn call runs on a goroutine (chatSendPrompt's contract).
	waitForCopilot(t, "session/prompt", func() bool {
		return len(fake.paramsFor("session/prompt")) == 1
	})
	raws := fake.paramsFor("session/prompt")
	var p struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(raws[0], &p); err != nil {
		t.Fatalf("unmarshal prompt: %v", err)
	}
	if len(p.Prompt) == 0 || !strings.Contains(p.Prompt[0].Text, "+new") {
		t.Errorf("prompt did not carry the diff: %+v", p.Prompt)
	}
	last := a.chat.msgs[0]
	if last.role != chatRoleUser || strings.Contains(last.text, "@@") {
		t.Errorf("transcript ask = %q (role %v), want a short line", last.text, last.role)
	}
	if a.chat.commitSuggest == nil || len(a.chat.commitSuggest.files) != 1 {
		t.Fatalf("pending request = %+v", a.chat.commitSuggest)
	}
}

// TestHandleGitCommitDiff_EmptyDiffExplains pins the degenerate case:
// nothing to describe never spends a request.
func TestHandleGitCommitDiff_EmptyDiffExplains(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.handleGitCommitDiff(&gitCommitDiffEvent{when: time.Now(), diff: "   \n"})
	if len(fake.paramsFor("session/prompt")) != 0 {
		t.Error("empty diff still sent a turn")
	}
	if !strings.Contains(a.statusMsg, "Nothing to describe") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestChatCommitSuggestDone_OpensPrefilledPrompt pins the claim side:
// the agent's answer lands in the commit prompt, pre-filled and
// editable, and the pending request is consumed so a later answer can't
// reuse it.
func TestChatCommitSuggestDone_OpensPrefilledPrompt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	files := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}
	a.chat.commitSuggest = &commitSuggestReq{files: files, seq: a.chat.connSeq, mark: len(a.chat.msgs)}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "```\nAdd commit suggestions\n```"})

	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})

	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the commit prompt", a.modal)
	}
	if got := m.field.String(); got != "Add commit suggestions" {
		t.Errorf("prefill = %q", got)
	}
	if !strings.Contains(m.title, "a.go") {
		t.Errorf("title = %q, want the target named", m.title)
	}
	if a.chat.commitSuggest != nil {
		t.Error("request not consumed")
	}
}

// TestChatCommitSuggestDone_DropsStaleAndCancelled pins the staleness
// discipline: an answer from a torn-down connection is ignored, and a
// stopped turn opens nothing — a cancelled draft must never become a
// commit message.
func TestChatCommitSuggestDone_DropsStaleAndCancelled(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.commitSuggest = &commitSuggestReq{seq: a.chat.connSeq - 1}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "Add something"})
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})
	if a.modal != nil {
		t.Fatalf("stale generation opened %T", a.modal)
	}
	if a.chat.commitSuggest == nil {
		t.Error("another connection's request should survive untouched")
	}

	a.chat.commitSuggest = &commitSuggestReq{seq: a.chat.connSeq, mark: len(a.chat.msgs)}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "Add something"})
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq, stopReason: "cancelled"})
	if a.modal != nil {
		t.Fatalf("cancelled turn opened %T", a.modal)
	}
	if a.chat.commitSuggest != nil {
		t.Error("cancelled request not consumed")
	}
}

// TestChatDisconnect_ClearsCommitSuggest pins the teardown rule: a
// pending draft dies with its connection, so the next agent's first
// answer is never mistaken for it.
func TestChatDisconnect_ClearsCommitSuggest(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.commitSuggest = &commitSuggestReq{seq: a.chat.connSeq}
	a.chatDisconnect()
	if a.chat.commitSuggest != nil {
		t.Error("commitSuggest survived a disconnect")
	}
}

// TestHasSuggestableCommit pins the ≡ row's gate: something to describe
// (a panel selection or a non-empty index) AND an agent that can run.
func TestHasSuggestableCommit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	wireChat(a)
	if a.hasSuggestableCommit() {
		t.Error("enabled with nothing to describe")
	}
	a.gitHasStaged = true
	if !a.hasSuggestableCommit() {
		t.Error("disabled with a staged change and a live agent")
	}
	a.chat.dead = true
	if a.hasSuggestableCommit() {
		t.Error("enabled with a dead agent")
	}
}
