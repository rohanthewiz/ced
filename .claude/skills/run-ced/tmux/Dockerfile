# =============================================================================
# File: .claude/skills/run-ced/tmux/Dockerfile
# Author: Rohan Allison <rohanthewiz@gmail.com>
# Created: 2026-11-17
# Copyright: 2026 Rohan Allison. All rights reserved.
# =============================================================================
#
# A Linux box with tmux, for running the capture driver THROUGH tmux
# (N-139). The dev Mac has no tmux, and ced is written for SSH-into-tmux,
# so the checks about tmux itself (Esc folded into Alt, OSC 52
# passthrough, mouse forwarding) run here. Only tmux is installed: ced and
# the driver are cross-compiled on the host and mounted in (run.sh), so
# the image never needs Go modules or the network after this build.
FROM golang:1.26.1
RUN apt-get update -qq && apt-get install -y -qq tmux && rm -rf /var/lib/apt/lists/*
