// =============================================================================
// File: internal/plugins/plugins.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// plugins loads declarative plugin manifests from
// ~/.config/ced/plugins/<name>/plugin.json and hands the editor a
// validated inventory. A plugin is DATA, not code: it can only declare
// shell commands the user asked for, bound to menu rows, leader keys,
// editor events, and a decoration overlay. ced never loads a shared
// object, never embeds an interpreter, and never gains an extension
// POINT the editor itself has to keep stable — which is what keeps this
// on the same side of the line as themes (a fixed key list) and skills
// (markdown ced hands off but never runs).
//
// It is deliberately the same instrument as actions.json, one octave
// up. actions.json answered "give me a menu row that shells out";
// this answers the three things that row could never do:
//
//	commands     stdin/stdout filters — feed the selection or the whole
//	             buffer to a command and put its output back, plus a
//	             leader key and a palette entry
//	hooks        run something on open / save / an idle pause after an
//	             edit, which is what makes a plugin ambient rather than
//	             something you go and click
//	decorations  paint the command's output over the code as gutter
//	             marks and underlines (see diag.go for the line format)
//
// Prompt collection is imported wholesale from customactions rather
// than re-specified: the form modal, the env-var contract, and the
// validation rules are already right, and a user who has written one
// actions.json entry should not have to learn a second dialect.
//
// Schema (every section optional):
//
//	{
//	  "name": "prettier",
//	  "description": "Format JS/TS through prettier",
//	  "commands": [
//	    {"id": "sel", "label": "Prettier: selection", "leader": "p",
//	     "input": "selection", "output": "replace",
//	     "command": "prettier --parser typescript"}
//	  ],
//	  "hooks": [
//	    {"on": ["save"], "glob": "*.ts",
//	     "command": "eslint --fix \"$FILE\"", "output": "reload"}
//	  ],
//	  "decorations": [
//	    {"id": "todo", "on": ["open", "save"], "glob": "*",
//	     "command": "grep -n TODO \"$FILE\""}
//	  ]
//	}
//
// Every command runs through `sh -c` with the editor-state env vars
// custom actions already export ($FILE, $FILENAME, $PROJECT_ROOT, …)
// plus $PLUGIN_DIR and $PLUGIN_NAME, so a plugin can ship a script
// beside its manifest and call it by path.
//
// Degradation is PER PLUGIN, the theme registry's rule: one unparseable
// manifest costs that plugin and names itself in the error list, never
// its neighbours and never startup.

package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rohanthewiz/ced/internal/customactions"
)

// ManifestName is the file every plugin directory must contain. Fixed
// rather than globbed so a directory holding scripts, a README, and a
// manifest has exactly one thing ced will read.
const ManifestName = "plugin.json"

// InputMode says what a command receives on stdin. The mode is also
// what defines the range OutputReplace writes back over, which is why
// "replace with no input" is a validation error rather than a guess.
type InputMode string

const (
	// InputNone runs the command with no stdin. The default: most
	// commands take the file path from $FILE and do their own IO.
	InputNone InputMode = "none"
	// InputSelection pipes the current selection in. The command is
	// unavailable (dimmed) while nothing is selected.
	InputSelection InputMode = "selection"
	// InputFile pipes the whole buffer in — the UNSAVED text, not the
	// file on disk. A formatter bound to a command should see what the
	// user is looking at, the same rule chat attachments follow.
	InputFile InputMode = "file"
)

// OutputMode says what the editor does with stdout when the command
// exits 0. Failures ignore this entirely and open the info modal with
// the command's stderr — the same treatment custom actions get.
type OutputMode string

const (
	// OutputNone discards stdout and flashes that the command ran.
	OutputNone OutputMode = "none"
	// OutputReplace overwrites the input range with stdout: the
	// selection for InputSelection, the whole buffer for InputFile.
	// One undo step either way.
	OutputReplace OutputMode = "replace"
	// OutputInsert drops stdout in at the cursor.
	OutputInsert OutputMode = "insert"
	// OutputInfo shows stdout in the info modal — the mode for
	// commands that answer a question rather than change the file.
	OutputInfo OutputMode = "info"
	// OutputFlash puts the first line of stdout in the status bar.
	OutputFlash OutputMode = "flash"
	// OutputReload re-reads the file from disk. For hooks that rewrite
	// $FILE in place (eslint --fix, a codegen pass) — the buffer would
	// otherwise sit there stale until the refresh tick noticed.
	OutputReload OutputMode = "reload"
)

