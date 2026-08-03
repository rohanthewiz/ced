// =============================================================================
// File: internal/app/plugins.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// plugins.go is the editor's half of the declarative plugin system: the
// inventory loaded from ~/.config/ced/plugins, the ≡ Plugins group, the
// dynamic command group spliced into the menu (and therefore into the
// palette), and the kill switch. The manifest schema, its validation,
// and the diagnostic line format live in internal/plugins; execution
// lives in plugincmd.go (commands) and plugindeco.go (hooks and
// decoration providers).
//
// The one thing to hold onto: a plugin is DATA the user wrote, not code
// ced loaded. Everything it can do is something the user could already
// do by typing a shell command — the plugin just says WHEN, and where
// the output goes. That's what keeps this on the themes-and-skills side
// of the no-plugin-system line rather than turning the editor into a
// host with an API surface to keep stable forever.
//
// House rules the rest of this file assumes:
//
//   - **Nothing runs at startup.** New reads the manifests; not one
//     command is executed until a file opens, a file is saved, or the
//     user picks a row. Same promise MCP makes about not spawning three
//     node processes while you weren't looking.
//   - **Degradation is per plugin.** One broken manifest names itself
//     in the ≡ row's label and costs that plugin only.
//   - **The kill switch is honoured everywhere, not just at load.**
//     `pluginsOn` gates the menu group, the leader namespace, the event
//     hooks, and the decoration source — turning plugins off has to
//     mean nothing of the user's runs, not "nothing new starts".

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/plugins"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// pluginsDirFn and pluginConfigPathFn are indirected through package
// vars so tests point them at temp directories. Without them a test
// that reloads plugins would read the developer's real inventory, and
// one that hit the toggle would rewrite their config.json — the same
// protection themeDirFn / themeConfigPathFn give the theme feature.
var (
	pluginsDirFn       = userconfig.PluginsDir
	pluginConfigPathFn = userconfig.DefaultPath
)

// pluginState is everything the editor knows about plugins. Mutated
// only on the main loop; the background command runs post events.
type pluginState struct {
	// enabled is the config kill switch ("plugins", default on).
	enabled bool

	// list is the loaded inventory, sorted by name.
	list []plugins.Plugin

	// loadErrs holds one message per manifest that failed to load, kept
	// so the ≡ row can say so and the info modal can show which. The
	// working plugins beside them are already loaded.
	loadErrs []string

	// decos maps an absolute file path to that file's decorations,
	// keyed by provider (see providerKey) so a re-run replaces only its
	// own marks. Read once per frame by pluginDecoSource.
	decos map[string]map[string][]plugins.Diagnostic

	// seq is the inventory generation, bumped on every reload and on
	// every flip of the kill switch. Every background run carries the
	// generation it started under and results from an older one are
	// dropped — otherwise a command in flight when the user hit Reload
	// would paint marks for a provider that no longer exists.
	seq int

	// editRev / editTimer drive the debounced "edit" event. editRev is
	// the summed EditRev of all open tabs at the last arm — the same
	// cheap signature auto-save uses — and editTimer is the pending
	// countdown. See pluginsAfterEvent in plugindeco.go.
	editRev   int
	editTimer *time.Timer
}

// pluginsOn reports whether plugins may run right now. Every surface
// asks this rather than testing `enabled` directly, so "off" can grow
// to mean more later (a project-trust verdict, say) in exactly one
// place.
func (a *App) pluginsOn() bool {
	return a.plugins.enabled && len(a.plugins.list) > 0
}

// loadPlugins reads the inventory and stores it, bumping the generation
// so anything already in flight is disowned. Load failures are held on
// the state rather than flashed: a flash at startup scrolls past before
// the user has looked at the screen, and the ≡ row's label carries the
// same news permanently. Startup stays quiet when nothing is wrong,
// which is the common case.
func (a *App) loadPlugins() {
	a.plugins.seq++
	a.plugins.list = nil
	a.plugins.loadErrs = nil
	// Drop every decoration: the providers that produced them may be
	// gone, renamed, or reconfigured, and marks nobody can explain are
	// worse than no marks.
	a.plugins.decos = nil

	list, errs := plugins.LoadDir(pluginsDirFn())
	a.plugins.list = list
	for _, err := range errs {
		a.plugins.loadErrs = append(a.plugins.loadErrs, err.Error())
	}
}

// pluginCommandCount totals the invocable commands across the
// inventory — what the ≡ label counts, since a plugin that only
// declares hooks has no rows to offer.
func (a *App) pluginCommandCount() int {
	n := 0
	for _, p := range a.plugins.list {
		n += len(p.Commands)
	}
	return n
}

