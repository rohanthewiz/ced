// =============================================================================
// File: internal/userconfig/userconfig.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// Package userconfig loads the editor's small user-level config from
// ~/.config/ced/config.json. It's separate from customactions on
// purpose: actions.json is a list of shell-out menu entries, config.json
// is editor preferences. Keeping them apart means a malformed actions
// file can't break editor settings and vice-versa.
//
// Schema today is intentionally tiny, but the loader is wrapped in a
// struct so we can grow new top-level fields without breaking older
// configs:
//
//	{"icons": "auto"}       // default; auto-detect Nerd Fonts on startup
//	{"icons": "on"}         // force-on, even if detection would say no
//	{"icons": "off"}        // force-off, even if a Nerd Font is installed
//	{"autosave": "on"}      // default; save dirty buffers after an idle pause
//	{"autosave": "off"}     // only explicit ≡ → Save writes to disk
//	{"autosavedelay": "5s"} // default; how long that idle pause is.
//	                        // A duration string or a bare number of
//	                        // seconds, clamped to [500ms, 5m]. No ≡ row —
//	                        // hand-edited only.
//	{"termdock": "bottom"}  // default; terminal panel is a bottom strip
//	{"termdock": "left"}    // terminal docks as a vertical strip on the
//	                        // left; the file tree flips to the right
//	{"findalldock": "top"}  // default; the Find-all results list is a
//	                        // strip under the tab bar
//	{"findalldock": "right"}// the list docks as a tall column on the
//	                        // right of the editor instead
//	{"execmarks": "on"}     // default; append an ls -F '*' to executables
//	{"execmarks": "off"}    // hide the executable marker in the file tree
//	{"treeautofit": "on"}   // default; the file tree widens itself to fit
//	                        // its longest visible row whenever the editor
//	                        // can spare the columns
//	{"treeautofit": "off"}  // the sidebar keeps whatever width it has —
//	                        // set by dragging the splitter. Dragging the
//	                        // splitter also writes this key off.
//	{"scrollbar": "on"}     // default; draggable vertical scrollbars on
//	                        // the editor and the file tree, thumb sized
//	                        // to the share of the content on screen
//	{"scrollbar": "off"}    // no bars — the editor keeps that column of
//	                        // code width
//	{"copilot": "on"}       // default; run copilot-language-server when
//	                        // it's installed (silent no-op when absent)
//	{"copilot": "off"}      // never spawn the Copilot sidecar
//	{"suggestions": "on"}   // default; show Copilot ghost-text inline
//	                        // completions while typing (needs copilot on)
//	{"suggestions": "off"}  // sidecar may run (sign-in, chat later) but
//	                        // never paints ghost text
//	{"chatmodel": "<id>"}   // preferred Copilot chat model id (e.g.
//	                        // "claude-sonnet-4.6"); "" or absent keeps
//	                        // the agent's own default. Ids are
//	                        // server-defined, so no validation here.
//	{"chatagent": "<id>"}   // preferred chat backend ("copilot",
//	                        // "claude", …); "" or absent means the
//	                        // default (Copilot). The registry lives in
//	                        // the app layer, so no validation here —
//	                        // an unknown id falls back at resolve time.
//	{"chatwrite": "on"}     // default; the chat agent may change files
//	                        // (each change still asks permission first)
//	{"chatwrite": "off"}    // read-only chat: ced declares no write
//	                        // capability and refuses every edit
//	{"remote": "on"}        // default; listen on a unix socket so
//	                        // `ced --remote` / `ced --wait` in another
//	                        // pane can hand this instance a file
//	{"remote": "off"}       // never create the socket; a remote open
//	                        // then falls back to its own editor
//	{"session": "on"}       // default; opening a folder reopens the tabs
//	                        // and cursor positions it had last time
//	{"session": "off"}      // folders are still remembered (for the
//	                        // recent list), but tabs are never reopened
//	{"commitmsgtrailer": "on"}
//	                        // default; a commit message the chat agent
//	                        // DRAFTED carries a Co-Authored-By trailer
//	                        // naming the agent. Hand-typed messages never
//	                        // get one, whatever this says.
//	{"commitmsgtrailer": "off"}
//	                        // never add it. The commit prompt's chip
//	                        // overrides this for one commit either way.
//	{"theme": "<name>"}     // named color theme ("tokyo-night" — the
//	                        // default — "darcula", "solarized-light", …,
//	                        // or a user theme from themes/*.json). Not
//	                        // validated here: the registry is app-layer
//	                        // knowledge and an unknown name falls back
//	                        // to the default at resolve time.
//
// The loader is best-effort the same way customactions is: missing
// file → defaults, malformed file → error returned for the app to
// flash, but the editor still starts cleanly.
package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// IconsMode is the user's preference for Nerd Font icons in the file
// tree. "auto" means "use them iff a Nerd Font is installed"; the
// other two values bypass detection entirely.
type IconsMode string

