// =============================================================================
// File: internal/app/plugins_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/customactions"
	"github.com/rohanthewiz/ced/internal/plugins"
)

// installPlugin writes a manifest into the test App's plugin directory
// (newTestApp already points that at a temp dir) and reloads. Returns
// the plugin's directory so a test can assert on $PLUGIN_DIR.
func installPlugin(t *testing.T, a *App, name, body string) string {
	t.Helper()
	dir := filepath.Join(pluginsDirFn(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugins.ManifestName), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	a.loadPlugins()
	return dir
}

// TestLoadPlugins_StartupIsQuiet pins the silent-degradation contract at
// its most common point: a user with no plugins directory must see no
// error, no flash, and an empty inventory.
func TestLoadPlugins_StartupIsQuiet(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.loadPlugins()

	if len(a.plugins.list) != 0 || len(a.plugins.loadErrs) != 0 {
		t.Fatalf("empty install produced %d plugins / %d errors",
			len(a.plugins.list), len(a.plugins.loadErrs))
	}
	if a.statusMsg != "" {
		t.Errorf("startup flashed %q — nothing is wrong", a.statusMsg)
	}
	if a.pluginsOn() {
		t.Error("pluginsOn() with nothing installed should be false")
	}
}

// TestLoadPlugins_ErrorsAreHeldNotFlashed pins where a load failure
// goes. A startup flash scrolls away before the user has looked at the
// screen; the ≡ label carries the same news for as long as it's true.
func TestLoadPlugins_ErrorsAreHeldNotFlashed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "broken", `{"commands":[`)

	if len(a.plugins.loadErrs) != 1 {
		t.Fatalf("loadErrs = %v, want one", a.plugins.loadErrs)
	}
	if a.statusMsg != "" {
		t.Errorf("load error flashed %q — it belongs in the ≡ label", a.statusMsg)
	}
	if got := a.pluginsMenuLabel(); !strings.Contains(got, "load error") {
		t.Errorf("menu label = %q, want it to mention the load error", got)
	}
}

// TestPluginsMenuLabel_States walks the label through every state it has
// to describe — it doubles as the feature's status line, so each one has
// to read differently.
func TestPluginsMenuLabel_States(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if got := a.pluginsMenuLabel(); !strings.Contains(got, "none installed") {
		t.Errorf("empty label = %q", got)
	}
	installPlugin(t, a, "one", `{"commands":[{"label":"A","command":"true"}]}`)
	if got := a.pluginsMenuLabel(); !strings.Contains(got, "(1)") {
		t.Errorf("loaded label = %q, want a count", got)
	}
	installPlugin(t, a, "bad", `{"commands":[`)
	if got := a.pluginsMenuLabel(); !strings.Contains(got, "failed") {
		t.Errorf("mixed label = %q, want both counts", got)
	}
	a.plugins.enabled = false
	if got := a.pluginsMenuLabel(); !strings.Contains(got, "disabled") {
		t.Errorf("disabled label = %q", got)
	}
}

// TestPluginMenuItems_LabelsAndGating pins the two rules a plugin row
// follows: it names its plugin (two plugins may both ship "Format", and
// the palette shows labels with no group context at all), and a
// selection filter is gated on having a selection.
func TestPluginMenuItems_LabelsAndGating(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "fmt", `{"commands":[
		{"label":"Sort","input":"selection","output":"replace","command":"sort"},
		{"label":"Build","command":"make"}
	]}`)

	items := a.pluginMenuItems()
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].label != "fmt: Sort" {
		t.Errorf("label = %q, want the plugin name prefixed", items[0].label)
	}
	// No tab, so no selection: the selection filter must be disabled and
	// the plain command must not be.
	if items[0].enabled(a) {
		t.Error("selection filter should be disabled with nothing selected")
	}
	if !items[1].enabled(a) {
		t.Error("a plain command should be enabled — user shell isn't second-guessed")
	}
}

// TestPluginMenuItems_KillSwitch pins that "off" reaches the menu, not
// just the runner. A disabled plugin that still lists its rows is a
// disabled plugin nobody believes is disabled.
func TestPluginMenuItems_KillSwitch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "p", `{"commands":[{"label":"A","command":"true"}]}`)
	if len(a.pluginMenuItems()) == 0 {
		t.Fatal("enabled plugin should contribute rows")
	}
	a.plugins.enabled = false
	if got := a.pluginMenuItems(); got != nil {
		t.Errorf("disabled plugins still offered %d rows", len(got))
	}
}