// Event names a plugin can hang a hook or a decoration provider on.
type Event string

const (
	// EventOpen fires once, when a file opens in a tab.
	EventOpen Event = "open"
	// EventSave fires after a successful write to disk (both explicit
	// saves and auto-save).
	EventSave Event = "save"
	// EventEdit fires after an idle pause following a buffer change —
	// debounced by the app, never per keystroke. This is what lets a
	// TODO scanner or a linter keep up with typing without spawning a
	// process per rune.
	EventEdit Event = "edit"
)

// Plugin is one loaded manifest plus where it came from.
type Plugin struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Commands    []Command  `json:"commands,omitempty"`
	Hooks       []Hook     `json:"hooks,omitempty"`
	Decorations []Provider `json:"decorations,omitempty"`

	// Dir is the absolute directory the manifest was read from,
	// exported to every command as $PLUGIN_DIR. Not a JSON field —
	// it's discovered, not declared, and a manifest that could name
	// its own directory could name somebody else's.
	Dir string `json:"-"`
}

// Command is one invocable plugin action: a menu row, a palette entry,
// and optionally a leader key. Label is what the user reads; ID keys
// the command for tests and diagnostics and defaults to the label.
type Command struct {
	ID      string     `json:"id,omitempty"`
	Label   string     `json:"label"`
	Leader  string     `json:"leader,omitempty"`
	Input   InputMode  `json:"input,omitempty"`
	Output  OutputMode `json:"output,omitempty"`
	Command string     `json:"command"`

	// Prompts opens the same form modal custom actions use before the
	// command runs, exporting each answer as an env var. Reused rather
	// than re-specified so there's one dialect to learn.
	Prompts []customactions.Prompt `json:"prompts,omitempty"`
}

// LeaderRune returns the command's leader key as a rune, and whether
// one is bound at all. Validation has already guaranteed a bound leader
// is exactly one rune, so this can't disagree with what loaded.
func (c Command) LeaderRune() (rune, bool) {
	if c.Leader == "" {
		return 0, false
	}
	rs := []rune(c.Leader)
	return rs[0], true
}

// Hook runs a command on an editor event, with no UI surface of its
// own. Glob matches against the file's BASE NAME (filepath.Match), so
// "*.go" reads the way a user expects; empty matches every file.
type Hook struct {
	On      []Event    `json:"on"`
	Glob    string     `json:"glob,omitempty"`
	Command string     `json:"command"`
	Output  OutputMode `json:"output,omitempty"`
}

// Provider is a decoration source: a command whose stdout is parsed as
// diagnostics (see diag.go) and painted over the file as gutter marks
// and underlines. ID keys the result set, so re-running a provider
// REPLACES its own marks and never touches another's.
type Provider struct {
	ID      string  `json:"id"`
	On      []Event `json:"on"`
	Glob    string  `json:"glob,omitempty"`
	Command string  `json:"command"`
}