// hasPluginCommands gates the menu rows that need something to run.
func (a *App) hasPluginCommands() bool {
	return a.pluginsOn() && a.pluginCommandCount() > 0
}

// pluginsMenuLabel is the ≡ Plugins group's headline row. It doubles as
// the feature's status line — how many loaded, whether any failed,
// whether the switch is off — because a user debugging a plugin looks
// at the menu before anywhere else.
func (a *App) pluginsMenuLabel() string {
	switch {
	case !a.plugins.enabled:
		return "Plugins… (disabled)"
	case len(a.plugins.loadErrs) > 0 && len(a.plugins.list) == 0:
		return "Plugins… (load error)"
	case len(a.plugins.loadErrs) > 0:
		return fmt.Sprintf("Plugins… (%d loaded, %d failed)",
			len(a.plugins.list), len(a.plugins.loadErrs))
	case len(a.plugins.list) == 0:
		return "Plugins… (none installed)"
	}
	return fmt.Sprintf("Plugins… (%d)", len(a.plugins.list))
}

// menuPluginsInfo opens the inventory as a readable report: what
// loaded, what each plugin contributes, which leader key it claimed,
// and what failed and why. With nothing installed it opens the setup
// help instead — the empty case is a question ("how do I make one?"),
// not an empty list, the same call MCP and Skills make.
func (a *App) menuPluginsInfo() {
	a.closeMenu()
	if len(a.plugins.list) == 0 && len(a.plugins.loadErrs) == 0 {
		a.openInfo("Plugins", a.pluginsSetupHelp())
		return
	}
	a.openInfo("Plugins", a.pluginsInfoLines())
}

// pluginsInfoLines renders the inventory report. Leader keys are shown
// as RESOLVED — the binding that actually fires, not the one the
// manifest asked for — so a collision between two plugins is visible
// here rather than as a key that mysteriously runs the wrong command.
func (a *App) pluginsInfoLines() []string {
	var out []string
	if !a.plugins.enabled {
		out = append(out, "Plugins are disabled — the ≡ toggle below re-enables them.", "")
	}
	claimed := a.pluginLeaderOwners()
	for _, p := range a.plugins.list {
		head := p.Name
		if p.Description != "" {
			head += " — " + p.Description
		}
		out = append(out, head)
		out = append(out, "  "+p.Dir)
		for _, c := range p.Commands {
			row := "  · " + c.Label
			if r, ok := c.LeaderRune(); ok {
				if owner, taken := claimed[r]; taken && owner == commandKey(p, c) {
					row += fmt.Sprintf("   [esc x %c]", r)
				} else {
					row += fmt.Sprintf("   [esc x %c taken — unbound]", r)
				}
			}
			out = append(out, row)
		}
		for _, h := range p.Hooks {
			out = append(out, "  · hook on "+eventList(h.On)+globNote(h.Glob))
		}
		for _, d := range p.Decorations {
			out = append(out, "  · marks "+d.ID+" on "+eventList(d.On)+globNote(d.Glob))
		}
		out = append(out, "")
	}
	if len(a.plugins.loadErrs) > 0 {
		out = append(out, "Failed to load:")
		for _, e := range a.plugins.loadErrs {
			out = append(out, "  "+e)
		}
	}
	return out
}

// eventList renders a hook's event set for the info report.
func eventList(on []plugins.Event) string {
	parts := make([]string, 0, len(on))
	for _, e := range on {
		parts = append(parts, string(e))
	}
	return strings.Join(parts, "/")
}

// globNote appends a glob to an info row, or nothing when the hook
// matches every file.
func globNote(glob string) string {
	if glob == "" {
		return ""
	}
	return " (" + glob + ")"
}