const (
	IconsAuto IconsMode = "auto"
	IconsOn   IconsMode = "on"
	IconsOff  IconsMode = "off"
)

// TermDock is where the terminal panel docks: the classic bottom
// strip, or a vertical strip on the left (which flips the file tree
// to the right edge).
type TermDock string

const (
	TermDockBottom TermDock = "bottom"
	TermDockLeft   TermDock = "left"
)

// FindAllDock is where the Find-all results list sits: a wide strip
// under the tab bar, or a tall column down the editor's right edge.
// Two layouts because the two shapes answer different questions — the
// strip shows long lines, the column shows many more of them at once.
type FindAllDock string

const (
	FindAllDockTop   FindAllDock = "top"
	FindAllDockRight FindAllDock = "right"
)

// Auto-save timing. The default is the idle pause the feature is tuned
// for; the floor and the ceiling are the range a hand-edited value is
// clamped into.
//
// The floor is not arbitrary. Every auto-save also runs format-on-save
// (which execs a formatter), an LSP didSave, and a git-status refresh, so
// a sub-half-second setting is a fork bomb wearing a preference's
// clothes. The ceiling is where it stops being auto-save at all — and
// what makes a long delay comfortable is the focus-out flush
// (app/autosave.go), not a bigger number here.
const (
	DefaultAutoSaveDelay = 5 * time.Second
	MinAutoSaveDelay     = 500 * time.Millisecond
	MaxAutoSaveDelay     = 5 * time.Minute
)

// parseAutoSaveDelay reads the "autosavedelay" value. A duration string
// ("5s", "800ms", "1m") is the documented form; a bare number is accepted
// as seconds for the same reason the commitmsgtrailer tag is matched
// case-insensitively — a user who writes what they meant should get it.
// An empty value returns 0, which the caller reads as "key absent".
// Clamping is the caller's job so this stays a parser.
func parseAutoSaveDelay(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if secs, err := strconv.Atoi(s); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	return 0, fmt.Errorf("invalid duration %q", s)
}

// clampAutoSaveDelay pins d into the supported range. A value outside it
// is honoured at the nearest end rather than rejected — see the constants
// above for why the ends are where they are.
func clampAutoSaveDelay(d time.Duration) time.Duration {
	if d < MinAutoSaveDelay {
		return MinAutoSaveDelay
	}
	if d > MaxAutoSaveDelay {
		return MaxAutoSaveDelay
	}
	return d
}

