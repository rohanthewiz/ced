// =============================================================================
// File: internal/editor/savemeta.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// savemeta.go is what an atomic save must carry over from the file it
// replaces (N-121a). writeFileAtomic writes a temp file and renames it
// over the target, which gives the target a NEW inode — and with it the
// temp's owner, group, mode bits and (lack of) extended attributes. Before
// this, every save silently:
//
//   - reset the group to the directory's / the user's (a shared repo's
//     group-writable file stopped being the team's),
//   - dropped setgid / setuid / sticky (the save copied only the 9
//     permission bits),
//   - dropped xattrs, and with them Linux POSIX ACLs (system.posix_acl_*),
//   - split hard links (the other names kept the old content),
//   - overwrote a 0444 file in a writable directory without a word,
//   - left the rename un-synced in the directory (a power loss could
//     publish the OLD name).
//
// The policy, applied in writeFileAtomic:
//
//	target lacks owner write ─► refuse (*ReadOnlyError); auto-save skips
//	target has nlink > 1 ─────► write IN PLACE (the inode is the point)
//	owner/group can't be set ─► write IN PLACE (the original keeps them)
//	an xattr won't copy ──────► write IN PLACE
//	otherwise ────────────────► temp gets owner, group, full mode and
//	                            xattrs, then rename + directory fsync
//
// In place costs atomicity (see writeInPlace for how it limits the
// damage), so it is taken only when keeping the file's identity requires
// it. macOS ACLs live outside the xattr API and are not carried; Linux
// ACLs are xattrs and are.
package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReadOnlyError is a save refused because the file lacks its owner's
// write permission. Writing it anyway was possible (the temp + rename
// needs only the DIRECTORY's permission), which is exactly why it was a
// silent overwrite of a file someone marked read-only.
type ReadOnlyError struct {
	Path string
	Mode os.FileMode
}

// Error implements error.
func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("%s is read-only (%s) — not saved; make it writable to save", filepath.Base(e.Path), e.Mode.Perm())
}

// IsReadOnlyError reports whether err is (or wraps) a *ReadOnlyError.
func IsReadOnlyError(err error) bool {
	var ro *ReadOnlyError
	return errors.As(err, &ro)
}

// targetMeta is the identity of the file a save replaces.
type targetMeta struct {
	exists   bool
	mode     os.FileMode // permission bits + setuid / setgid / sticky
	uid, gid int
	nlink    uint64
}

// keptModeBits is the part of a mode a save must reproduce.
const keptModeBits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

// readTargetMeta stats path (already symlink-resolved). A missing file is
// exists=false and no error: the save creates it with the defaults.
func readTargetMeta(path string) (targetMeta, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return targetMeta{}, nil
		}
		return targetMeta{}, err
	}
	m := targetMeta{exists: true, mode: info.Mode() & keptModeBits, uid: -1, gid: -1, nlink: 1}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		m.uid, m.gid, m.nlink = int(st.Uid), int(st.Gid), uint64(st.Nlink)
	}
	return m, nil
}

// keepIdentity gives the temp file the target's owner, group and extended
// attributes. An error means the temp can NOT be made equivalent, and the
// caller writes in place instead.
func keepIdentity(tmp *os.File, target string, m targetMeta) error {
	if m.uid >= 0 {
		if st, err := tmp.Stat(); err == nil {
			if ts, ok := st.Sys().(*syscall.Stat_t); ok && (int(ts.Uid) != m.uid || int(ts.Gid) != m.gid) {
				// Usually only the GROUP differs (a temp takes the
				// creator's or the directory's group), and setting it is
				// allowed whenever the user is in that group.
				if err := tmp.Chown(m.uid, m.gid); err != nil {
					return fmt.Errorf("keep owner/group: %w", err)
				}
			}
		}
	}
	return copyXattrs(target, tmp)
}

// copyXattrs copies every extended attribute of src onto the open file
// dst. A filesystem without xattr support has nothing to copy.
func copyXattrs(src string, dst *os.File) error {
	names, err := listXattrs(src)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			return nil
		}
		return fmt.Errorf("list xattrs: %w", err)
	}
	for _, name := range names {
		val, err := getXattr(src, name)
		if err != nil {
			return fmt.Errorf("read xattr %s: %w", name, err)
		}
		if err := unix.Fsetxattr(int(dst.Fd()), name, val, 0); err != nil {
			return fmt.Errorf("copy xattr %s: %w", name, err)
		}
	}
	return nil
}

// listXattrs returns the names of path's extended attributes.
func listXattrs(path string) ([]string, error) {
	sz, err := unix.Listxattr(path, nil)
	if err != nil || sz == 0 {
		return nil, err
	}
	buf := make([]byte, sz)
	sz, err = unix.Listxattr(path, buf)
	if err != nil {
		return nil, err
	}
	var names []string
	start := 0
	for i := 0; i < sz; i++ {
		if buf[i] == 0 {
			if i > start {
				names = append(names, string(buf[start:i]))
			}
			start = i + 1
		}
	}
	return names, nil
}

// getXattr reads one extended attribute's value.
func getXattr(path, name string) ([]byte, error) {
	sz, err := unix.Getxattr(path, name, nil)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, sz)
	sz, err = unix.Getxattr(path, name, buf)
	if err != nil {
		return nil, err
	}
	return buf[:sz], nil
}

// syncDir flushes a directory entry change (the rename) to disk, so a
// power loss after Save returns can't leave the OLD name in place.
// Best-effort: some filesystems refuse fsync on a directory, and the data
// itself is already synced.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
