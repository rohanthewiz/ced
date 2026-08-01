// =============================================================================
// File: internal/app/skills_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the editor half of skill support (skills.go). newTestApp
// points skillsUserDirFn / skillsCedDirFn at throwaway directories, so
// nothing here can read the developer's real ~/.claude/skills — the
// project directory lives under the test's own root.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/skills"
)

// seedSkill writes dir/<name>/SKILL.md with the given frontmatter
// description and returns the SKILL.md path.
func seedSkill(t *testing.T, dir, name, description string) string {
	t.Helper()
	sd := filepath.Join(dir, name)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sd, skills.FileName)
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nDo the thing.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// seedProjectSkill puts a skill in the app's own <root>/.claude/skills.
func seedProjectSkill(t *testing.T, a *App, name, description string) string {
	t.Helper()
	return seedSkill(t, filepath.Join(a.rootDir, ".claude", "skills"), name, description)
}

// TestSkillDirs_PrecedenceOrder pins the scan order the shadowing rule
// depends on: ced's own directory first, then the personal one, with the
// project's last so a checked-in skill wins.
func TestSkillDirs_PrecedenceOrder(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	dirs := a.skillDirs()
	if len(dirs) != 3 {
		t.Fatalf("got %d dirs, want 3: %+v", len(dirs), dirs)
	}
	want := []skills.Source{skills.SourceCed, skills.SourceUser, skills.SourceProject}
	for i, src := range want {
		if dirs[i].Source != src {
			t.Errorf("dirs[%d].Source = %q, want %q", i, dirs[i].Source, src)
		}
	}
	if got := dirs[2].Path; got != filepath.Join(a.rootDir, ".claude", "skills") {
		t.Errorf("project dir = %q", got)
	}
}

// TestLoadSkills_FindsProjectAndUser checks the inventory merges both
// live directories and sorts by name — the picker's row order.
func TestLoadSkills_FindsProjectAndUser(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedSkill(t, skillsUserDirFn(), "zeta-skill", "personal one")
	seedProjectSkill(t, a, "alpha-skill", "project one")

	a.loadSkills()
	if a.skills.loadErr != "" {
		t.Fatalf("loadErr = %q", a.skills.loadErr)
	}
	if len(a.skills.list) != 2 {
		t.Fatalf("inventory = %+v, want 2 skills", a.skills.list)
	}
	if a.skills.list[0].Name != "alpha-skill" || a.skills.list[1].Name != "zeta-skill" {
		t.Fatalf("unsorted inventory: %q, %q", a.skills.list[0].Name, a.skills.list[1].Name)
	}
	if !a.hasSkills() {
		t.Error("hasSkills should be true with an inventory")
	}
}

// TestSkillsMenuLabel_CarriesCount pins the ≡ row's dynamic label: it
// answers "do I have any?" without opening the picker.
func TestSkillsMenuLabel_CarriesCount(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.loadSkills()
	if got := a.skillsMenuLabel(); got != "Use skill in chat… (none found)" {
		t.Errorf("empty label = %q", got)
	}
	seedProjectSkill(t, a, "one", "first")
	a.loadSkills()
	if got := a.skillsMenuLabel(); got != "Use skill in chat… (1)" {
		t.Errorf("populated label = %q", got)
	}
}

// TestMenuUseSkill_EmptyOpensSetupHelp is the "picker with no rows
// answers the wrong question" rule: with nothing installed the row must
// say where skills go, and name every directory that was searched.
func TestMenuUseSkill_EmptyOpensSetupHelp(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuUseSkill()
	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want the info modal", a.modal)
	}
	body := strings.Join(m.lines, "\n")
	for _, d := range a.skillDirs() {
		if !strings.Contains(body, d.Path) {
			t.Errorf("setup help omits %q:\n%s", d.Path, body)
		}
	}
	if !strings.Contains(body, "Nothing here is ever executed") {
		t.Errorf("setup help should state that skills are not run:\n%s", body)
	}
}