// Config is the resolved, validated form of config.json. Callers get a
// fully-populated Config back from Load — defaults are filled in for
// any field the file omitted, so consumers never need to nil-check.
type Config struct {
	Icons IconsMode

	// AutoSave controls whether dirty buffers are written to disk
	// automatically after an idle pause. Defaults to on — the editor
	// is opinionated toward "your work is always on disk", and the
	// ≡ menu toggle (which persists here) is the escape hatch.
	AutoSave bool

	// AutoSaveDelay is how long the user must be idle before that write
	// happens, already parsed and clamped. Hand-edited only: unlike
	// AutoSave there is no ≡ row, because the menu is for verbs and a
	// duration is a tuning knob — it would need a picker of canned values
	// to have any menu shape at all, for a setting nobody adjusts twice.
	AutoSaveDelay time.Duration

	// TermDock is the terminal panel's home edge. Defaults to the
	// bottom strip; "left" selects the alternate layout (terminal
	// vertical on the left, file tree on the right). Persisted by
	// the ≡ layout toggle, same as AutoSave.
	TermDock TermDock

	// FindAllDock is the Find-all list's edge. Defaults to the top
	// strip. Persisted by the popup's own ◨ button and the ≡ view
	// toggle — a layout preference the user sets once, like TermDock.
	FindAllDock FindAllDock

	// ExecMarks controls whether the file tree appends an ls -F style
	// '*' to executable regular files. Defaults to on. Persisted by the
	// ≡ view toggle, same as AutoSave.
	ExecMarks bool

	// TreeAutoFit controls whether the sidebar re-derives its width from
	// the file tree's own longest row, so a deep folder stops truncating
	// names the moment it's expanded. Defaults to on. Off means the width
	// is whatever the splitter drag last set — which is also why the drag
	// itself persists this key off: the two cannot both own the number.
	TreeAutoFit bool

	// Scrollbar controls whether the editor body reserves its rightmost
	// column for a draggable scrollbar, and whether the file tree paints
	// one over its own last column. Defaults to on: "how much of this am
	// I looking at" has no other answer in a terminal editor, and the
	// status bar's line count can't show it at a glance. Off is here
	// because the editor's bar costs a column of code width, which a user
	// on an 80-column terminal may well want back. One key for both
	// surfaces — see app/scrollbar.go. Persisted by the ≡ view toggle,
	// same as ExecMarks.
	Scrollbar bool

	// WordHL controls whether the editor tints the other visible
	// instances of the word under the cursor. Defaults to on — reading
	// code is the editor's primary job and "where else is this used"
	// is the question you ask most while doing it. Off is here because
	// the wash is ambient decoration nobody asked for, which is
	// precisely the kind of thing a subset of users find noisy.
	// Persisted by the ≡ view toggle, same as ExecMarks.
	WordHL bool

	// Copilot controls whether the editor runs the GitHub Copilot
	// sidecar (copilot-language-server). Defaults to on because the
	// binary is only ever spawned when the user has installed it —
	// presence on PATH is itself the opt-in; this key is the opt-out
	// for people who have the binary for other editors but don't want
	// ced touching it. Persisted by the ≡ toggle, same as AutoSave.
	Copilot bool

	// Suggestions controls whether Copilot ghost-text inline completions
	// are requested and painted while typing. Separate from Copilot so a
	// user can keep the sidecar (sign-in today, chat later) while opting
	// out of just the ghost text — the most intrusive part. Defaults to
	// on; moot while Copilot is off. Persisted by the ≡ toggle.
	Suggestions bool

	// ChatModel is the preferred Copilot chat model id, applied via
	// ACP session/set_model after each session opens. Empty (the
	// default) keeps the agent's own default. Deliberately not
	// validated against a fixed list — the roster is server-defined
	// and changes without an ced release; a stale id is silently
	// ignored at apply time instead. Persisted by the ≡ model picker.
	ChatModel string

	// ChatAgent is the preferred chat backend's registry id ("copilot",
	// "claude", …). Empty (the default) means the default backend.
	// Deliberately not validated here — the agent registry is app-layer
	// knowledge, and an id from a newer or older ced must not break
	// config loading; the app falls back to its default for unknown ids,
	// the same stale-preference rule as ChatModel. Persisted by the ≡
	// agent picker.
	ChatAgent string

	// ChatContext controls whether every chat prompt automatically
	// carries the active tab (or just its selection, when there is
	// one) as attached context. Defaults to on — "what about this
	// file?" is the question a chat panel inside an editor exists to
	// answer. Separate from Copilot and Suggestions because it is the
	// one knob with a per-turn token cost the user may want to control
	// independently. Persisted by the ≡ toggle.
	ChatContext bool

	// ChatWrite controls whether the chat agent may change anything on
	// disk: ced's own fs/write_text_file handler, and any tool call the
	// agent labels as a mutation. Defaults to on — every change still
	// asks permission first, which is the primary guard. Off is the
	// belt-and-braces "read-only chat" posture for the times you want to
	// ask questions with no possibility of an edit landing, however the
	// prompt is answered. Separate from ChatContext because reading your
	// code and rewriting it are different levels of trust. Persisted by
	// the ≡ toggle.
	ChatWrite bool

	// Plugins controls whether declarative plugins under
	// ~/.config/ced/plugins are loaded and allowed to run. Defaults to
	// on: like the Copilot binary, having written a manifest is itself
	// the opt-in — nothing exists to run until the user creates one.
	// This key is the kill switch for the times you want the editor to
	// touch nothing of yours (bisecting a misbehaving hook, or handing
	// the terminal to somebody else). Persisted by the ≡ toggle.
	Plugins bool

	// Remote controls whether the editor listens on a unix socket so a
	// `ced --remote` / `ced --wait` in another pane can hand it a file
	// instead of starting a second editor. Defaults to on: the whole
	// premise of this editor is one instance per project inside tmux,
	// and without the socket `EDITOR=ced` nests a full-screen TUI inside
	// the first instance's terminal strip. Off is for a shared or
	// hardened machine where an extra listening socket — even one in a
	// 0700 per-user runtime directory — is one more thing to reason
	// about. Persisted by the ≡ toggle, same as AutoSave.
	Remote bool

	// Session controls whether opening a folder reopens the tabs and
	// cursor positions it had when it was last closed. Defaults to on —
	// an editor you point at a project should give you back the project,
	// which is what every graphical editor already does. Off is for
	// people who want a deliberate blank slate each time; the folder is
	// still RECORDED either way, because the recent-folders list is a
	// different feature reading the same file. Persisted by the ≡ toggle,
	// same as AutoSave.
	Session bool

	// CommitMsgTrailer controls whether a commit message the chat agent
	// DRAFTED carries a Co-Authored-By trailer naming the agent. Defaults
	// to on: a machine wrote the sentence, and the trailer is the record
	// of that — the same convention every agent CLI already writes. Off
	// is for people whose projects don't want machine co-authors in the
	// log at all. It never applies to a message the user typed, so this
	// key cannot put attribution on human work.
	//
	// Set from the ≡ Git toggle, which persists it; the ✦ commit prompt's
	// [trailer: on] chip overrides it for a single commit without writing
	// anything back.
	CommitMsgTrailer bool

	// Theme is the named color theme's registry id ("tokyo-night",
	// "darcula", a user theme's filename stem, …). Empty (the default)
	// means the shipped default. Deliberately not validated here for
	// the same reason as ChatModel and ChatAgent: the theme registry
	// includes files the user can add and remove between runs, so an
	// unresolvable name has to be a silent fallback at resolve time,
	// not a config-load failure that stops the editor starting.
	// Persisted by the ≡ theme picker.
	Theme string
}