// Matches reports whether a hook should fire for event ev on a file
// named base. Shared by Hook and Provider through the two thin methods
// below so the two can never drift on what "*.go on save" means.
func matches(on []Event, glob string, ev Event, base string) bool {
	found := false
	for _, e := range on {
		if e == ev {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if glob == "" {
		return true
	}
	ok, err := filepath.Match(glob, base)
	// A malformed pattern can't reach here — validation rejected it —
	// so an error is impossible in practice; treat it as "no match"
	// rather than silently firing on every file.
	return err == nil && ok
}

// Matches reports whether this hook fires for ev on a file named base.
func (h Hook) Matches(ev Event, base string) bool { return matches(h.On, h.Glob, ev, base) }

// Matches reports whether this provider re-runs for ev on a file named base.
func (p Provider) Matches(ev Event, base string) bool { return matches(p.On, p.Glob, ev, base) }

// manifest mirrors the on-disk JSON. Separate from Plugin so Dir can be
// stamped by the loader rather than accepted from the file.
type manifest struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Commands    []Command  `json:"commands"`
	Hooks       []Hook     `json:"hooks"`
	Decorations []Provider `json:"decorations"`
}

// LoadDir scans dir for <dir>/*/plugin.json and returns the plugins
// that loaded, sorted by name, plus one error per plugin that didn't.
//
// The contract, matching every other integration in this codebase:
//
//   - dir == "" or missing → (nil, nil). The common case says nothing
//     at all; a user with no plugins should never see a message.
//   - a directory with no plugin.json → skipped silently. Plugin
//     directories sit next to whatever else the user keeps there.
//   - one unreadable / unparseable / invalid manifest → one error
//     naming it, and the OTHER plugins still load. Per-plugin
//     degradation is the theme registry's rule and it matters more
//     here: a plugin is somebody else's code and a typo in it must
//     not cost you the ones that work.
func LoadDir(dir string) ([]Plugin, []error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read %s: %w", dir, err)}
	}

	var out []Plugin
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		path := filepath.Join(sub, ManifestName)
		if _, statErr := os.Stat(path); statErr != nil {
			// No manifest — not a plugin directory. Silent by design.
			continue
		}
		p, loadErr := LoadFile(path)
		if loadErr != nil {
			errs = append(errs, loadErr)
			continue
		}
		out = append(out, p)
	}
	// Sorted so the menu, the palette, and the leader table agree on
	// order run to run — a plugin whose row moves because readdir
	// returned a different sequence is a plugin nobody can build muscle
	// memory for.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

