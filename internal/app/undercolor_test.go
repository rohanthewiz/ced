// =============================================================================
// File: internal/app/undercolor_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// envOf turns a map into the env lookup the sniff takes.
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestUnderlineColorTerminal_Allowlist pins which environments vouch for
// SGR 58 and, as importantly, which don't: a generic xterm TERM, Apple
// Terminal and an empty environment stay plain, an old VTE stays plain,
// and the multiplexer rules win over a leaked outer TERM_PROGRAM / cats
// marker.
func TestUnderlineColorTerminal_Allowlist(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"empty", nil, ""},
		{"generic xterm", map[string]string{"TERM": "xterm-256color"}, ""},
		{"Apple Terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, ""},
		{"tmux announcing itself", map[string]string{"TMUX": "/tmp/s,1,0", "TERM_PROGRAM": "tmux", "TERM": "screen-256color"}, "tmux"},
		{"old tmux", map[string]string{"TMUX": "/tmp/s,1,0", "TERM": "screen-256color"}, ""},
		{"tmux in kitty: leaked kitty id ignored", map[string]string{"TMUX": "x", "KITTY_WINDOW_ID": "3"}, ""},
		{"screen inside cats: cats marker ignored", map[string]string{"STY": "1.pts", "CATS_ENV": "1"}, ""},
		{"tmux inside cats", map[string]string{"TMUX": "x", "TERM_PROGRAM": "tmux", "CATS_ENV": "1"}, "tmux"},
		{"zellij", map[string]string{"ZELLIJ": "0"}, "zellij"},
		{"cats pane, leaked Apple Terminal", map[string]string{"CATS_ENV": "1", "TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"}, "cats"},
		{"cats marker must be 1", map[string]string{"CATS_ENV": "0"}, ""},
		{"iTerm2", map[string]string{"TERM_PROGRAM": "iTerm.app"}, "iterm2"},
		{"WezTerm", map[string]string{"TERM_PROGRAM": "WezTerm"}, "wezterm"},
		{"Ghostty by program", map[string]string{"TERM_PROGRAM": "ghostty"}, "ghostty"},
		{"VS Code", map[string]string{"TERM_PROGRAM": "vscode"}, "xterm.js"},
		{"kitty over ssh", map[string]string{"TERM": "xterm-kitty"}, "kitty"},
		{"ghostty over ssh", map[string]string{"TERM": "xterm-ghostty"}, "ghostty"},
		{"foot", map[string]string{"TERM": "foot-extra"}, "foot"},
		{"alacritty", map[string]string{"TERM": "alacritty"}, "alacritty"},
		{"kitty by window id", map[string]string{"KITTY_WINDOW_ID": "1", "TERM": "xterm-256color"}, "kitty"},
		{"Windows Terminal (WSL)", map[string]string{"WT_SESSION": "abc"}, "windows-terminal"},
		{"Konsole", map[string]string{"KONSOLE_VERSION": "230804"}, "konsole"},
		{"new VTE", map[string]string{"VTE_VERSION": "7600"}, "vte"},
		{"old VTE", map[string]string{"VTE_VERSION": "5002"}, ""},
		{"junk VTE", map[string]string{"VTE_VERSION": "x"}, ""},
	}
	for _, c := range cases {
		if got := underlineColorTerminal(envOf(c.env)); got != c.want {
			t.Errorf("%s: underlineColorTerminal = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestResolveUnderlineColor pins the override: on and off ignore the
// environment entirely, auto defers to the sniff.
func TestResolveUnderlineColor(t *testing.T) {
	capable := envOf(map[string]string{"TERM": "xterm-kitty"})
	plain := envOf(map[string]string{"TERM_PROGRAM": "Apple_Terminal"})
	cases := []struct {
		mode userconfig.UnderlineColorMode
		env  func(string) string
		want bool
	}{
		{userconfig.UnderlineColorAuto, capable, true},
		{userconfig.UnderlineColorAuto, plain, false},
		{userconfig.UnderlineColorOn, plain, true},
		{userconfig.UnderlineColorOff, capable, false},
		{"", capable, true}, // a zero Config value behaves as auto
	}
	for _, c := range cases {
		if got := resolveUnderlineColor(c.mode, c.env); got != c.want {
			t.Errorf("resolveUnderlineColor(%q) = %v, want %v", c.mode, got, c.want)
		}
	}
}
