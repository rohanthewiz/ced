#!/bin/sh
# =============================================================================
# File: .claude/skills/run-ced/tmux/run.sh
# Author: Rohan Allison <rohanthewiz@gmail.com>
# Created: 2026-11-17
# Copyright: 2026 Rohan Allison. All rights reserved.
# =============================================================================
#
# Run the capture driver against ced INSIDE tmux, in a Linux container
# (N-139).
#
#   run.sh <project-dir> <out-dir> [capture flags…]
#
# Cross-compiles ced and the driver for the container's arch, builds the
# tmux image once (ced-tmux-harness), and runs:
#
#   capture -bin ced-in-tmux -dir /work \
#           -snapcmd 'tmux -L ced capture-pane -p' -raw /out/raw.bin …
#
# so -text prints tmux's own view of the pane at SNAP, and <out-dir>/raw.bin
# holds every byte tmux sent to its "terminal" (the driver) — where an
# OSC 52 copy shows up if tmux forwarded it. TMUX_CONF=tmux-default.conf
# runs an untouched tmux instead of the recommended settings.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../../../.." && pwd)"
proj="$(cd "$1" && pwd)"; shift
out="$(mkdir -p "$1" && cd "$1" && pwd)"; shift
arch="$(docker info --format '{{.Architecture}}' 2>/dev/null || uname -m)"
case "$arch" in aarch64|arm64) goarch=arm64 ;; *) goarch=amd64 ;; esac

# HARNESS_BIN names a directory already holding linux ced + capture
# (checks.sh builds once for all its runs); otherwise build into <out>/bin.
bin="${HARNESS_BIN:-$out/bin}"
if [ -z "${HARNESS_BIN:-}" ] || [ ! -x "$bin/ced" ] || [ ! -x "$bin/capture" ]; then
  mkdir -p "$bin"
  (cd "$repo" && CGO_ENABLED=0 GOOS=linux GOARCH=$goarch go build -o "$bin/ced" .)
  (cd "$here/../capture" && CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOFLAGS=-mod=mod GOPROXY=off go build -o "$bin/capture" .)
fi

docker image inspect ced-tmux-harness >/dev/null 2>&1 ||
  docker build -q -t ced-tmux-harness "$here" >/dev/null

conf="/harness/${TMUX_CONF:-tmux.conf}"
docker run --rm \
  -e TMUX_CONF="$conf" \
  -v "$here:/harness:ro" -v "$bin:/hbin:ro" \
  -v "$proj:/work" -v "$out:/out" \
  ced-tmux-harness \
  /hbin/capture -bin /harness/ced-in-tmux -dir /work \
    -snapcmd 'tmux -L ced capture-pane -p' -raw /out/raw.bin "$@"