// Defaults returns a Config populated with the values used when no
// config file is present (or every field in it is blank). Centralised
// so tests and the loader can't drift from each other.
func Defaults() Config {
	return Config{Icons: IconsAuto, AutoSave: true, AutoSaveDelay: DefaultAutoSaveDelay, TermDock: TermDockBottom, FindAllDock: FindAllDockTop, ExecMarks: true, TreeAutoFit: true, Scrollbar: true, WordHL: true, Copilot: true, Suggestions: true, ChatContext: true, ChatWrite: true, Plugins: true, Remote: true, Session: true, CommitMsgTrailer: true}
}

// fileFormat mirrors the on-disk JSON shape. We decode into this and
// then promote into Config so the public type doesn't have to carry
// JSON tags or pointer fields just for "field was absent" detection.
// AutoSave is a string ("on"/"off"), not a bool, for the same absent-
// field reason: a missing key must mean "keep the default", and JSON
// false is indistinguishable from absent on a plain bool.
type fileFormat struct {
	Icons string `json:"icons,omitempty"`
	// AutoSaveDelay is a string for the same absent-field reason as the
	// on/off keys, plus one of its own: saveKey writes every value into a
	// map[string]any as a string, so a JSON number would be the one key
	// here that couldn't survive a ≡ toggle rewriting the file.
	AutoSave      string `json:"autosave,omitempty"`
	AutoSaveDelay string `json:"autosavedelay,omitempty"`
	TermDock      string `json:"termdock,omitempty"`
	FindAllDock   string `json:"findalldock,omitempty"`
	ExecMarks     string `json:"execmarks,omitempty"`
	TreeAutoFit   string `json:"treeautofit,omitempty"`
	Scrollbar     string `json:"scrollbar,omitempty"`
	WordHL        string `json:"wordhl,omitempty"`
	Copilot       string `json:"copilot,omitempty"`
	Suggestions   string `json:"suggestions,omitempty"`
	ChatModel     string `json:"chatmodel,omitempty"`
	ChatAgent     string `json:"chatagent,omitempty"`
	ChatContext   string `json:"chatcontext,omitempty"`
	ChatWrite     string `json:"chatwrite,omitempty"`
	Plugins       string `json:"plugins,omitempty"`
	Remote        string `json:"remote,omitempty"`
	Session       string `json:"session,omitempty"`
	Theme         string `json:"theme,omitempty"`

	// The tag is lowercase like every other key here. encoding/json
	// matches field tags case-INSENSITIVELY, so the camelCase spelling
	// the roadmap used ("commitMsgTrailer") loads the same key — which is
	// the point: a user copying either spelling out of a document gets
	// the setting they meant, not a silently ignored line.
	CommitMsgTrailer string `json:"commitmsgtrailer,omitempty"`
}

