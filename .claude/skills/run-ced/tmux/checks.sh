#!/bin/sh
# =============================================================================
# File: .claude/skills/run-ced/tmux/checks.sh
# Author: Rohan Allison <rohanthewiz@gmail.com>
# Created: 2026-11-17
# Copyright: 2026 Rohan Allison. All rights reserved.
# =============================================================================
#
# The scripted half of the hand checks that are about TMUX ITSELF (N-139):
# each check drives the real ced binary inside tmux (run.sh) and greps
# tmux's own view of the pane, or the raw bytes tmux sent its terminal.
#
#   checks.sh <scratch-dir>          # every check, PASS / FAIL lines
#
# What only an eye can judge (colours, glyph width) and cats / Terminal.app
# stay hand checks. The validate items each check speaks to are named in
# its label; record results there.
#
# Geometry: 100×30, no tmux status line, a two-file Go project. The tab
# bar's ✕ quit button is at column 32 and ≡ at 36 of row 1 (1-based, as
# SGR mouse reports count) — re-measure if the tree's default width or
# the tab bar lead changes.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
scratch="$1"
proj="$scratch/proj"
rm -rf "$proj" && mkdir -p "$proj"
(cd "$proj" && git init -q &&
  printf 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello (world)")\n}\n' >main.go &&
  git add -A && git -c user.email=h@h -c user.name=h commit -qm init)

# One build for every run below.
HARNESS_BIN="$scratch/bin"
export HARNESS_BIN
rm -rf "$HARNESS_BIN"
"$here/run.sh" "$proj" "$scratch/build" -text -script '200@SNAP;100@{esc}q;300@' >/dev/null 2>&1 || true
[ -x "$HARNESS_BIN/ced" ] || { echo "build failed"; exit 1; }

pass=0
fail=0
OPEN='2500@;300@{esc}p;500@main.go;700@{enter};800@'
QUIT='400@{esc};300@{esc};300@{esc}q;800@'

# check LABEL GREP-MODE PATTERN SCRIPT [conf]
#   GREP-MODE: has | lacks (tmux's pane text) · raw (bytes tmux wrote out)
check() {
  label="$1"; mode="$2"; pat="$3"; script="$4"; conf="${5:-tmux.conf}"
  out="$scratch/out-$(echo "$label" | tr -c 'a-zA-Z0-9' '-')"
  mkdir -p "$out"
  TMUX_CONF="$conf" "$here/run.sh" "$proj" "$out" -text -cols 100 -rows 30 \
    -script "$script" >"$out/screen.txt" 2>"$out/stderr.txt"
  case "$mode" in
  has) grep -q -- "$pat" "$out/screen.txt" ;;
  lacks) ! grep -q -- "$pat" "$out/screen.txt" ;;
  raw) LC_ALL=C grep -q -a -- "$pat" "$out/raw.bin" ;;
  # divider: row 2's first │/┃ (1-based column) is at least PATTERN.
  divider) python3 -I -c 'import sys
row = open(sys.argv[1]).read().split("\n")[1]
cols = [i + 1 for i, c in enumerate(row) if c in "│┃"]
sys.exit(0 if cols and cols[0] >= int(sys.argv[2]) else 1)' "$out/screen.txt" "$pat" ;;
  esac
  if [ $? -eq 0 ]; then
    pass=$((pass + 1)); echo "PASS  $label"
  else
    fail=$((fail + 1)); echo "FAIL  $label   (see $out)"
  fi
}

# N-082 · Esc folded into Alt: "Esc r" sent as one write arrives through
# tmux as Alt+r, which the find bar takes as its regex toggle (not the
# Esc-r Redo leader) — so "(fmt" becomes an invalid regex and says so.
check "N-082 find bar alt+r through tmux toggles regex" has 'missing closing' \
  "$OPEN;{esc}f;400@(fmt;300@{esc}r;900@SNAP;$QUIT"

# N-094 · Alt+x in a find prompt empties the field, and the Esc-x plugin
# leader does not fire (its which-key hint would name "Plugin").
check "N-094 prompt alt+x through tmux clears the field" lacks 'fmtzz' \
  "$OPEN;{esc}F;600@fmtzz;400@{esc}x;900@SNAP;$QUIT"
check "N-094 prompt alt+x does not arm the plugin leader" lacks 'Plugin' \
  "$OPEN;{esc}F;600@fmtzz;400@{esc}x;900@SNAP;$QUIT"

# N-070 · a real click on ✕ through tmux's mouse forwarding opens the
# quit confirm; a click on ≡ beside it opens the menu.
check "N-070 click on the quit button asks first" has 'Close the editor' \
  "$OPEN;{esc}[<0{semi}32{semi}1M;60@{esc}[<0{semi}32{semi}1m;900@SNAP;$QUIT"
check "N-070 click on the menu button opens the menu" has 'Quit editor' \
  "$OPEN;{esc}[<0{semi}36{semi}1M;60@{esc}[<0{semi}36{semi}1m;900@SNAP;$QUIT"

# N-073 · resting the pointer on ≡ (a motion report, button 35) shows its
# label after ~½s — tmux forwards motion only to an app that asked for it.
check "N-073 hover label on the menu button" has 'Action menu' \
  "$OPEN;{esc}[<35{semi}36{semi}1M;1200@SNAP;$QUIT"

# N-090 (drag through tmux) · press on the tree/editor seam, drag right,
# release: the explorer widens, so row 2's divider moves from column 30
# to at least 40.
check "N-090 seam drag through tmux moves the divider" divider 40 \
  "$OPEN;{esc}[<0{semi}30{semi}12M;80@{esc}[<32{semi}38{semi}12M;80@{esc}[<32{semi}46{semi}12M;80@{esc}[<0{semi}46{semi}12m;900@SNAP;$QUIT"

# N-086 / N-078 · OSC 52 through tmux: "Copy reference" (palette) copies
# `<abs path>:<line>`; with the recommended config tmux must hand an
# OSC 52 to ITS terminal (the outer one), which is what reaches the Mac
# clipboard over SSH.
check "N-086 copy reaches the outer terminal (recommended tmux.conf)" raw ']52;c;' \
  "$OPEN;{esc}k;500@Copy reference;600@{enter};1200@SNAP;$QUIT" tmux.conf
check "N-086 copy reaches the outer terminal (untouched tmux)" raw ']52;c;' \
  "$OPEN;{esc}k;500@Copy reference;600@{enter};1200@SNAP;$QUIT" tmux-default.conf

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