// pluginsSetupHelp is what a user with no plugins sees. It's a worked
// example rather than a pointer to documentation: the whole design bet
// is that a plugin is small enough to write from the example in front
// of you, and if that isn't true the feature has failed anyway.
func (a *App) pluginsSetupHelp() []string {
	dir := pluginsDirFn()
	if dir == "" {
		dir = "~/.config/ced/plugins"
	}
	return []string{
		"No plugins installed.",
		"",
		"A plugin is one JSON file describing shell commands ced runs",
		"for you. Nothing is compiled, loaded, or interpreted.",
		"",
		"Create " + filepath.Join(dir, "<name>", "plugin.json") + ":",
		"",
		`  {`,
		`    "name": "todo",`,
		`    "commands": [`,
		`      {"label": "Sort selection", "leader": "s",`,
		`       "input": "selection", "output": "replace",`,
		`       "command": "sort"}`,
		`    ],`,
		`    "hooks": [`,
		`      {"on": ["save"], "glob": "*.go",`,
		`       "command": "gofmt -w \"$FILE\"", "output": "reload"}`,
		`    ],`,
		`    "decorations": [`,
		`      {"id": "todo", "on": ["open", "save", "edit"],`,
		`       "command": "grep -n TODO \"$FILE\""}`,
		`    ]`,
		`  }`,
		"",
		"commands     run from ≡ → Plugins, the palette, or esc x <leader>.",
		"             input:  none | selection | file   (what goes to stdin)",
		"             output: none | replace | insert | info | flash",
		"hooks        run on open / save / edit (an idle pause after typing).",
		"             output: none | flash | info | reload",
		"decorations  stdout is read as  path:line:col: severity: message",
		"             and painted as gutter marks — `grep -n`, `go vet`,",
		"             `shellcheck -f gcc` and `eslint -f unix` all fit.",
		"",
		"Every command gets $FILE, $FILENAME, $PROJECT_ROOT, $ACTIVE_FOLDER,",
		"$PLUGIN_DIR and $PLUGIN_NAME, and runs through sh -c.",
		"",
		"Then: ≡ → Plugins → Reload plugins.",
	}
}

// menuReloadPlugins re-reads the inventory. The deliberate retry path
// after editing a manifest — and, like every other integration's
// reconnect row, the only one: nothing watches the directory, because a
// plugin that reloaded itself mid-edit would run half-written commands.
func (a *App) menuReloadPlugins() {
	a.closeMenu()
	a.loadPlugins()
	switch {
	case len(a.plugins.loadErrs) > 0:
		a.openInfo("Plugins", a.pluginsInfoLines())
	case len(a.plugins.list) == 0:
		a.flash("No plugins found in " + pluginsDirFn())
	default:
		a.flash(fmt.Sprintf("Loaded %d plugin(s), %d command(s)",
			len(a.plugins.list), a.pluginCommandCount()))
	}
}

// pluginsToggleLabel names the state the row switches TO, like every
// other ≡ toggle.
func (a *App) pluginsToggleLabel() string {
	if a.plugins.enabled {
		return "Disable plugins"
	}
	return "Enable plugins"
}

// menuTogglePlugins flips the kill switch.
func (a *App) menuTogglePlugins() {
	a.closeMenu()
	a.setPluginsEnabled(!a.plugins.enabled)
}

// setPluginsEnabled is the single write path for the kill switch, so
// the ≡ row and any future surface can't disagree about what flipping
// it does. Turning plugins OFF drops every painted decoration and bumps
// the generation: "off" has to mean the marks leave the screen too,
// not just that no new ones arrive.
func (a *App) setPluginsEnabled(on bool) {
	a.plugins.enabled = on
	a.plugins.seq++
	if !on {
		a.plugins.decos = nil
		a.stopPluginEditTimer()
		a.flash("Plugins disabled")
	} else {
		a.loadPlugins()
		a.flash(fmt.Sprintf("Plugins enabled — %d loaded", len(a.plugins.list)))
	}
	if err := userconfig.SavePlugins(pluginConfigPathFn(), on); err != nil {
		a.flash("config: " + err.Error())
	}
}

// pluginMenuItems flattens every plugin command into menu rows. Labels
// are prefixed with the plugin name so two plugins can both ship a
// "Format" row and the menu still reads unambiguously — and so the
// palette, which shows labels with no group context at all, stays
// searchable by plugin.
//
// A command whose input needs a selection is gated on having one; the
// rest are always enabled, on the same reasoning custom actions use —
// user-written shell is not something to second-guess from the outside.
func (a *App) pluginMenuItems() []menuItemDef {
	if !a.pluginsOn() {
		return nil
	}
	var out []menuItemDef
	for pi := range a.plugins.list {
		p := a.plugins.list[pi]
		for ci := range p.Commands {
			c := p.Commands[ci]
			enabled := alwaysTrue
			if c.Input == plugins.InputSelection {
				enabled = (*App).hasSelection
			}
			out = append(out, menuItemDef{
				label:    p.Name + ": " + c.Label,
				shortcut: pluginShortcutHint(c),
				action:   func(app *App) { app.runPluginCommand(p, c) },
				enabled:  enabled,
			})
		}
	}
	return out
}

// pluginShortcutHint renders the ≡ accelerator column for a command
// that claimed a leader key. Display only — dispatch lives in the Esc-x
// namespace (leader.go), and the two must be updated together.
func pluginShortcutHint(c plugins.Command) string {
	r, ok := c.LeaderRune()
	if !ok {
		return ""
	}
	return "esc x " + string(r)
}

