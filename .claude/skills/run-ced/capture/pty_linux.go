// =============================================================================
// File: .claude/skills/run-ced/capture/pty_linux.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// openPTY allocates a master/slave pty pair and sizes it — the Linux
// spelling (unlock with TIOCSPTLCK, name from TIOCGPTN), so the driver
// also runs inside a Linux container, e.g. under tmux (see ../tmux).
func openPTY(cols, rows int) (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(m.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return nil, nil, fmt.Errorf("unlock: %w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		return nil, nil, fmt.Errorf("ptsname: %w", err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
	_ = unix.IoctlSetWinsize(int(s.Fd()), unix.TIOCSWINSZ, ws)
	return m, s, nil
}