// TestVisibleMenuGroups_SplicesPluginCommands pins where plugin commands
// land in the menu: their own group, after the built-ins and before
// Custom and Quit. Splicing here (rather than into builtinMenuGroups) is
// also what puts them in the command palette, which the second half
// checks.
func TestVisibleMenuGroups_SplicesPluginCommands(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "tools", `{"commands":[{"label":"Sort","command":"sort"}]}`)

	groups := a.visibleMenuGroups()
	var titles []string
	for _, g := range groups {
		titles = append(titles, g.title)
	}
	idx := -1
	for i, g := range groups {
		if g.title == "Plugin commands" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no Plugin commands group in %v", titles)
	}
	if groups[len(groups)-1].title != "Quit" {
		t.Fatalf("Quit must stay last, got %v", titles)
	}
	if idx != len(groups)-2 {
		t.Errorf("Plugin commands at %d, want just before Quit in %v", idx, titles)
	}

	// The palette flattens visibleMenuGroups, so the row is searchable
	// without the palette knowing plugins exist.
	found := false
	for _, it := range paletteActionItems(a) {
		if it.label == "tools: Sort" {
			found = true
		}
	}
	if !found {
		t.Error("plugin command missing from the command palette")
	}
}

// TestVisibleMenuGroups_PluginsAndCustomActionsCoexist pins the order
// when both user-owned inventories are present — the earlier version of
// this splice handled exactly one and returned early.
func TestVisibleMenuGroups_PluginsAndCustomActionsCoexist(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "tools", `{"commands":[{"label":"Sort","command":"sort"}]}`)
	a.customActions = []customactions.Action{{Label: "Open on Rager", Command: "echo r"}}

	groups := a.visibleMenuGroups()
	n := len(groups)
	if groups[n-1].title != "Quit" || groups[n-2].title != "Custom" || groups[n-3].title != "Plugin commands" {
		t.Errorf("tail order = %q/%q/%q, want Plugin commands/Custom/Quit",
			groups[n-3].title, groups[n-2].title, groups[n-1].title)
	}
}

// TestPluginLeader_FirstDeclaredWins pins collision resolution. Two
// plugins asking for the same key is inevitable; the rule has to be
// stable so installing an unrelated plugin can't silently steal a key
// the user has muscle memory for.
func TestPluginLeader_FirstDeclaredWins(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Load order is name-sorted, so "aaa" declares before "zzz".
	installPlugin(t, a, "aaa", `{"commands":[{"id":"mine","label":"Mine","leader":"s","command":"true"}]}`)
	installPlugin(t, a, "zzz", `{"commands":[{"id":"theirs","label":"Theirs","leader":"s","command":"true"}]}`)

	entries := a.pluginLeaderEntries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 — the key can only fire one command", len(entries))
	}
	if entries[0].plugin.Name != "aaa" {
		t.Errorf("winner = %q, want aaa (first declared)", entries[0].plugin.Name)
	}
	owners := a.pluginLeaderOwners()
	if owners['s'] != "aaa/mine" {
		t.Errorf("owner of 's' = %q, want aaa/mine", owners['s'])
	}
	// The loser has to be visible somewhere, or the user just has a key
	// that runs the wrong thing.
	report := strings.Join(a.pluginsInfoLines(), "\n")
	if !strings.Contains(report, "taken — unbound") {
		t.Errorf("info report should mark the losing binding:\n%s", report)
	}
}

// TestPluginLeaderHint_ListsEveryBinding keeps the Esc-x hint honest for
// the same reason the AI namespace's is pinned: a chord's flashed hint
// is its only keyboard discovery surface.
func TestPluginLeaderHint_ListsEveryBinding(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "p", `{"commands":[
		{"label":"Sort","leader":"s","command":"sort"},
		{"label":"Format","leader":"f","command":"fmt"},
		{"label":"No key","command":"true"}
	]}`)

	hint := pluginLeaderHint(a)
	for _, want := range []string{"s p: Sort", "f p: Format"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q missing %q", hint, want)
		}
	}
	if strings.Contains(hint, "No key") {
		t.Errorf("hint %q lists a command with no leader", hint)
	}
}

// TestPluginLeaderBindings_EmptyWhenDisabled pins that the kill switch
// reaches the keyboard too — Esc-x must not fire a plugin the user has
// turned off.
func TestPluginLeaderBindings_EmptyWhenDisabled(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "p", `{"commands":[{"label":"Sort","leader":"s","command":"sort"}]}`)
	if len(pluginLeaderBindings(a)) != 1 {
		t.Fatal("enabled plugin should bind its leader")
	}
	a.plugins.enabled = false
	if got := pluginLeaderBindings(a); got != nil {
		t.Errorf("disabled plugins still bound %d leader keys", len(got))
	}
}

