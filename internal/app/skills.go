// =============================================================================
// File: internal/app/skills.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// skills.go is the editor half of skill support: the inventory read from
// the three standard skills directories (internal/skills), the ≡ rows
// that browse it, and the one thing ced does with a chosen skill — hand
// its instructions to the chat agent for the next turn.
//
// House rules:
//
//   - **A skill is PUSHED, and only on purpose.** ced is not a model; it
//     cannot look at a description and decide a skill applies. So there
//     is no auto-selection and no per-turn skill index riding along on
//     every prompt — the user picks one, it attaches, and it goes out
//     with the next message. That also keeps the token cost visible: the
//     chip is on screen before Enter is pressed.
//   - **Attachment, not a new mechanism.** A skill rides the existing
//     context-attachment layer (copilot_chat_context.go): same chips,
//     same ✕, same per-turn consumption, same size cap, same
//     embedded-resource / fenced-block wire formats. The only additions
//     are the label and the directive line naming the skill's directory,
//     so an agent with filesystem access can read the supporting files
//     that sit beside the markdown.
//   - **Agent-agnostic**, like MCP: whatever backend the chat panel is
//     running gets the skill. This is not a Copilot feature and its rows
//     do not live in the Copilot group.
//   - **Nothing is executed.** A skill is markdown ced hands to an agent,
//     never a script ced runs — the config-file line in CLAUDE.md holds:
//     these directories add no extension point to the editor itself.
//   - **Silent degradation, per directory.** A missing skills directory
//     is the common case and says nothing; an unreadable file costs that
//     one skill. The load error is remembered, not flashed at startup,
//     and reported in full the moment the user goes looking.
//   - **Every list is the palette** (openPicker), and the picker rescans
//     first so a skill written moments ago in ced is already there.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/skills"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// skillSummaryMax caps the description shown in a picker row. Skill
// descriptions are written for a model to match against and run long;
// past this the row stops distinguishing anything and starts making
// every entry score alike in the fuzzy matcher.
const skillSummaryMax = 90

// skillsUserDirFn resolves the personal skills directory
// (~/.claude/skills — where the ecosystem already keeps them). A package
// var, like themeDirFn, so tests point the feature at a t.TempDir()
// instead of reading the developer's real skills.
var skillsUserDirFn = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "skills")
}

// skillsCedDirFn resolves ced's own skills directory, stubbed in tests
// for the same reason.
var skillsCedDirFn = userconfig.SkillsDir

// skillsState is the inventory, owned by App and mutated only on the
// main loop.
type skillsState struct {
	// list is every discovered skill in name order.
	list []skills.Skill

	// loadErr is the first read failure from the last scan, kept so the
	// ≡ surfaces can report it instead of showing an empty list that
	// looks like "nothing installed".
	loadErr string
}

// skillDirs is the scan list in increasing precedence: ced's own
// directory, the user's personal one, then the project's. A project
// skill therefore shadows a same-named personal skill, which is the
// point of checking one into a repo. Empty paths (no resolvable home or
// config dir) are dropped rather than scanned.
func (a *App) skillDirs() []skills.Dir {
	candidates := []skills.Dir{
		{Path: skillsCedDirFn(), Source: skills.SourceCed},
		{Path: skillsUserDirFn(), Source: skills.SourceUser},
		{Path: filepath.Join(a.rootDir, ".claude", "skills"), Source: skills.SourceProject},
	}
	out := make([]skills.Dir, 0, len(candidates))
	for _, d := range candidates {
		if d.Path != "" {
			out = append(out, d)
		}
	}
	return out
}

// loadSkills rebuilds the inventory. Errors are remembered rather than
// flashed: this runs at startup, where a message about a skill the user
// hasn't asked for yet is noise, and the ≡ rows surface it in full.
func (a *App) loadSkills() {
	list, errs := skills.Registry(a.skillDirs())
	a.skills.list = list
	a.skills.loadErr = ""
	if len(errs) > 0 {
		a.skills.loadErr = errs[0].Error()
	}
}

// hasSkills gates the rows that can only act on an existing skill.
func (a *App) hasSkills() bool { return len(a.skills.list) > 0 }

// skillsMenuLabel is the ≡ row's dynamic label — it carries the count so
// the menu answers "do I have any?" without opening the picker.
func (a *App) skillsMenuLabel() string {
	switch {
	case a.skills.loadErr != "" && len(a.skills.list) == 0:
		return "Use skill in chat… (load error)"
	case len(a.skills.list) == 0:
		return "Use skill in chat… (none found)"
	}
	return fmt.Sprintf("Use skill in chat… (%d)", len(a.skills.list))
}