// configFilePath resolves the ced config directory
// ($XDG_CONFIG_HOME/ced, else ~/.config/ced) and joins name onto it.
// Returns "" when neither XDG_CONFIG_HOME nor a home directory resolves,
// which callers treat as "no config location — use defaults / skip".
// DefaultPath and RcPath both go through here so the two files can never
// drift into different directories.
func configFilePath(name string) string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ced", name)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "ced", name)
}

// DefaultPath returns the canonical config-file location:
// $XDG_CONFIG_HOME/ced/config.json, falling back to
// ~/.config/ced/config.json. Returns "" when neither resolves
// — callers should treat that as "use defaults".
func DefaultPath() string { return configFilePath("config.json") }

// RcPath returns the canonical grsh rc-file location:
// $XDG_CONFIG_HOME/ced/rc.grsh, falling back to ~/.config/ced/rc.grsh
// (or "" when no config location resolves).
//
// This is the grsh analog of ~/.zshrc: the embedded terminal panel
// sources it once, when the grsh session is created, so a user's aliases
// and functions are available in every ced shell. It MUST be grsh
// syntax, not zsh — the terminal embeds grsh (its own shell language),
// so it never reads ~/.zshrc or any zsh startup file, and this file is
// exactly the gap that fills.
func RcPath() string { return configFilePath("rc.grsh") }

// MCPPath returns the canonical MCP-inventory location:
// $XDG_CONFIG_HOME/ced/mcp.json, falling back to ~/.config/ced/mcp.json
// (or "" when no config location resolves).
//
// It's a file of its own, not a key in config.json, for the same reason
// actions.json is: config.json holds flat preferences the ≡ toggles
// write back, while mcp.json is a nested inventory the user hand-edits
// in the shape the rest of the MCP ecosystem already uses. Keeping them
// apart means a syntax error in one can't disable the other. The parser
// lives in internal/mcp; this package only knows where the file is, so
// the two config locations can never drift apart.
func MCPPath() string { return configFilePath("mcp.json") }