// TestSetPluginsEnabled_OffClearsDecorations pins that "off" means the
// marks leave the screen, not just that no new ones arrive.
func TestSetPluginsEnabled_OffClearsDecorations(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		"/tmp/x.go": {"p/lint": {{Line: 1, Message: "boom"}}},
	}
	a.setPluginsEnabled(false)

	if a.plugins.enabled {
		t.Fatal("kill switch did not flip")
	}
	if len(a.plugins.decos) != 0 {
		t.Errorf("decorations survived the kill switch: %v", a.plugins.decos)
	}
	// And it persists — into the test's throwaway config, never the
	// developer's.
	data, err := os.ReadFile(pluginConfigPathFn())
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("config unreadable: %v", err)
	}
	if raw["plugins"] != "off" {
		t.Errorf("config plugins = %v, want \"off\"", raw["plugins"])
	}
}

// TestSetPluginsEnabled_OnReloads pins that re-enabling picks the
// inventory back up — turning the switch on with a stale empty list
// would look like the plugins had vanished.
func TestSetPluginsEnabled_OnReloads(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "p", `{"commands":[{"label":"A","command":"true"}]}`)
	a.setPluginsEnabled(false)
	a.setPluginsEnabled(true)

	if len(a.plugins.list) != 1 {
		t.Errorf("re-enable loaded %d plugins, want 1", len(a.plugins.list))
	}
}

// TestPluginEnvFor_CarriesPluginDir pins $PLUGIN_DIR and $PLUGIN_NAME.
// Without the directory a plugin that ships a script beside its manifest
// has to hardcode an absolute path, which breaks for everyone else who
// installs it.
func TestPluginEnvFor_CarriesPluginDir(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	dir := installPlugin(t, a, "scripted", `{"commands":[{"label":"A","command":"$PLUGIN_DIR/run.sh"}]}`)

	env := pluginEnvFor(a.plugins.list[0], a.captureActionVars())
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PLUGIN_DIR="+dir) {
		t.Errorf("env missing PLUGIN_DIR=%s:\n%s", dir, joined)
	}
	if !strings.Contains(joined, "PLUGIN_NAME=scripted") {
		t.Errorf("env missing PLUGIN_NAME:\n%s", joined)
	}
}

// TestPluginVars_TargetsTheGivenTab pins why pluginVars exists: a hook
// fires for a NAMED file, and by the time the command runs the active
// tab may be something else entirely. $FILE has to be the file the event
// was about.
func TestPluginVars_TargetsTheGivenTab(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	hookTarget := filepath.Join(root, "hooked.go")
	other := filepath.Join(root, "active.go")
	for _, p := range []string{hookTarget, other} {
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	a.openFile(hookTarget)
	target := a.activeTabPtr()
	a.openFile(other) // active tab is now the OTHER file

	v := a.pluginVars(target)
	if v.File != hookTarget {
		t.Errorf("FILE = %q, want the hook's file %q", v.File, hookTarget)
	}
	if v.Filename != "hooked.go" {
		t.Errorf("FILENAME = %q, want hooked.go", v.Filename)
	}
	// nil means "whatever is active", which is what commands want.
	if got := a.pluginVars(nil); got.File != other {
		t.Errorf("nil tab gave FILE = %q, want the active tab %q", got.File, other)
	}
}

// TestMenuReloadPlugins_PicksUpNewManifests pins the deliberate retry
// path — nothing watches the directory, so this row is how a manifest
// the user just wrote becomes live.
func TestMenuReloadPlugins_PicksUpNewManifests(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.loadPlugins()
	if len(a.plugins.list) != 0 {
		t.Fatal("expected an empty start")
	}
	dir := filepath.Join(pluginsDirFn(), "late")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"commands":[{"label":"A","command":"true"}]}`
	if err := os.WriteFile(filepath.Join(dir, plugins.ManifestName), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.menuReloadPlugins()

	if len(a.plugins.list) != 1 {
		t.Fatalf("reload found %d plugins, want 1", len(a.plugins.list))
	}
	if !strings.Contains(a.statusMsg, "1 plugin") {
		t.Errorf("reload flash = %q, want a count", a.statusMsg)
	}
}

// TestMenuPluginsInfo_EmptyOpensSetupHelp pins the empty-case call every
// inventory feature in this codebase makes: a user with nothing
// installed is asking "how do I make one?", not looking at a list.
func TestMenuPluginsInfo_EmptyOpensSetupHelp(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuPluginsInfo()

	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want the info modal", a.modal)
	}
	body := strings.Join(m.lines, "\n")
	if !strings.Contains(body, "plugin.json") {
		t.Errorf("setup help should show where the file goes:\n%s", body)
	}
}
