// =============================================================================
// File: internal/app/gitcommitmsg_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
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

// -----------------------------------------------------------------------------
// The attribution trailer (Phase 4.5)
// -----------------------------------------------------------------------------

// TestCommitTrailerLine_NamesTheActiveAgent pins what the trailer says:
// the agent's DISPLAY name — the same string the header, the ≡ picker
// and the transcript use — beside its non-routing address, and nothing
// at all for a backend with no identity to credit.
func TestCommitTrailerLine_NamesTheActiveAgent(t *testing.T) {
	for _, def := range chatAgents() {
		got := commitTrailerLine(def)
		if !strings.HasPrefix(got, "Co-Authored-By: "+def.name+" <") || !strings.HasSuffix(got, ">") {
			t.Errorf("commitTrailerLine(%s) = %q", def.id, got)
		}
		// A resolving account address would turn the record into a
		// mention of somebody ced never verified was involved.
		if !strings.Contains(got, "noreply@") {
			t.Errorf("commitTrailerLine(%s) = %q, want a non-routing address", def.id, got)
		}
	}
	if got := commitTrailerLine(chatAgentDef{name: "Nameless"}); got != "" {
		t.Errorf("an agent with no address should get no trailer, got %q", got)
	}
}

// TestCommitMsgWithTrailer pins the assembly: the trailer is its own
// paragraph (git reads a trailer block only after a blank line), adding
// it twice is a no-op, and an empty trailer leaves the message alone.
func TestCommitMsgWithTrailer(t *testing.T) {
	line := "Co-Authored-By: Copilot <noreply@github.com>"
	got := commitMsgWithTrailer("Add the thing", line)
	if got != "Add the thing\n\n"+line {
		t.Fatalf("assembled = %q", got)
	}
	if again := commitMsgWithTrailer(got, line); again != got {
		t.Errorf("second application changed the message: %q", again)
	}
	if got := commitMsgWithTrailer("Add the thing", ""); got != "Add the thing" {
		t.Errorf("no trailer should mean no change, got %q", got)
	}
}

// TestCommitPromptTrailerChip_OnlyOnADraft pins where the chip may
// appear: on a message the AGENT wrote, never on one the user is about
// to type — there is nothing to attribute on hand-written work, and a
// chip reading "trailer: on" over it would be a lie.
func TestCommitPromptTrailerChip_OnlyOnADraft(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	wireChat(a)
	files := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}

	a.openCommitPrompt(files, "")
	pm := a.modal.(*promptModal)
	if len(pm.extras) != 1 {
		t.Fatalf("a hand-typed prompt should carry the ✦ button alone, got %d extras", len(pm.extras))
	}

	a.closeModal()
	a.openCommitPromptDraft(files, "Add the thing", true)
	pm = a.modal.(*promptModal)
	if len(pm.extras) != 2 {
		t.Fatalf("a drafted prompt should carry ✦ and the chip, got %d extras", len(pm.extras))
	}
	chip := pm.extras[1]
	if got := chip.label(a); got != "[trailer: on]" {
		t.Fatalf("chip label = %q", got)
	}
	chip.run(a)
	if got := chip.label(a); got != "[trailer: off]" {
		t.Fatalf("after the click chip label = %q", got)
	}
	if chip.width < runeLen("[trailer: off]") {
		t.Errorf("chip width %d must cover its widest label", chip.width)
	}
}

// TestCommitPromptTrailerChip_Layout pins the button row with both
// extras on it: the ✦ button keeps the right edge (so it doesn't move
// when the chip joins it), the chip sits left of it, neither overlaps
// OK, and the modal is drawn wide enough to hold the lot.
func TestCommitPromptTrailerChip_Layout(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	wireChat(a)
	files := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}

	a.openCommitPrompt(files, "")
	pm := a.modal.(*promptModal)
	aiAlone := pm.extraRects(a)[0]
	plainX, _, plainW, _ := pm.rect(a)
	// Measured from the modal's RIGHT edge: the box is centered, so it
	// is the edge, not the screen column, that the button holds onto.
	plainInset := plainX + plainW - (aiAlone.x + aiAlone.w)

	a.closeModal()
	a.openCommitPromptDraft(files, "Add the thing", true)
	pm = a.modal.(*promptModal)
	rects := pm.extraRects(a)
	if len(rects) != 2 {
		t.Fatalf("both extras should fit a 120-column screen, got %d", len(rects))
	}
	mx, _, mw, _ := pm.rect(a)
	if mw <= plainW {
		t.Errorf("modal width %d did not grow past the plain %d", mw, plainW)
	}
	if got := mx + mw - (rects[0].x + rects[0].w); got != plainInset {
		t.Errorf("✦ sits %d cells in from the right edge, want %d (it holds that edge)", got, plainInset)
	}
	if rects[1].x+rects[1].w >= rects[0].x {
		t.Errorf("chip %v overlaps the ✦ button %v", rects[1], rects[0])
	}
	_, ok := pm.buttons(a)
	if rects[1].x < ok.x+ok.w+1 {
		t.Errorf("chip %v crowds OK %v", rects[1], ok)
	}

	// And it survives a real draw: both labels land on the button row,
	// with OK still legible beside them.
	pm.draw(a)
	a.screen.Show()
	row := strings.Split(screenText(a), "\n")[rects[0].y]
	for _, want := range []string{"[  OK  ]", "[trailer: on]", "[ ✦ AI ]"} {
		if !strings.Contains(row, want) {
			t.Errorf("button row %q missing %q", row, want)
		}
	}
}

