// =============================================================================
// File: .claude/skills/run-ced/capture/pty_darwin.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY allocates a master/slave pty pair and sizes it. Darwin needs
// the TIOCPTY* ioctl dance rather than grantpt(3); a zero-sized pty
// makes tcell draw nothing, which is why the winsize is set on both ends.
func openPTY(cols, rows int) (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(m.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		return nil, nil, fmt.Errorf("grant: %w", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		return nil, nil, fmt.Errorf("unlock: %w", err)
	}
	var buf [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return nil, nil, fmt.Errorf("ptsname: %v", errno)
	}
	end := bytes.IndexByte(buf[:], 0)
	if end < 0 {
		end = len(buf)
	}
	s, err := os.OpenFile(string(buf[:end]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
	_ = unix.IoctlSetWinsize(int(s.Fd()), unix.TIOCSWINSZ, ws)
	return m, s, nil
}
