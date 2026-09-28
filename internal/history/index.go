// =============================================================================
// File: internal/history/index.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The folder usage index: how RECENTLY and how OFTEN each folder of one
// repository has been used, so the "Recent locations" picker can offer the
// last five places beside the ten you actually live in, and drill from any
// of them into the folders used beneath it.
//
// A USE is a sign of work in a folder: a file opened there in a new tab,
// or the folder picked from the picker itself. Tab switching is not a use
// — flipping between two open files is not working in two more places,
// and counting it would let tab churn outvote where files get opened.
//
// RECENCY IS A SEQUENCE NUMBER, NOT A CLOCK. Every use takes the next value
// of one monotonic counter, persisted beside the rows (db.go). Nothing
// shows "2 hours ago", and a counter cannot go backwards across a clock
// change, a timezone, or a laptop that slept through NTP.
//
// THE ALGORITHM — a path trie with SUBTREE BOUNDS, searched best-first.
//
//	<repo> ─┬─ internal ─┬─ app     (self: hits 30, last 811)
//	        │            └─ editor  (self: hits 12, last 790)
//	        └─ ai_docs  ─── plans   (self: hits 2,  last 400)
//
// Each node stores its own stats (`self`) and the MAXIMUM of those stats
// over itself and every descendant (`sub`). The max, not the sum: it is the
// tightest bound that can be maintained in O(depth) on a record, and "no
// folder below here is more recent / more used than X" is exactly the
// question a pruned search asks.
//
//   - Record walks root→leaf once, O(depth): bump the leaf, then raise
//     every ancestor's `sub` to meet it. Nothing is ever re-sorted.
//   - A top-k query is a best-first search with a max-heap keyed on those
//     bounds. Popping a SUBTREE entry expands it into its node's own entry
//     plus one subtree entry per child; popping a SELF entry emits it.
//     Because a subtree's key is an upper bound on everything inside it,
//     the first k self entries popped ARE the top k — the search never has
//     to look inside a subtree whose bound already loses. Cost is roughly
//     O(k · depth · fanout · log heap), independent of how many folders
//     are tracked; "subfolders of X" costs one walk to X and then the same
//     search rooted there.
//   - Frequency ties break toward the more recent folder. The bound pair
//     (maxHits, maxLast) is compared lexicographically and is still a
//     valid upper bound: if the max hit count equals a descendant's, the
//     max `last` over the same subtree is at least that descendant's.
//
// SIZE IS BOUNDED BY EVICTION WITH HYSTERESIS. Past MaxFolders the index
// forgets folders down to 90% of the cap in one pass — so the O(n log n)
// sort and the O(n) re-aggregation run once per few dozen new folders
// rather than on every one. The most recent quarter is PROTECTED (a folder
// used a moment ago must not vanish from the recent list for having only
// one hit); among the rest the least-used go first, oldest breaking ties.
//
// THE TRIE IS A RUNTIME INDEX, NOT A FILE FORMAT. The database holds one
// flat row per used folder and Load rebuilds the trie in O(total path
// segments). Because more than one ced can be open on a repository, the
// index also remembers what CHANGED since it was loaded — hits to add,
// rows to delete — so a write adds this instance's uses to whatever the
// others recorded instead of overwriting them. That bookkeeping is `delta`
// on a node, the `dirty` set and the `deletes` list; nothing else here
// knows about persistence.

package history

import (
	"container/heap"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rohanthewiz/ced/internal/session"
)

// MaxFolders caps how many folders the index remembers. Every file open
// can add one; the lists only ever show fifteen at a level, and four
// hundred is every folder a large repository has anyone working in.
const MaxFolders = 400

// useStat is a folder's counters: how many uses, and the sequence number
// of the latest. In `sub` both fields are maxima over the subtree.
type useStat struct {
	hits uint64
	last uint64
}

// beats reports whether s outranks o under the given ordering. Recency
// compares `last` alone (sequence numbers are unique per use); frequency
// compares hits, then recency. Shared by the heap and the tests so the two
// cannot disagree about what "top" means.
func (s useStat) beats(o useStat, byFreq bool) bool {
	if byFreq && s.hits != o.hits {
		return s.hits > o.hits
	}
	return s.last > o.last
}

// maxStat is the componentwise max — the aggregate a subtree keeps.
func maxStat(a, b useStat) useStat {
	if b.hits > a.hits {
		a.hits = b.hits
	}
	if b.last > a.last {
		a.last = b.last
	}
	return a
}

// useNode is one path segment in the trie.
type useNode struct {
	name     string
	parent   *useNode
	children map[string]*useNode
	self     useStat
	sub      useStat
	// delta is the hits recorded since the last write — what the next
	// write ADDS to the stored row.
	delta uint64
}

// used reports whether the folder itself (not just something under it)
// has been used.
func (n *useNode) used() bool { return n.self.hits > 0 }

// recompute rebuilds n's subtree bound from its own stats and its
// children's bounds. O(children) — used when something was REMOVED, since
// a max cannot be lowered incrementally.
func (n *useNode) recompute() {
	n.sub = n.self
	for _, c := range n.children {
		n.sub = maxStat(n.sub, c.sub)
	}
}