// -----------------------------------------------------------------------------
// The Esc-x leader namespace
// -----------------------------------------------------------------------------

// pluginLeaderEntry is one resolved leader binding: the key, what it
// runs, and the label the hint line shows for it.
type pluginLeaderEntry struct {
	key    rune
	label  string
	plugin plugins.Plugin
	cmd    plugins.Command
}

// pluginLeaderEntries resolves the inventory's requested leader keys
// into the ones that actually fire. FIRST DECLARED WINS, walking
// plugins in their (name-sorted) load order — a stable rule, so
// installing an unrelated plugin can't silently steal a key you've
// built muscle memory for, and the loser is named in the ≡ Plugins
// report rather than failing silently.
//
// Deliberately NOT gated on the kill switch: the info report explains
// bindings whether or not they're live right now.
func (a *App) pluginLeaderEntries() []pluginLeaderEntry {
	var out []pluginLeaderEntry
	seen := make(map[rune]struct{})
	for _, p := range a.plugins.list {
		for _, c := range p.Commands {
			r, ok := c.LeaderRune()
			if !ok {
				continue
			}
			if _, taken := seen[r]; taken {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, pluginLeaderEntry{
				key: r, label: p.Name + ": " + c.Label, plugin: p, cmd: c,
			})
		}
	}
	return out
}

// pluginLeaderOwners maps each claimed key to the command that won it,
// so the info report can mark the losers.
func (a *App) pluginLeaderOwners() map[rune]string {
	entries := a.pluginLeaderEntries()
	owners := make(map[rune]string, len(entries))
	for _, e := range entries {
		owners[e.key] = commandKey(e.plugin, e.cmd)
	}
	return owners
}

// pluginLeaderBindings is the Esc-x sub-table, resolved fresh on every
// arm. Dynamic because the inventory is: a leader table baked at
// startup would go stale the moment the user hit Reload plugins.
func pluginLeaderBindings(a *App) []leaderBinding {
	if !a.pluginsOn() {
		return nil
	}
	entries := a.pluginLeaderEntries()
	out := make([]leaderBinding, 0, len(entries))
	for _, e := range entries {
		out = append(out, leaderBinding{
			key:    e.key,
			action: func(app *App) { app.runPluginCommand(e.plugin, e.cmd) },
		})
	}
	return out
}

// pluginLeaderHint is the one-line menu flashed when Esc-x arms — the
// namespace's only keyboard discovery surface, same job the AI
// namespace's static hint does. Built from the same resolved entries as
// the bindings, so it can never advertise a key that doesn't fire.
func pluginLeaderHint(a *App) string {
	entries := a.pluginLeaderEntries()
	if len(entries) == 0 {
		return "Plugins — no leader keys bound (≡ → Plugins)"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%c %s", e.key, e.label))
	}
	return "Plugins  " + strings.Join(parts, " · ")
}

// commandKey identifies one command across the whole inventory. Used to
// resolve leader collisions and to name a command in diagnostics; two
// plugins may each have a command called "format", so the plugin name
// has to be part of the identity.
func commandKey(p plugins.Plugin, c plugins.Command) string {
	return p.Name + "/" + c.ID
}

// providerKey does the same for a decoration provider, and is the key
// its marks are filed under so a re-run replaces its own and nobody
// else's.
func providerKey(p plugins.Plugin, d plugins.Provider) string {
	return p.Name + "/" + d.ID
}

// pluginVars snapshots the editor-state variables for a plugin run,
// aimed at a SPECIFIC tab rather than the active one. Hooks and
// decoration providers fire for a named file — a save, an open — and
// the active tab may well be a different file by the time the command
// runs. Passing nil falls back to the active tab, which is what
// commands want.
func (a *App) pluginVars(tab *editor.Tab) actionVars {
	v := a.captureActionVars()
	if tab == nil || tab.Path == "" {
		return v
	}
	path := tab.Path
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	v.File = path
	v.Filename = filepath.Base(path)
	v.CurrentFile = path
	v.CurrentFileRel = relOrEmpty(v.ProjectRoot, path)
	return v
}

// pluginEnvFor builds the environment for one plugin run: the standard
// editor-state variables plus the two a plugin needs to find its own
// files. $PLUGIN_DIR is what makes "ship a script beside the manifest"
// work — without it a plugin author has to hardcode an absolute path
// that breaks the moment anyone else installs it.
func pluginEnvFor(p plugins.Plugin, v actionVars) []string {
	return append(v.envSlice(),
		"PLUGIN_DIR="+p.Dir,
		"PLUGIN_NAME="+p.Name,
	)
}