// TestMenuUseSkill_PicksAndAttaches drives the headline path: the picker
// lists the inventory (with scope and description for scanning), and
// running a row queues that skill for the next chat turn.
func TestMenuUseSkill_PicksAndAttaches(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := seedProjectSkill(t, a, "run-ced", "Drive the real binary")

	a.menuUseSkill()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want picker", a.modal)
	}
	if len(pm.items) != 1 {
		t.Fatalf("picker rows = %d, want 1", len(pm.items))
	}
	if label := pm.items[0].label; !strings.Contains(label, "run-ced") ||
		!strings.Contains(label, "project") || !strings.Contains(label, "Drive the real binary") {
		t.Errorf("picker row = %q, want name + scope + description", label)
	}

	pm.items[0].run(a)
	if len(a.chat.attach) != 1 {
		t.Fatalf("attachments = %+v, want the skill", a.chat.attach)
	}
	at := a.chat.attach[0]
	if at.path != path || at.skill != "run-ced" || at.skillDir != filepath.Dir(path) {
		t.Fatalf("attachment = %+v", at)
	}
	// A skill is labelled by NAME: personal skills live outside the
	// project, so a relative path would render as ../../ noise.
	if got := a.chatAttachLabel(at); got != "skill: run-ced" {
		t.Errorf("label = %q, want skill: run-ced", got)
	}
}

// TestMenuUseSkill_RescansBeforeOpening pins the "write a skill, use it
// immediately" loop: the picker re-reads the directories, so a skill
// created after startup is already there.
func TestMenuUseSkill_RescansBeforeOpening(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.loadSkills() // empty inventory at "startup"

	seedProjectSkill(t, a, "fresh", "written just now")
	a.menuUseSkill()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want picker — the rescan missed the new skill", a.modal)
	}
	if len(pm.items) != 1 || !strings.Contains(pm.items[0].label, "fresh") {
		t.Fatalf("picker rows = %+v", pm.items)
	}
}

// TestLeaderSkills_OpensThePicker pins Esc-S as the keyboard path to the
// skill picker. The menu row carries the same accelerator in its hint
// column, and a hint with no dispatch behind it is a menu that lies.
func TestLeaderSkills_OpensThePicker(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedProjectSkill(t, a, "run-ced", "Drive the real binary")

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'S'))

	pm, ok := a.modal.(*paletteModal)
	if !ok || pm.title != "Use skill in chat" {
		t.Fatalf("modal = %T (%+v), want the skill picker", a.modal, a.modal)
	}
	// The hint column and the dispatch table have to agree — a menu that
	// advertises a key nothing dispatches is a menu that lies. The row
	// draws its label through labelFor, so it's found that way rather
	// than by the static label field (which is empty for toggles).
	items, _, _ := a.menuLayout()
	found := ""
	for _, it := range items {
		if it.labelFor != nil && it.labelFor(a) == a.skillsMenuLabel() {
			found = it.shortcut
		}
	}
	if found != "esc S" {
		t.Errorf("menu hint = %q, want esc S", found)
	}
}

// TestMenuOpenSkill_OpensTheMarkdown pins the read-what-it-says path:
// reading (and editing) SKILL.md in ced is how a user learns what a
// skill will tell the agent to do.
func TestMenuOpenSkill_OpensTheMarkdown(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := seedProjectSkill(t, a, "inspect-me", "have a look")

	a.menuOpenSkill()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want picker", a.modal)
	}
	pm.items[0].run(a)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != path {
		t.Fatalf("active tab = %+v, want %s", tab, path)
	}
}

// TestMenuReloadSkills_ReportsCountAndErrors pins the diagnostic row:
// the count on success, and the load error when a directory can't be
// read — the one surface that says why a skill is missing.
func TestMenuReloadSkills_ReportsCountAndErrors(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedProjectSkill(t, a, "one", "first")
	a.menuReloadSkills()
	if !strings.Contains(a.statusMsg, "Reloaded skills (1 available)") {
		t.Errorf("flash = %q", a.statusMsg)
	}

	// An unreadable directory is reported, not swallowed.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(blocked, 0o000); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("running as a user that can read 0000 directories")
	}
	prev := skillsUserDirFn
	skillsUserDirFn = func() string { return blocked }
	t.Cleanup(func() { skillsUserDirFn = prev })

	a.menuReloadSkills()
	if !strings.Contains(a.statusMsg, "skills:") {
		t.Errorf("error flash = %q, want the load error", a.statusMsg)
	}
}