// Index is the folder usage index. The zero value is not ready; use
// NewIndex.
type Index struct {
	root *useNode
	seq  uint64
	// base is the database's sequence counter as of the last load or
	// write. Every `last` above it was minted by this instance and is
	// re-issued from the database's counter when written (db.go).
	base uint64
	// count is how many nodes are used(), maintained on every path so the
	// eviction check is O(1) per record.
	count int

	// dirty holds the nodes recorded since the last write.
	dirty map[*useNode]bool
	// deletes are the rows the next write must remove, in the order they
	// were forgotten. Written BEFORE the upserts, so a folder forgotten and
	// then used again in one session comes back with its new hits.
	deletes []deletion
}

// deletion is one pending row removal: a pruned folder takes its whole
// subtree, an evicted one only itself (its used subfolders may well be
// why they survived).
type deletion struct {
	path    string
	subtree bool
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{root: &useNode{}, dirty: map[*useNode]bool{}}
}

// Len is how many folders the index remembers.
func (u *Index) Len() int { return u.count }

// Dirty reports whether anything is waiting to be written.
func (u *Index) Dirty() bool { return len(u.dirty) > 0 || len(u.deletes) > 0 }

// splitPath breaks an absolute, cleaned path into its segments. The
// filesystem root yields none, which addresses the trie's root node.
// Relative paths are refused (nil, false): every key in the index is
// absolute, and a relative one would silently hang off the wrong node.
func splitPath(p string) ([]string, bool) {
	if p == "" || !filepath.IsAbs(p) {
		return nil, false
	}
	p = filepath.Clean(p)
	trimmed := strings.Trim(p, string(filepath.Separator))
	if trimmed == "" {
		return nil, true
	}
	return strings.Split(trimmed, string(filepath.Separator)), true
}