// menuUseSkill opens the inventory as a picker; the pick attaches that
// skill to the next chat message. An empty inventory opens the setup
// help instead — a picker with no rows answers "what skills do I have?"
// with silence, when the real question is "where do they go?".
func (a *App) menuUseSkill() {
	a.closeMenu()
	a.loadSkills() // pick up skills written since startup
	if len(a.skills.list) == 0 {
		a.openInfo("Skills", a.skillsSetupHelp())
		return
	}
	items := make([]paletteItem, 0, len(a.skills.list))
	for _, s := range a.skills.list {
		s := s // capture per-iteration for the closure
		items = append(items, paletteItem{
			label: skillPickerLabel(s),
			run:   func(app *App) { app.attachSkill(s) },
		})
	}
	a.openPicker("Use skill in chat", items)
}

// menuOpenSkill opens a skill's SKILL.md in a tab. Reading (and editing)
// the markdown is how a user learns what a skill will actually tell the
// agent to do — the same "the editing loop is the customization UI"
// stance the theme feature takes.
func (a *App) menuOpenSkill() {
	a.closeMenu()
	a.loadSkills()
	if len(a.skills.list) == 0 {
		a.openInfo("Skills", a.skillsSetupHelp())
		return
	}
	items := make([]paletteItem, 0, len(a.skills.list))
	for _, s := range a.skills.list {
		s := s
		items = append(items, paletteItem{
			label: skillPickerLabel(s),
			run:   func(app *App) { app.openFile(s.Path) },
		})
	}
	a.openPicker("Open skill", items)
}

// menuReloadSkills rescans the directories and reports the count. The
// pickers rescan too, so this row exists for the diagnostic case: it is
// the one surface that says a file failed to load and why.
func (a *App) menuReloadSkills() {
	a.closeMenu()
	a.loadSkills()
	if a.skills.loadErr != "" {
		a.flash("skills: " + a.skills.loadErr)
		return
	}
	a.flash(fmt.Sprintf("Reloaded skills (%d available)", len(a.skills.list)))
}

// skillPickerLabel is one inventory row: the name, its scope, and enough
// of the description to choose by. The scope is a suffix rather than a
// column because the fuzzy scorer searches the whole label — "project"
// then works as a search term for free.
func skillPickerLabel(s skills.Skill) string {
	label := s.Name + " (" + string(s.Source) + ")"
	if d := s.Summary(skillSummaryMax); d != "" {
		label += " — " + d
	}
	return label
}

// skillsSetupHelp is the "you have no skills" body: the directories that
// were searched, the load error when there is one, and the smallest
// possible skill to copy.
func (a *App) skillsSetupHelp() []string {
	lines := []string{"A skill is a folder holding a " + skills.FileName + ". ced looks in:"}
	for _, d := range a.skillDirs() {
		lines = append(lines, fmt.Sprintf("  %-9s %s", string(d.Source), d.Path))
	}
	if a.skills.loadErr != "" {
		lines = append(lines, "", "Load error:", "  "+a.skills.loadErr)
	}
	return append(lines, "",
		"Example — "+filepath.Join(a.skillDirs()[len(a.skillDirs())-1].Path, "my-skill", skills.FileName)+":",
		"",
		"  ---",
		"  name: my-skill",
		"  description: What this skill is for.",
		"  ---",
		"  # My skill",
		"  …instructions for the agent…",
		"",
		"Picking a skill attaches its instructions to your next chat",
		"message. Nothing here is ever executed by the editor.")
}

// attachSkill queues a skill for the next chat turn. It goes through the
// ordinary attachment path, so the panel opens, the chip appears, and the
// per-turn consumption rule applies unchanged.
func (a *App) attachSkill(s skills.Skill) {
	a.chatAddAttachment(chatAttach{path: s.Path, skill: s.Name, skillDir: s.Dir})
}

// chatSkillDirective is the line that turns attached markdown into an
// instruction. Without it a SKILL.md reads as "here is a document";
// with it the agent knows the text is a procedure to follow, and knows
// where the skill's scripts and reference files live — ced ships only
// the markdown, deliberately (the directory can run to megabytes), so
// naming the directory is what keeps the rest reachable.
//
// Returns "" when no skill is attached, which is the common case.
func chatSkillDirective(pending []chatAttach) string {
	var b strings.Builder
	for _, at := range pending {
		if at.skill == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("Use the following skill(s) for this request — the instructions are attached below, and each skill's supporting files live in its directory:\n")
		}
		b.WriteString("  • " + at.skill)
		if at.skillDir != "" {
			b.WriteString(" — " + at.skillDir)
		}
		b.WriteString("\n")
	}
	return b.String()
}
