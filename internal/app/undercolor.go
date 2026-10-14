// =============================================================================
// File: internal/app/undercolor.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// undercolor.go decides whether ced may COLOUR an underline — today only
// the line under a tab group's members (tabbar.go), which then takes the
// group's colour instead of the text colour.
//
//	plain:     │ api │ server.go × │ routes.go × │     line in text colour
//	coloured:  │ api │ server.go × │ routes.go × │     line in the chip's hue
//	                 └──────── SGR 58 ─────────┘
//
// Why a check is needed at all: an underline colour is SGR 58, sent in
// its COLON form (tcell writes "58:5:n" or "58:2::r:g:b"). A terminal that
// knows SGR 58 reads that as one attribute. A terminal that doesn't, and
// that splits colons like semicolons, reads "58;5;n" — 58 ignored, then
// 5 = BLINK and n = whatever SGR n means — so the strip would start
// blinking or change colour. There's no reliable runtime query (an
// XTGETTCAP answer would arrive on stdin, which tcell owns and would
// misparse as keys), so the check is an ENVIRONMENT SNIFF.
//
// Design choices:
//
//   - **Allowlist, positive evidence only.** A terminal is accepted only
//     when something in the environment names one known to parse the colon
//     form. Unknown → plain underline, which every terminal draws. Wrongly
//     plain costs a little colour; wrongly coloured costs a blinking strip.
//   - **The innermost multiplexer decides.** ced's output is parsed by the
//     nearest emulator: tmux, GNU screen or Zellij when one is running,
//     otherwise the cats pane or the outer terminal. The outer terminal's
//     variables (TERM_PROGRAM, KITTY_WINDOW_ID, CATS_ENV …) LEAK into
//     multiplexer panes, so the multiplexer is checked first and its
//     answer is final either way.
//   - **tmux is accepted only when it announces itself**
//     (TERM_PROGRAM=tmux). tmux parses SGR 58 and re-emits it only to
//     outer terminals that declare support, dropping it otherwise, so
//     inside a current tmux it is always safe. tmux releases that predate
//     TERM_PROGRAM are not vouched for. This covers the SSH-into-tmux
//     workflow ced is built for, where the outer terminal's own variables
//     never cross the ssh hop.
//   - **cats is accepted by CATS_ENV.** Its panes are parsed by
//     libghostty-vt, Ghostty's own parser.
//   - **An override, not a menu row.** "underlinecolor" in config.json
//     (auto / on / off) is for the case the sniff can't see — most often a
//     capable terminal on the far side of ssh. Hand-edited, like
//     "terminal": nobody flips it twice.
//   - **Necessary, not sufficient.** tcell writes SGR 58 only for terminals
//     whose TERM it treats as xterm-like (any "xterm*", plus a few named
//     entries). A yes here with TERM=screen-256color still draws a plain
//     underline. That's harmless, and why the result isn't shown to the
//     user as a capability.
//
// The accepted terminals are the ones a September 2026 review of terminal
// SGR parsers (vim PR #21412) found to take 58:5:n and 58:2::r:g:b. Very
// old releases of them aren't checked except VTE, which has a version
// variable; "underlinecolor": "off" covers those.

package app

import (
	"strconv"
	"strings"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// vteMinUnderlineColor is the lowest VTE_VERSION (major*10000 +
// minor*100 + micro) accepted: 0.52. It's a conservative minimum, since
// releases before it haven't been checked.
const vteMinUnderlineColor = 5200

// resolveUnderlineColor turns the "underlinecolor" mode into the
// decision tabbar.go reads: on/off are taken as given, auto asks the
// sniff over env.
func resolveUnderlineColor(mode userconfig.UnderlineColorMode, env func(string) string) bool {
	switch mode {
	case userconfig.UnderlineColorOn:
		return true
	case userconfig.UnderlineColorOff:
		return false
	}
	return underlineColorTerminal(env) != ""
}

// underlineColorTerminal names the terminal (or multiplexer) whose
// presence vouches for the colon-form SGR 58, or "" when nothing does.
// The name is for tests and comments, which rule fired; callers only
// test it against "". See the header for the order.
func underlineColorTerminal(env func(string) string) string {
	// Multiplexers first: their answer is final, because the outer
	// terminal's variables leak into their panes.
	if env("STY") != "" {
		return "" // GNU screen: no SGR 58, colon handling unknown
	}
	if env("TMUX") != "" {
		if env("TERM_PROGRAM") == "tmux" {
			return "tmux"
		}
		return "" // a tmux too old to set TERM_PROGRAM
	}
	if env("ZELLIJ") != "" {
		return "zellij"
	}
	if env("CATS_ENV") == "1" {
		return "cats"
	}

	switch env("TERM_PROGRAM") {
	case "iTerm.app":
		return "iterm2"
	case "WezTerm":
		return "wezterm"
	case "ghostty":
		return "ghostty"
	case "vscode":
		return "xterm.js" // VS Code's (and Cursor's) integrated terminal
	case "mintty":
		return "mintty"
	case "rio":
		return "rio"
	}

	// TERM survives ssh, so it's the one signal that still works on the
	// far side of a hop without tmux. Only names that are a single
	// terminal's own count; a generic "xterm-256color" says nothing.
	term := env("TERM")
	switch {
	case strings.Contains(term, "kitty"):
		return "kitty"
	case strings.Contains(term, "ghostty"):
		return "ghostty"
	case term == "foot" || strings.HasPrefix(term, "foot-"):
		return "foot"
	case term == "wezterm":
		return "wezterm"
	case term == "alacritty" || strings.HasPrefix(term, "alacritty-"):
		return "alacritty"
	case term == "rio":
		return "rio"
	case term == "contour" || strings.HasPrefix(term, "contour-"):
		return "contour"
	}

	// Variables a terminal sets for its own children.
	if env("KITTY_WINDOW_ID") != "" {
		return "kitty"
	}
	if env("WT_SESSION") != "" {
		return "windows-terminal" // reaches ced through WSL
	}
	if env("KONSOLE_VERSION") != "" {
		return "konsole"
	}
	if v, err := strconv.Atoi(env("VTE_VERSION")); err == nil && v >= vteMinUnderlineColor {
		return "vte"
	}
	return ""
}