// nodePath rebuilds a node's absolute path by walking to the root. Only
// eviction and the database write need it; the query builds paths as it
// descends instead.
func nodePath(n *useNode) string {
	var segs []string
	for ; n != nil && n.parent != nil; n = n.parent {
		segs = append(segs, n.name)
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return string(filepath.Separator) + filepath.Join(segs...)
}

// find returns the node for path, or nil when the index has never seen it.
func (u *Index) find(path string) *useNode {
	segs, ok := splitPath(path)
	if !ok {
		return nil
	}
	n := u.root
	for _, s := range segs {
		n = n.children[s]
		if n == nil {
			return nil
		}
	}
	return n
}

// Record notes one use of the folder at path. The path is normalized
// (session.Normalize — the editor's one spelling of a folder) so a
// symlinked spelling cannot split a folder's history in two.
func (u *Index) Record(path string) {
	u.record(session.Normalize(path), 1, 0, true)
}

// record is Record's core, with the stats given rather than implied, so a
// load can replay a stored row through the same path. hits is added;
// last == 0 means "take the next sequence number". track says the hits are
// NEW — to be added to the database by the next write — which is true of
// every use and false of a row being loaded.
func (u *Index) record(path string, hits, last uint64, track bool) {
	segs, ok := splitPath(path)
	if !ok || hits == 0 {
		return
	}
	if last == 0 {
		u.seq++
		last = u.seq
	} else if last > u.seq {
		u.seq = last
	}
	n := u.root
	for _, s := range segs {
		c := n.children[s]
		if c == nil {
			c = &useNode{name: s, parent: n}
			if n.children == nil {
				n.children = map[string]*useNode{}
			}
			n.children[s] = c
		}
		n = c
	}
	if !n.used() {
		u.count++
	}
	n.self.hits += hits
	if last > n.self.last {
		n.self.last = last
	}
	if track {
		n.delta += hits
		u.dirty[n] = true
	}
	// Raise every ancestor's bound to meet the leaf. A max only grows on a
	// record, so this walk is all the maintenance a record needs.
	for p := n; p != nil; p = p.parent {
		p.sub = maxStat(p.sub, n.self)
	}
	if u.count > MaxFolders {
		u.evict(MaxFolders * 9 / 10)
	}
}

// Remove forgets path AND everything under it — how a folder that no
// longer exists is pruned, since its subfolders went with it. Ancestors'
// bounds are recomputed on the way up, and ancestors left holding nothing
// are dropped.
func (u *Index) Remove(path string) {
	path = session.Normalize(path)
	n := u.find(path)
	if n == nil {
		return
	}
	u.forgetDirty(n)
	u.deletes = append(u.deletes, deletion{path: path, subtree: true})
	if n.parent == nil {
		// Forgetting "/" is forgetting everything.
		u.root, u.count = &useNode{}, 0
		return
	}
	u.count -= countUsed(n)
	parent := n.parent
	delete(parent.children, n.name)
	u.repairUp(parent)
}

// repairUp recomputes bounds from n to the root, pruning nodes that no
// longer carry anything (unused, childless) as it goes.
func (u *Index) repairUp(n *useNode) {
	for n != nil {
		up := n.parent
		if up != nil && !n.used() && len(n.children) == 0 {
			delete(up.children, n.name)
		} else {
			n.recompute()
		}
		n = up
	}
}

// forgetDirty drops every node in n's subtree from the dirty set — their
// rows are being deleted, and a pending upsert would resurrect them.
func (u *Index) forgetDirty(n *useNode) {
	delete(u.dirty, n)
	for _, c := range n.children {
		u.forgetDirty(c)
	}
}

// countUsed counts used nodes in n's subtree, n included.
func countUsed(n *useNode) int {
	c := 0
	if n.used() {
		c++
	}
	for _, ch := range n.children {
		c += countUsed(ch)
	}
	return c
}

// evict forgets folders until at most target remain — see the header for
// the policy. It clears the victims' own stats and then rebuilds every
// bound in one post-order pass, which is cheaper than repairing each
// victim's ancestry separately.
func (u *Index) evict(target int) {
	var all []*useNode
	var walk func(*useNode)
	walk = func(n *useNode) {
		if n.used() {
			all = append(all, n)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(u.root)
	if len(all) <= target {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].self.last > all[j].self.last })
	protected := min(MaxFolders/4, target)
	rest := all[protected:]
	sort.Slice(rest, func(i, j int) bool {
		a, b := rest[i].self, rest[j].self
		if a.hits != b.hits {
			return a.hits < b.hits
		}
		return a.last < b.last
	})
	drop := len(all) - target
	for _, n := range rest[:drop] {
		u.deletes = append(u.deletes, deletion{path: nodePath(n)})
		delete(u.dirty, n)
		n.self, n.delta = useStat{}, 0
	}
	u.count = target
	u.rebuild(u.root)
}

// rebuild recomputes every bound under n bottom-up and prunes empty
// leaves. Reports whether n itself should be kept.
func (u *Index) rebuild(n *useNode) bool {
	for name, c := range n.children {
		if !u.rebuild(c) {
			delete(n.children, name)
		}
	}
	n.recompute()
	return n.used() || len(n.children) > 0
}

// HasUsesBelow reports whether any folder strictly below path has been
// used. O(children of path) thanks to the bounds — it decides whether
// picking a folder drills in or reveals it.
func (u *Index) HasUsesBelow(path string) bool {
	n := u.find(session.Normalize(path))
	if n == nil {
		return false
	}
	for _, c := range n.children {
		if c.sub.hits > 0 {
			return true
		}
	}
	return false
}

// Recent returns up to k folders STRICTLY below under, most recent first.
// accept, when non-nil, can veto a candidate (a folder already listed, one
// that no longer exists) and the search keeps going past it, so a veto
// never shortens the list.
func (u *Index) Recent(under string, k int, accept func(string) bool) []string {
	return u.top(under, false, k, accept)
}

// Frequent is Recent ranked by use count, recency breaking ties.
func (u *Index) Frequent(under string, k int, accept func(string) bool) []string {
	return u.top(under, true, k, accept)
}

// useEntry is one heap item: a node's own stats (self) or its subtree's
// bound. path is built on the way down so the search never walks back up.
type useEntry struct {
	n    *useNode
	path string
	key  useStat
	self bool
}

// useHeap is a max-heap of entries under one ordering.
type useHeap struct {
	items  []useEntry
	byFreq bool
}

func (h *useHeap) Len() int { return len(h.items) }
func (h *useHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.key.beats(b.key, h.byFreq) {
		return true
	}
	if b.key.beats(a.key, h.byFreq) {
		return false
	}
	// Equal keys: a self entry first — the bound it ties with may be that
	// very node, and emitting it now saves an expansion.
	return a.self && !b.self
}
func (h *useHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *useHeap) Push(x any)    { h.items = append(h.items, x.(useEntry)) }
func (h *useHeap) Pop() any {
	old := h.items
	e := old[len(old)-1]
	h.items = old[:len(old)-1]
	return e
}

// top is the best-first search both rankings share — see the header. The
// start folder itself is never a candidate: "subfolders of X" must not
// list X.
func (u *Index) top(under string, byFreq bool, k int, accept func(string) bool) []string {
	if k <= 0 {
		return nil
	}
	startPath := session.Normalize(under)
	start := u.find(startPath)
	if start == nil {
		return nil
	}
	h := &useHeap{byFreq: byFreq}
	for name, c := range start.children {
		if c.sub.hits > 0 {
			h.items = append(h.items, useEntry{n: c, path: filepath.Join(startPath, name), key: c.sub})
		}
	}
	heap.Init(h)
	out := make([]string, 0, k)
	for h.Len() > 0 && len(out) < k {
		e := heap.Pop(h).(useEntry)
		if e.self {
			if accept == nil || accept(e.path) {
				out = append(out, e.path)
			}
			continue
		}
		if e.n.self.hits > 0 {
			heap.Push(h, useEntry{n: e.n, path: e.path, key: e.n.self, self: true})
		}
		for name, c := range e.n.children {
			if c.sub.hits > 0 {
				heap.Push(h, useEntry{n: c, path: filepath.Join(e.path, name), key: c.sub})
			}
		}
	}
	return out
}
