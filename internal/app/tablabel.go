// =============================================================================
// File: internal/app/tablabel.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-24
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// tablabel.go answers the one question the tab strip could not: WHICH
// main.go is this? Every tab was drawn as its basename, so a Go project
// with three main.go / handler.go / tab.go files open showed three
// identical tabs, and the only way to tell them apart was to click one
// and read the status bar.
//
// The rule is the shortest true answer:
//
//	main.go                     unique on the strip → just the name
//	cmd/main.go   web/main.go   two mains → one directory each
//	a/x/dup.go    b/x/dup.go    still tied at one → take another
//
// Two properties are load-bearing:
//
//   - **Only ambiguous tabs grow.** A label is the basename until some
//     OTHER open tab claims it, which keeps the strip narrow in the
//     common case — the strip is the scarcest horizontal space in the
//     editor (tabbar.go scrolls it), so paying directory columns for
//     files nobody could confuse would cost visible tabs for nothing.
//   - **Extension is per COLLIDING GROUP, not global.** Adding a
//     directory to `cmd/main.go` must not widen `tabbar.go` sitting
//     beside it; the loop re-groups after every round, so a tab drops
//     out of the growth the moment its label is unique.
//
// The cache is validated against the exact list of open PATHS rather than
// invalidated at every mutation site. Tabs are opened, closed, renamed
// and restored from six different files; a flag one of them forgot to set
// would show a stale label with nothing on screen to explain it, whereas
// comparing the paths cannot be wrong. The list is tiny (open tabs), and
// the comparison is what a hit costs.
package app

import (
	"path/filepath"
	"strings"
)

// tabLabel is what tab i is DRAWN as in the strip — its basename when
// nothing else on the strip shares it, otherwise the shortest trailing
// run of directories that tells them apart. Every width measurement and
// every paint in tabbar.go goes through here, so the label and the space
// reserved for it can never disagree.
func (a *App) tabLabel(i int) string {
	if i < 0 || i >= len(a.tabs) {
		return ""
	}
	a.ensureTabLabels()
	if i < len(a.tabLabels) {
		return a.tabLabels[i]
	}
	return a.tabs[i].DisplayName()
}

// ensureTabLabels recomputes the label set when the open paths have moved
// since the last computation. See the file comment for why the cache key
// is the path list itself rather than a dirty flag: a tab's path changes
// on open, close, reorder, rename and save-as, and this is the one test
// that covers all five without any of them knowing about it.
func (a *App) ensureTabLabels() {
	if len(a.tabLabelPaths) == len(a.tabs) {
		same := true
		for i, t := range a.tabs {
			if a.tabLabelPaths[i] != t.Path {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	paths := make([]string, len(a.tabs))
	names := make([]string, len(a.tabs))
	for i, t := range a.tabs {
		paths[i] = t.Path
		names[i] = t.DisplayName()
	}
	a.tabLabelPaths = paths
	a.tabLabels = disambiguateTabLabels(paths, names)
}

// disambiguateTabLabels turns (path, display name) pairs into the labels
// the strip draws. Names are passed in rather than derived from the paths
// because an unsaved tab has no path at all and is displayed as
// "untitled" — it simply has nothing to disambiguate WITH, which the
// saturation check below handles without a special case.
//
// The loop is: group by current label, and give every member of a group
// larger than one its next directory segment. Re-grouping each round is
// what keeps growth confined to tabs that are still tied (see the file
// comment), and a round in which nothing could grow ends it — that is
// also the termination proof for the pathological cases (two untitled
// tabs, the same file open twice).
func disambiguateTabLabels(paths, names []string) []string {
	labels := make([]string, len(names))
	copy(labels, names)

	// dirs[i] is path i's directory segments, root-first, so segment
	// depth d means "the last d of these".
	dirs := make([][]string, len(paths))
	deepest := 0
	for i, p := range paths {
		dirs[i] = pathSegments(p)
		deepest = max(deepest, len(dirs[i]))
	}
	depth := make([]int, len(paths))

	// Bounded by the deepest path: every round adds a segment to at
	// least one tab, and no tab can outgrow its own directory chain.
	for range deepest {
		groups := make(map[string][]int, len(labels))
		for i, l := range labels {
			groups[l] = append(groups[l], i)
		}
		grew := false
		for _, idxs := range groups {
			if len(idxs) < 2 {
				continue
			}
			for _, i := range idxs {
				if depth[i] >= len(dirs[i]) {
					continue // saturated: no directory left to take.
				}
				depth[i]++
				labels[i] = tabLabelWithDepth(names[i], dirs[i], depth[i])
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	return labels
}

// tabLabelWithDepth renders "the last depth directories, then the name".
// Always forward slashes: this is a label, not a path to be reopened, and
// a uniform separator is what makes the strip scan as one column of
// names with prefixes rather than a mix of shapes.
func tabLabelWithDepth(name string, dirs []string, depth int) string {
	if depth <= 0 || len(dirs) == 0 {
		return name
	}
	depth = min(depth, len(dirs))
	var b strings.Builder
	for _, seg := range dirs[len(dirs)-depth:] {
		b.WriteString(seg)
		b.WriteByte('/')
	}
	b.WriteString(name)
	return b.String()
}

// pathSegments returns the DIRECTORY segments of path, root-first and with
// the volume, separators and any "." dropped. An empty or root-level path
// yields none, which is exactly the "nothing to disambiguate with" case.
func pathSegments(path string) []string {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(dir), "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		out = append(out, p)
	}
	return out
}