// StatePath returns the canonical workspace-state location:
// $XDG_CONFIG_HOME/ced/state.json, falling back to
// ~/.config/ced/state.json (or "" when no config location resolves).
//
// A file of its own, and the reasoning is the inverse of mcp.json's.
// That one is separate because the user hand-writes it; this one is
// separate because ced REWRITES it constantly — every folder switch and
// every exit — and machine churn has no business landing in the same
// file as preferences somebody edited by hand. It also means a corrupt
// state file costs a tab list, not a settings file. The schema lives in
// internal/session; this package only knows where the file is, so the
// config locations can never drift apart.
func StatePath() string { return configFilePath("state.json") }

// ThemesDir returns the canonical user-themes directory:
// $XDG_CONFIG_HOME/ced/themes, falling back to ~/.config/ced/themes
// (or "" when no config location resolves).
//
// A directory rather than a key in config.json, for the mcp.json reason:
// config.json holds the flat preferences the ≡ toggles write back, while
// a theme is a document the user hand-edits — and one file per theme is
// what makes "shadow a built-in by name" and "delete this theme" both
// obvious operations. The parser and registry live in internal/theme;
// this package only knows where the directory is, so the two config
// locations can never drift apart.
func ThemesDir() string { return configFilePath("themes") }

// SkillsDir returns the canonical ced-owned skills directory:
// $XDG_CONFIG_HOME/ced/skills, falling back to ~/.config/ced/skills
// (or "" when no config location resolves).
//
// It is ONE of the directories the skills inventory scans, and the least
// used of them: the ecosystem keeps personal skills in ~/.claude/skills
// and project skills in <project>/.claude/skills, and ced reads both of
// those as they are rather than asking anyone to duplicate a folder. This
// directory is for skills written for ced itself. See internal/skills for
// the scan order and the shadowing rule; this package only knows where
// the directory is, so the config locations can never drift apart.
func SkillsDir() string { return configFilePath("skills") }

// PluginsDir returns the canonical plugins directory:
// $XDG_CONFIG_HOME/ced/plugins, falling back to ~/.config/ced/plugins
// (or "" when no config location resolves).
//
// A directory of directories, for the themes reason: one folder per
// plugin is what makes "this plugin ships a script beside its manifest"
// and "delete this plugin" both obvious operations, and it gives every
// command a $PLUGIN_DIR to resolve its own files against. The parser
// and the validation rules live in internal/plugins; this package only
// knows where the directory is, so the config locations can never drift
// apart.
func PluginsDir() string { return configFilePath("plugins") }