// TestChatSkillDirective_NamesSkillAndDirectory pins what turns attached
// markdown into an instruction: the directive says to follow it, and
// names the skill's directory so an agent can reach the supporting files
// ced deliberately doesn't ship. No skills attached → no directive.
func TestChatSkillDirective_NamesSkillAndDirectory(t *testing.T) {
	if got := chatSkillDirective([]chatAttach{{path: "/p/main.go"}}); got != "" {
		t.Errorf("plain attachments should produce no directive, got %q", got)
	}
	got := chatSkillDirective([]chatAttach{
		{path: "/p/main.go"},
		{path: "/s/run-ced/SKILL.md", skill: "run-ced", skillDir: "/s/run-ced"},
	})
	if !strings.Contains(got, "run-ced") || !strings.Contains(got, "/s/run-ced") {
		t.Errorf("directive = %q", got)
	}
	if !strings.Contains(got, "skill") {
		t.Errorf("directive should say these are skills: %q", got)
	}
}

// TestChatPromptBlocks_SkillDirectiveLeadsTheText pins the wire format:
// the skill's markdown rides as an attachment like any other context,
// and the directive leads the TEXT block — the one part of the payload
// the agent is guaranteed to read in both embedded and fenced shapes.
func TestChatPromptBlocks_SkillDirectiveLeadsTheText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.autoContext = false
	path := seedProjectSkill(t, a, "run-ced", "Drive the real binary")
	a.chat.attach = []chatAttach{{path: path, skill: "run-ced", skillDir: filepath.Dir(path)}}

	for _, embedded := range []bool{true, false} {
		a.chat.embeddedContext = embedded
		blocks, notes := a.chatPromptBlocks("how do I screenshot it?")
		text, _ := blocks[len(blocks)-1]["text"].(string)
		if !strings.HasPrefix(text, "Use the following skill(s)") {
			t.Errorf("embedded=%v: text block = %q", embedded, text)
		}
		if !strings.Contains(text, "how do I screenshot it?") {
			t.Errorf("embedded=%v: user's question was dropped: %q", embedded, text)
		}
		if embedded {
			if len(blocks) != 2 || blocks[0]["type"] != "resource" {
				t.Errorf("embedded blocks = %+v, want a resource + text", blocks)
			}
		} else if !strings.Contains(text, "Do the thing.") {
			t.Errorf("fenced fallback lost the skill body: %q", text)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], "skill: run-ced") {
			t.Errorf("transcript notes = %v", notes)
		}
	}
}

// TestSkillAttachment_IsPerTurn pins that a skill inherits the ordinary
// attachment lifecycle: consumed and cleared by the dispatch that sends
// it, never sticky. An ACP session keeps history server-side, so a
// sticky skill would re-send the whole markdown every turn.
func TestSkillAttachment_IsPerTurn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.autoContext = false
	path := seedProjectSkill(t, a, "run-ced", "Drive the real binary")
	a.attachSkill(skills.Skill{Name: "run-ced", Path: path, Dir: filepath.Dir(path)})
	if len(a.chat.attach) != 1 {
		t.Fatalf("attach = %+v", a.chat.attach)
	}

	a.chatSendPrompt("go")
	if len(a.chat.attach) != 0 {
		t.Fatalf("skill survived the turn: %+v", a.chat.attach)
	}
}

// TestAttachSkill_RefusesDuplicates keeps a double-pick from sending the
// same skill twice (and paying for it twice).
func TestAttachSkill_RefusesDuplicates(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := seedProjectSkill(t, a, "run-ced", "Drive the real binary")
	s := skills.Skill{Name: "run-ced", Path: path, Dir: filepath.Dir(path)}

	a.attachSkill(s)
	a.attachSkill(s)
	if len(a.chat.attach) != 1 {
		t.Fatalf("attachments = %+v, want one", a.chat.attach)
	}
	if !strings.Contains(a.statusMsg, "already attached") {
		t.Errorf("flash = %q, want the duplicate notice", a.statusMsg)
	}
}