// TestCommitPromptTrailerChip_NarrowScreenDropsTheChip pins the
// degradation: on a terminal too narrow to widen into, the modal keeps
// its shipped width and sheds extras from the LEFT — the ✦ button, which
// holds the right edge, survives; the chip drops rather than being
// painted truncated or over OK. The preference still applies and is
// still visible in the ≡ row, so what a 56-column terminal loses is the
// per-commit override, not the setting.
func TestCommitPromptTrailerChip_NarrowScreenDropsTheChip(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	wireChat(a)
	a.width = 56 // wide enough for the prompt, too narrow for the row
	a.openCommitPromptDraft([]gitPanelFile{{Path: "/p/a.go", Rel: "a.go"}}, "Add the thing", true)
	pm := a.modal.(*promptModal)
	if _, _, mw, _ := pm.rect(a); mw != promptModalWidth {
		t.Errorf("modal width = %d, want the shipped %d on a narrow screen", mw, promptModalWidth)
	}
	rects := pm.extraRects(a)
	if len(rects) != 1 {
		t.Fatalf("extras = %v, want just the ✦ button", rects)
	}
	if _, ok := pm.buttons(a); rects[0].x < ok.x+ok.w+1 {
		t.Errorf("surviving button %v crowds OK %v", rects[0], ok)
	}
}

// TestCommitPromptTrailer_CommitsWithAndWithoutIt pins the payload end
// to end against a real repository: a drafted message commits with the
// trailer, and the same message commits without it once the chip is
// clicked. The click goes through handleMouse, so the hit-test is
// exercised too — a chip that can't be clicked is not a control.
func TestCommitPromptTrailer_CommitsWithAndWithoutIt(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	a := newTestApp(t, repo)
	wireChat(a)
	a.refreshGitStatus()

	writeFileT(t, file, "one\nWITH\n")
	target := []gitPanelFile{{Path: file, Rel: "f.txt", Code: " M"}}
	a.openCommitPromptDraft(target, "Add the first thing", true)
	a.modal.(*promptModal).submit(a)
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(gitOut(t, repo, "log", "-1", "--pretty=%s"), "Add the first thing")
	})
	want := commitTrailerLine(a.chatAgent())
	if body := gitOut(t, repo, "log", "-1", "--pretty=%B"); !strings.Contains(body, want) {
		t.Fatalf("commit body = %q, want the trailer %q", body, want)
	}

	writeFileT(t, file, "one\nWITHOUT\n")
	a.openCommitPromptDraft(target, "Add the second thing", true)
	pm := a.modal.(*promptModal)
	chip := pm.extraRects(a)[1]
	pm.handleMouse(a, chip.x+1, chip.y, tcell.Button1)
	if got := pm.extras[1].label(a); got != "[trailer: off]" {
		t.Fatalf("the click missed the chip: label %q", got)
	}
	pm.submit(a)
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(gitOut(t, repo, "log", "-1", "--pretty=%s"), "Add the second thing")
	})
	if body := gitOut(t, repo, "log", "-1", "--pretty=%B"); strings.Contains(body, "Co-Authored-By") {
		t.Fatalf("chip off, but the commit still carries a trailer: %q", body)
	}
}

// TestCommitTrailer_SurvivesTheRedraft pins the round trip: the chip's
// answer travels with the request, so asking the agent for another
// draft does not quietly re-arm the trailer the user just switched off.
func TestCommitTrailer_SurvivesTheRedraft(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	wireChat(a)
	files := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}

	// The ✦ button on a prompt whose chip is off must ask with it off.
	a.handleGitCommitDiff(&gitCommitDiffEvent{
		when: time.Now(), files: files, diff: "@@ -1 +1 @@\n+new", trailer: false,
	})
	if req := a.chat.commitSuggest; req == nil || req.trailer {
		t.Fatalf("request = %+v, want the trailer decision carried through", req)
	}
	// And the prompt the answer reopens starts from that decision.
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "Add the thing"})
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the commit prompt", a.modal)
	}
	if got := pm.extras[1].label(a); got != "[trailer: off]" {
		t.Fatalf("reopened chip = %q, want the user's earlier answer", got)
	}
}

// TestMenuToggleCommitTrailer_PersistsAndSeedsThePrompt pins the ≡ row:
// it flips the preference, writes it, and the next drafted prompt opens
// from the new default — the chip is the override, this is the setting.
func TestMenuToggleCommitTrailer_PersistsAndSeedsThePrompt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	wireChat(a)
	if got := a.commitTrailerToggleLabel(); got != "AI commit trailer: on" {
		t.Fatalf("label = %q, want the shipped default", got)
	}

	a.menuToggleCommitTrailer()
	if a.commitTrailer {
		t.Fatal("the ≡ row did not flip the preference")
	}
	if got := a.commitTrailerToggleLabel(); got != "AI commit trailer: off" {
		t.Errorf("label = %q after the toggle", got)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil || !strings.Contains(string(data), `"commitmsgtrailer": "off"`) {
		t.Fatalf("preference not persisted: %v %s", err, data)
	}

	// And the ≡ "Suggest commit message" row asks with it: the entry
	// points read the preference, the prompt's chip is the only thing
	// that departs from it. Needs a real change to describe, or the
	// request is never sent at all.
	if !gitAvailable() {
		t.Skip("git not on PATH — the entry-point half needs a real diff")
	}
	repo, file := panelRepo(t)
	writeFileT(t, file, "one\nCHANGED\n")
	gitRun(t, repo, "add", "f.txt") // the ≡ row describes the index
	b := newTestApp(t, repo)
	wireChat(b)
	b.refreshGitStatus()
	b.commitTrailer = false // as applyConfig would have loaded it
	b.menuGitSuggestCommit()
	pumpAppEvents(t, b, func() bool { return b.chat.commitSuggest != nil })
	if req := b.chat.commitSuggest; req == nil || req.trailer {
		t.Fatalf("request = %+v, want the persisted off", req)
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