// Load reads and parses the config file at path, returning a Config
// with defaults filled in for any missing or blank fields.
//
// Contract:
//   - path == ""              → (Defaults(), nil). Treated as "no
//     config configured".
//   - file doesn't exist      → (Defaults(), nil). Same as above.
//   - file unreadable         → (Defaults(), err). Caller can flash a
//     message; editor keeps running on defaults.
//   - file empty / all-blank  → (Defaults(), nil).
//   - unknown icons value     → (Defaults(), err). We'd rather tell
//     the user their config has a typo than silently fall back to
//     defaults and hide the bug.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return cfg, nil
	}

	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}

	switch IconsMode(strings.ToLower(strings.TrimSpace(ff.Icons))) {
	case "":
		// field omitted — keep default
	case IconsAuto:
		cfg.Icons = IconsAuto
	case IconsOn:
		cfg.Icons = IconsOn
	case IconsOff:
		cfg.Icons = IconsOff
	default:
		return Defaults(), fmt.Errorf(
			"%s: icons must be %q, %q, or %q (got %q)",
			path, IconsAuto, IconsOn, IconsOff, ff.Icons,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.AutoSave)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.AutoSave = true
	case "off":
		cfg.AutoSave = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: autosave must be \"on\" or \"off\" (got %q)",
			path, ff.AutoSave,
		)
	}

	switch TermDock(strings.ToLower(strings.TrimSpace(ff.TermDock))) {
	case "":
		// field omitted — keep default
	case TermDockBottom:
		cfg.TermDock = TermDockBottom
	case TermDockLeft:
		cfg.TermDock = TermDockLeft
	default:
		return Defaults(), fmt.Errorf(
			"%s: termdock must be %q or %q (got %q)",
			path, TermDockBottom, TermDockLeft, ff.TermDock,
		)
	}

	switch FindAllDock(strings.ToLower(strings.TrimSpace(ff.FindAllDock))) {
	case "":
		// field omitted — keep default
	case FindAllDockTop:
		cfg.FindAllDock = FindAllDockTop
	case FindAllDockRight:
		cfg.FindAllDock = FindAllDockRight
	default:
		return Defaults(), fmt.Errorf(
			"%s: findalldock must be %q or %q (got %q)",
			path, FindAllDockTop, FindAllDockRight, ff.FindAllDock,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.ExecMarks)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.ExecMarks = true
	case "off":
		cfg.ExecMarks = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: execmarks must be \"on\" or \"off\" (got %q)",
			path, ff.ExecMarks,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.TreeAutoFit)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.TreeAutoFit = true
	case "off":
		cfg.TreeAutoFit = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: treeautofit must be \"on\" or \"off\" (got %q)",
			path, ff.TreeAutoFit,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.Scrollbar)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.Scrollbar = true
	case "off":
		cfg.Scrollbar = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: scrollbar must be \"on\" or \"off\" (got %q)",
			path, ff.Scrollbar,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.WordHL)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.WordHL = true
	case "off":
		cfg.WordHL = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: wordhl must be \"on\" or \"off\" (got %q)",
			path, ff.WordHL,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.Copilot)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.Copilot = true
	case "off":
		cfg.Copilot = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: copilot must be \"on\" or \"off\" (got %q)",
			path, ff.Copilot,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.Suggestions)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.Suggestions = true
	case "off":
		cfg.Suggestions = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: suggestions must be \"on\" or \"off\" (got %q)",
			path, ff.Suggestions,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.Plugins)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.Plugins = true
	case "off":
		cfg.Plugins = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: plugins must be \"on\" or \"off\" (got %q)",
			path, ff.Plugins,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.ChatContext)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.ChatContext = true
	case "off":
		cfg.ChatContext = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: chatcontext must be \"on\" or \"off\" (got %q)",
			path, ff.ChatContext,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.ChatWrite)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.ChatWrite = true
	case "off":
		cfg.ChatWrite = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: chatwrite must be \"on\" or \"off\" (got %q)",
			path, ff.ChatWrite,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.Remote)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.Remote = true
	case "off":
		cfg.Remote = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: remote must be \"on\" or \"off\" (got %q)",
			path, ff.Remote,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.Session)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.Session = true
	case "off":
		cfg.Session = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: session must be \"on\" or \"off\" (got %q)",
			path, ff.Session,
		)
	}

	switch strings.ToLower(strings.TrimSpace(ff.CommitMsgTrailer)) {
	case "":
		// field omitted — keep default
	case "on":
		cfg.CommitMsgTrailer = true
	case "off":
		cfg.CommitMsgTrailer = false
	default:
		return Defaults(), fmt.Errorf(
			"%s: commitmsgtrailer must be \"on\" or \"off\" (got %q)",
			path, ff.CommitMsgTrailer,
		)
	}

	// A typo is LOUD (the rule every key above follows — a silently
	// ignored value is one the user believes is in effect), but a value
	// merely out of range is CLAMPED in silence: "50ms" is still a
	// coherent expression of "save aggressively", so honouring the intent
	// at the floor beats refusing the whole config over it.
	switch d, perr := parseAutoSaveDelay(ff.AutoSaveDelay); {
	case perr != nil:
		return Defaults(), fmt.Errorf(
			"%s: autosavedelay must be a duration like \"5s\" or a number of seconds (got %q)",
			path, ff.AutoSaveDelay,
		)
	case d == 0:
		// field omitted — keep default
	default:
		cfg.AutoSaveDelay = clampAutoSaveDelay(d)
	}

	// Any non-blank value is accepted as-is — see Config.ChatModel,
	// Config.ChatAgent, and Config.Theme for why there's no allowlist to
	// check against.
	cfg.ChatModel = strings.TrimSpace(ff.ChatModel)
	cfg.ChatAgent = strings.ToLower(strings.TrimSpace(ff.ChatAgent))
	cfg.Theme = strings.ToLower(strings.TrimSpace(ff.Theme))
	return cfg, nil
}