// LoadFile reads and validates one manifest. The plugin's Name defaults
// to its directory's base name, so the smallest working manifest is a
// single "commands" array.
func LoadFile(path string) (Plugin, error) {
	dir := filepath.Dir(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return Plugin{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Plugin{}, fmt.Errorf("parse %s: %w", path, err)
	}

	p := Plugin{
		Name:        strings.TrimSpace(m.Name),
		Description: strings.TrimSpace(m.Description),
		Commands:    m.Commands,
		Hooks:       m.Hooks,
		Decorations: m.Decorations,
		Dir:         dir,
	}
	if abs, absErr := filepath.Abs(dir); absErr == nil {
		p.Dir = abs
	}
	if p.Name == "" {
		p.Name = filepath.Base(dir)
	}
	if err := p.validate(path); err != nil {
		return Plugin{}, err
	}
	return p, nil
}

// validate normalises defaults in place and rejects the manifest
// outright when something in it can't be made to mean anything.
//
// The split between "drop it" and "reject the file" follows Load in
// customactions: a half-written entry (blank label or command) is
// dropped so one unfinished line doesn't cost the user their whole
// plugin, but anything that WOULD load and then misbehave — an unknown
// mode, a two-rune leader, replace-with-nothing-to-replace — is an
// error naming the offender, because the alternative is a menu row that
// silently does the wrong thing.
func (p *Plugin) validate(path string) error {
	cmds := make([]Command, 0, len(p.Commands))
	seenID := make(map[string]struct{}, len(p.Commands))
	for i := range p.Commands {
		c := p.Commands[i]
		c.Label = strings.TrimSpace(c.Label)
		c.Command = strings.TrimSpace(c.Command)
		c.ID = strings.TrimSpace(c.ID)
		if c.Label == "" || c.Command == "" {
			continue
		}
		if c.ID == "" {
			c.ID = c.Label
		}
		if _, dup := seenID[c.ID]; dup {
			return fmt.Errorf("%s: duplicate command id %q", path, c.ID)
		}
		seenID[c.ID] = struct{}{}

		if c.Input == "" {
			c.Input = InputNone
		}
		if c.Output == "" {
			c.Output = OutputNone
		}
		switch c.Input {
		case InputNone, InputSelection, InputFile:
		default:
			return fmt.Errorf("%s: command %q: unknown input %q (want %q, %q or %q)",
				path, c.ID, c.Input, InputNone, InputSelection, InputFile)
		}
		switch c.Output {
		case OutputNone, OutputReplace, OutputInsert, OutputInfo, OutputFlash, OutputReload:
		default:
			return fmt.Errorf("%s: command %q: unknown output %q", path, c.ID, c.Output)
		}
		// "replace" names a range, and only the input modes carve one
		// out. Guessing (the selection if there is one, else the file)
		// would make the same row destroy different amounts of text
		// depending on where the cursor happened to be.
		if c.Output == OutputReplace && c.Input == InputNone {
			return fmt.Errorf("%s: command %q: output %q needs input %q or %q — "+
				"there is no range to replace otherwise",
				path, c.ID, OutputReplace, InputSelection, InputFile)
		}
		if c.Leader != "" && len([]rune(c.Leader)) != 1 {
			return fmt.Errorf("%s: command %q: leader %q must be exactly one character",
				path, c.ID, c.Leader)
		}
		if err := customactions.ValidatePrompts(c.Label, c.Prompts); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		cmds = append(cmds, c)
	}
	p.Commands = cmds

	hooks := make([]Hook, 0, len(p.Hooks))
	for i := range p.Hooks {
		h := p.Hooks[i]
		h.Command = strings.TrimSpace(h.Command)
		h.Glob = strings.TrimSpace(h.Glob)
		if h.Command == "" {
			continue
		}
		if h.Output == "" {
			h.Output = OutputNone
		}
		switch h.Output {
		// A hook has no cursor and no selection to write into, so the
		// three modes that need one are not offered here. Its command
		// edits $FILE and asks for a reload, or it says something.
		case OutputNone, OutputFlash, OutputInfo, OutputReload:
		default:
			return fmt.Errorf("%s: hook %d: output %q not valid for a hook "+
				"(want %q, %q, %q or %q)",
				path, i, h.Output, OutputNone, OutputFlash, OutputInfo, OutputReload)
		}
		if err := validateEvents(path, fmt.Sprintf("hook %d", i), h.On); err != nil {
			return err
		}
		if err := validateGlob(path, fmt.Sprintf("hook %d", i), h.Glob); err != nil {
			return err
		}
		hooks = append(hooks, h)
	}
	p.Hooks = hooks

	provs := make([]Provider, 0, len(p.Decorations))
	seenProv := make(map[string]struct{}, len(p.Decorations))
	for i := range p.Decorations {
		d := p.Decorations[i]
		d.ID = strings.TrimSpace(d.ID)
		d.Command = strings.TrimSpace(d.Command)
		d.Glob = strings.TrimSpace(d.Glob)
		if d.Command == "" {
			continue
		}
		// Unlike a command's, a provider's id is REQUIRED: it's the key
		// a re-run replaces its own marks under, and two providers
		// sharing one would erase each other on every save.
		if d.ID == "" {
			return fmt.Errorf("%s: decoration %d: id is required", path, i)
		}
		if _, dup := seenProv[d.ID]; dup {
			return fmt.Errorf("%s: duplicate decoration id %q", path, d.ID)
		}
		seenProv[d.ID] = struct{}{}
		if err := validateEvents(path, fmt.Sprintf("decoration %q", d.ID), d.On); err != nil {
			return err
		}
		if err := validateGlob(path, fmt.Sprintf("decoration %q", d.ID), d.Glob); err != nil {
			return err
		}
		provs = append(provs, d)
	}
	p.Decorations = provs
	return nil
}

// validateEvents rejects unknown event names and an empty list. An
// empty "on" is a hook that can never fire — almost certainly a
// forgotten line rather than a deliberate no-op, and silently loading
// it would leave the user waiting for something that isn't coming.
func validateEvents(path, what string, on []Event) error {
	if len(on) == 0 {
		return fmt.Errorf("%s: %s: on is required (%q, %q and/or %q)",
			path, what, EventOpen, EventSave, EventEdit)
	}
	for _, e := range on {
		switch e {
		case EventOpen, EventSave, EventEdit:
		default:
			return fmt.Errorf("%s: %s: unknown event %q (want %q, %q or %q)",
				path, what, e, EventOpen, EventSave, EventEdit)
		}
	}
	return nil
}

// validateGlob rejects a pattern filepath.Match can't parse, so the
// failure surfaces at load time with the plugin's name on it rather
// than as a hook that mysteriously never fires.
func validateGlob(path, what, glob string) error {
	if glob == "" {
		return nil
	}
	if _, err := filepath.Match(glob, "probe"); err != nil {
		return fmt.Errorf("%s: %s: bad glob %q: %w", path, what, glob, err)
	}
	return nil
}