// SaveAutoSave persists the auto-save preference into the config file
// at path. See saveKey for the round-trip guarantees.
func SaveAutoSave(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "autosave", val)
}

// SaveTermDock persists the terminal-dock preference into the config
// file at path. See saveKey for the round-trip guarantees.
func SaveTermDock(path string, dock TermDock) error {
	return saveKey(path, "termdock", string(dock))
}

// SaveFindAllDock persists the Find-all dock preference into the config
// file at path. See saveKey for the round-trip guarantees.
func SaveFindAllDock(path string, dock FindAllDock) error {
	return saveKey(path, "findalldock", string(dock))
}

// SaveExecMarks persists the executable-marker preference into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveExecMarks(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "execmarks", val)
}

// SaveTreeAutoFit persists the file-tree auto-fit preference into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveTreeAutoFit(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "treeautofit", val)
}

// SaveScrollbar persists the editor scrollbar preference into the config
// file at path. See saveKey for the round-trip guarantees.
func SaveScrollbar(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "scrollbar", val)
}

// SaveWordHL persists the matching-word-highlight preference into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveWordHL(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "wordhl", val)
}

// SaveCopilot persists the Copilot-sidecar preference into the config
// file at path. See saveKey for the round-trip guarantees.
func SaveCopilot(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "copilot", val)
}

// SaveSuggestions persists the ghost-text inline-completion preference
// into the config file at path. See saveKey for the round-trip
// guarantees.
func SaveSuggestions(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "suggestions", val)
}

// SaveChatModel persists the preferred Copilot chat model id into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveChatModel(path, id string) error {
	return saveKey(path, "chatmodel", id)
}

// SaveChatAgent persists the preferred chat backend's registry id into
// the config file at path. See saveKey for the round-trip guarantees.
func SaveChatAgent(path, id string) error {
	return saveKey(path, "chatagent", id)
}

// SaveChatContext persists the auto-attach-current-file preference into
// the config file at path. See saveKey for the round-trip guarantees.
func SaveChatContext(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "chatcontext", val)
}

// SaveChatWrite persists the agent-may-change-files preference into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveChatWrite(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "chatwrite", val)
}

// SavePlugins persists the plugin kill switch into the config file at
// path. See saveKey for the round-trip guarantees.
func SavePlugins(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "plugins", val)
}

// SaveRemote persists the remote-open listener preference into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveRemote(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "remote", val)
}

// SaveSession persists the restore-tabs-on-open preference into the
// config file at path. See saveKey for the round-trip guarantees.
func SaveSession(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "session", val)
}

// SaveCommitMsgTrailer persists the drafted-commit-attribution
// preference into the config file at path. See saveKey for the
// round-trip guarantees.
func SaveCommitMsgTrailer(path string, on bool) error {
	val := "on"
	if !on {
		val = "off"
	}
	return saveKey(path, "commitmsgtrailer", val)
}

// SaveTheme persists the named color theme into the config file at
// path. See saveKey for the round-trip guarantees.
func SaveTheme(path, name string) error {
	return saveKey(path, "theme", name)
}

// saveKey writes one preference into the config file at path,
// preserving every other key the user may have set by hand (icons
// today, anything we add tomorrow). The read-modify-write goes
// through a raw map — not fileFormat — so keys this binary doesn't
// know about survive a round-trip with a newer or older ced.
// Writes atomically (temp + rename), same as the format-config
// installer, so a crash mid-write can't corrupt the config.
func saveKey(path, key, val string) error {
	if path == "" {
		return errors.New("no config directory resolved — cannot persist preference")
	}
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		if err := json.Unmarshal(data, &raw); err != nil {
			// A malformed config is the user's hand-edit; overwriting
			// it with a single fresh key would eat their file.
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read %s: %w", path, err)
	}

	raw[key] = val

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
