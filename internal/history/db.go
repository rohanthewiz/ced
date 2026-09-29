// =============================================================================
// File: internal/history/db.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Persistence: a bytdb database at <repo>/.ced/history.bytdb, reached
// through bytdb's database/sql driver.
//
// OPEN, DO THE WORK, CLOSE. bytdb holds an exclusive lock on its file for
// as long as an engine is open ("one engine per file, enforced"), and two
// ced windows on one repository are ordinary. Holding the database for an
// editor's lifetime would lock every sibling out for that lifetime, so the
// file is opened for two moments only — the load at startup and each
// write — and a contended open is retried briefly (a sibling's write is
// milliseconds long) before giving up. Giving up costs nothing permanent:
// the pending changes stay in memory and the next write carries them.
//
//	ced A ──open─load─close────────────── … ──open─write(+Δ)─close──
//	ced B ─────────── open─load─close ─ … ─ open─write(+Δ)─close ──
//
// A WRITE ADDS, IT NEVER OVERWRITES. Each instance writes the DELTA since
// its load: folder hits are added to the stored rows, and the order of
// what it touched is re-issued from the database's own counters inside
// the transaction. Absolute writes would make two sessions on one repo a
// race in which the last to close erases the other's afternoon.
//
// Sequence re-issue, concretely: a load remembers the stored counter
// (`base`). Every folder `last` above it was minted by this instance, so
// the write sorts those values and maps them onto G+1…G+m, where G is the
// counter read inside the transaction, then stores G+m. Recent files do
// the same with the ring's order: the touched entries, oldest first, get
// G'+1…G'+n. Order within the instance survives, and it all lands after
// what was stored — which is true: it happened after the load.
//
// Loading never CREATES anything. Opening ced on a folder must not leave
// a .ced/ behind by itself; the directory, the database and the ignore
// entry appear with the first write that has something to say.
//
// COMPACTION IS OURS TO ASK FOR. bytdb's storage is an append-only log:
// every UPDATE and DELETE leaves the old record behind until a compaction
// rewrites the file. btypedb compacts on its own only past 32MB, and only
// from a long-lived engine — neither ever happens here. The live set is
// capped (MaxFolders rows + MaxRecentFiles rows ≈ 45KB at worst, measured
// with deep paths; the three search lists add at most 3 × MaxSearches ×
// MaxSearchBytes ≈ 38KB more, and typically a few hundred bytes) while each write appends ~2.6KB, so left alone the file
// would grow by the session forever. A write therefore ends with a VACUUM
// once the file passes compactAbove:
//
//	size ─╱╲──╱╲──╱╲──   saw-tooth between the live set and the ceiling
//
// A fixed ceiling rather than btypedb's growth ratio because the live set
// is bounded by the caps: the ceiling sits well above the largest live set
// the caps allow, so a compaction always has something to reclaim and runs
// once per ~80 sessions, never on every write. It happens inside the
// write's own open, so it holds the lock no longer than a few ms of extra
// rewrite of a sub-megabyte file.
//
// Schema:
//
//	folder_use(path PK, hits, last)  — path relative to the repo
//	recent_files(path PK, last)      — relative inside, absolute outside
//	search_history(k PK, last)       — k = "<kind>:<text>" (searches.go)
//	history_meta(k PK, v)            — 'folder_seq', 'file_seq', 'search_seq'
//
// Searches follow the recent-file recipe exactly: touched entries are
// re-stamped from their counter in list order, oldest first, and each
// kind is trimmed to its newest MaxSearches after the merge.

package history

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/bytdb"
	_ "github.com/rohanthewiz/bytdb/stdlib" // registers the "bytdb" driver
)

// DBName is the database's file name inside <repo>/.ced/.
const DBName = "history.bytdb"

// ignoreLine is the .ced/.gitignore entry covering the database and its
// lock sidecar.
const ignoreLine = DBName + "*"

// lockRetries and lockBackoff bound how long an open waits out another
// instance's write: 10 × 25ms. A write is a handful of rows, so a lock held
// longer than that is a sibling doing something else, and the caller is
// better served by a deferred write than by a frozen editor.
const (
	lockRetries = 10
	lockBackoff = 25 * time.Millisecond
)

// The history_meta keys.
const (
	folderSeqKey = "folder_seq"
	fileSeqKey   = "file_seq"
	searchSeqKey = "search_seq"
)

// compactAbove is the file size past which a write ends with a VACUUM —
// ~6× the largest live set the caps allow (see the header). A variable so
// tests can bring the ceiling within reach of a few writes.
var compactAbove int64 = 256 << 10

// schema is applied on every open. DDL runs outside a transaction (a bytdb
// rule), and IF NOT EXISTS makes it a no-op after the first run.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS folder_use (
		path TEXT PRIMARY KEY,
		hits BIGINT NOT NULL,
		last BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS recent_files (
		path TEXT PRIMARY KEY,
		last BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS search_history (
		k TEXT PRIMARY KEY,
		last BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS history_meta (
		k TEXT PRIMARY KEY,
		v BIGINT NOT NULL
	)`,
}

// DBPath is where a repository's history lives.
func DBPath(root string) string {
	return filepath.Join(root, ".ced", DBName)
}

// open opens the database at path, creating its directory and schema,
// retrying while another process holds the lock.
func open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	var db *sql.DB
	var err error
	for i := 0; i < lockRetries; i++ {
		// sql.Open reaches the driver's OpenConnector at once, which is
		// where bytdb takes the file lock — so a contended open fails here,
		// not on the first query.
		db, err = sql.Open("bytdb", path)
		if err == nil || !errors.Is(err, bytdb.ErrLocked) {
			break
		}
		time.Sleep(lockBackoff)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("schema %s: %w", path, err)
		}
	}
	return db, nil
}

// readSeq reads one history_meta counter (0 when absent).
func readSeq(q interface {
	QueryRow(string, ...any) *sql.Row
}, key string) (uint64, error) {
	var v int64
	err := q.QueryRow(`SELECT v FROM history_meta WHERE k = $1`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil || v < 0 {
		return 0, fmt.Errorf("read %s: %w", key, err)
	}
	return uint64(v), nil
}

// Load reads the history for the repository at root from dbPath. A missing
// file is an empty history, not an error — and is not created. On an
// error the history is still returned, empty and usable: history is
// convenience, the editor must start regardless, and changes recorded
// meanwhile are added to the database by a later write.
//
// Rows that could only come from outside ced — a folder path that is
// absolute or climbs out of the repo, zero hits — are skipped rather than
// failing the load.
func Load(root, dbPath string) (*History, error) {
	h := New(root)
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	db, err := open(dbPath)
	if err != nil {
		return h, err
	}
	defer db.Close()
	if err := h.load(db); err != nil {
		return New(root), err
	}
	return h, nil
}

// load is Load's body against an open database.
func (h *History) load(db *sql.DB) error {
	u := h.Folders
	seq, err := readSeq(db, folderSeqKey)
	if err != nil {
		return err
	}
	rows, err := db.Query(`SELECT path, hits, last FROM folder_use`)
	if err != nil {
		return fmt.Errorf("read folders: %w", err)
	}
	for rows.Next() {
		var p string
		var hits, last int64
		if err := rows.Scan(&p, &hits, &last); err != nil {
			rows.Close()
			return fmt.Errorf("read folder row: %w", err)
		}
		abs, ok := h.decodeFolder(p)
		if !ok || hits <= 0 || last < 0 {
			continue
		}
		u.record(abs, uint64(hits), uint64(last), false)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read folders: %w", err)
	}
	u.seq = max(u.seq, seq)
	u.base = u.seq
	// Several instances each adding folders can leave more rows than one
	// index keeps; trimming here queues the deletes for the next write.
	if u.count > MaxFolders {
		u.evict(MaxFolders * 9 / 10)
	}

	if h.fileSeq, err = readSeq(db, fileSeqKey); err != nil {
		return err
	}
	frows, err := db.Query(`SELECT path, last FROM recent_files`)
	if err != nil {
		return fmt.Errorf("read recent files: %w", err)
	}
	type fileRow struct {
		path string
		last int64
	}
	var files []fileRow
	for frows.Next() {
		var r fileRow
		if err := frows.Scan(&r.path, &r.last); err != nil {
			frows.Close()
			return fmt.Errorf("read recent file row: %w", err)
		}
		if r.path != "" {
			files = append(files, r)
		}
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return fmt.Errorf("read recent files: %w", err)
	}
	// Sorted here rather than by ORDER BY so the path tie-break makes two
	// loads of one database agree even if two rows share a number.
	sort.Slice(files, func(i, j int) bool {
		if files[i].last != files[j].last {
			return files[i].last > files[j].last
		}
		return files[i].path < files[j].path
	})
	for i, r := range files {
		if i >= MaxRecentFiles {
			break
		}
		h.files = append(h.files, h.decodeFile(r.path))
	}
	return h.loadSearches(db)
}

// loadSearches reads the search lists (searches.go).
func (h *History) loadSearches(db *sql.DB) error {
	var err error
	if h.searchSeq, err = readSeq(db, searchSeqKey); err != nil {
		return err
	}
	rows, err := readSearchRows(db)
	if err != nil {
		return err
	}
	h.loadSearchRows(rows)
	return nil
}

// readSearchRows reads every stored search row, unordered.
func readSearchRows(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]searchRow, error) {
	rows, err := q.Query(`SELECT k, last FROM search_history`)
	if err != nil {
		return nil, fmt.Errorf("read searches: %w", err)
	}
	defer rows.Close()
	var out []searchRow
	for rows.Next() {
		var r searchRow
		if err := rows.Scan(&r.key, &r.last); err != nil {
			return nil, fmt.Errorf("read search row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read searches: %w", err)
	}
	return out, nil
}

// Write adds the pending changes to the database at dbPath in one
// transaction — see the header for why it adds rather than overwrites.
// ring is the caller's live recent-file ring, most recent first: it is
// what orders the touched files. A no-op when nothing is pending. On
// success the pending set is cleared; on failure nothing is, so the next
// Write retries the lot.
func (h *History) Write(dbPath string, ring []string) error {
	if !h.Dirty() {
		return nil
	}
	db, err := open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	ensureIgnored(filepath.Dir(dbPath))

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	remap, folderTop, err := h.writeFolders(tx)
	if err == nil {
		err = h.writeFiles(tx, ring)
	}
	if err == nil {
		err = h.writeSearches(tx)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	h.Folders.adoptWrite(remap, folderTop)
	h.touched = map[string]bool{}
	h.removed = map[string]bool{}
	h.searchTouched = map[string]bool{}
	h.searchRemoved = map[string]bool{}
	compactIfLarge(db, dbPath)
	return nil
}

// compactIfLarge VACUUMs the database once its file has outgrown
// compactAbove, reclaiming the records overwritten and deleted by earlier
// writes. Best-effort: the write it follows has already committed, a
// failed compaction leaves the old log intact (btypedb swaps files by
// rename), and the next write past the ceiling tries again.
func compactIfLarge(db *sql.DB, dbPath string) {
	fi, err := os.Stat(dbPath)
	if err != nil || fi.Size() <= compactAbove {
		return
	}
	// VACUUM refuses to run inside a transaction, so this must follow the
	// commit rather than join it.
	_, _ = db.Exec(`VACUUM`)
}

// writeFolders writes the folder index's pending changes. It returns the
// local→stored sequence mapping and the new counter, applied to memory
// only once the commit has landed.
func (h *History) writeFolders(tx *sql.Tx) (map[uint64]uint64, uint64, error) {
	u := h.Folders
	g, err := readSeq(tx, folderSeqKey)
	if err != nil {
		return nil, 0, err
	}

	// Deletes first: a folder forgotten and then used again in the same
	// session must come back with its new hits, not be erased by its own
	// earlier removal.
	for _, d := range u.deletes {
		rel, ok := h.encodeFolder(d.path)
		if !ok {
			continue
		}
		if d.subtree {
			// Everything under the folder: "p/…" sorts between "p/" and
			// "p0", because '0' is the byte after the separator '/'.
			_, err = tx.Exec(`DELETE FROM folder_use WHERE path = $1 OR (path > $2 AND path < $3)`,
				rel, rel+string(filepath.Separator), rel+string(rune(filepath.Separator)+1))
		} else {
			_, err = tx.Exec(`DELETE FROM folder_use WHERE path = $1`, rel)
		}
		if err != nil {
			return nil, 0, fmt.Errorf("delete %s: %w", rel, err)
		}
	}

	// Re-issue every sequence number this instance minted (above base),
	// in order, from the stored counter.
	var minted []uint64
	for n := range u.dirty {
		if l := n.self.last; l > u.base {
			minted = append(minted, l)
		}
	}
	sort.Slice(minted, func(i, j int) bool { return minted[i] < minted[j] })
	remap := make(map[uint64]uint64, len(minted))
	for i, l := range minted {
		remap[l] = g + uint64(i) + 1
	}

	// Sorted for a deterministic statement order; map iteration would
	// make two identical writes differ.
	type pending struct {
		n   *useNode
		rel string
	}
	var rows []pending
	for n := range u.dirty {
		if rel, ok := h.encodeFolder(nodePath(n)); ok {
			rows = append(rows, pending{n, rel})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].rel < rows[j].rel })

	for _, r := range rows {
		last := r.n.self.last
		if m, ok := remap[last]; ok {
			last = m
		}
		var hits, stored int64
		err := tx.QueryRow(`SELECT hits, last FROM folder_use WHERE path = $1`, r.rel).Scan(&hits, &stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// No row: a folder new to the database, or one another
			// instance pruned. Either way this instance's own totals are
			// the best record there is.
			_, err = tx.Exec(`INSERT INTO folder_use (path, hits, last) VALUES ($1, $2, $3)`,
				r.rel, int64(r.n.self.hits), int64(last))
		case err != nil:
		default:
			_, err = tx.Exec(`UPDATE folder_use SET hits = $2, last = $3 WHERE path = $1`,
				r.rel, hits+int64(r.n.delta), max(stored, int64(last)))
		}
		if err != nil {
			return nil, 0, fmt.Errorf("write folder %s: %w", r.rel, err)
		}
	}

	top := g + uint64(len(minted))
	if err := writeSeq(tx, folderSeqKey, top); err != nil {
		return nil, 0, err
	}
	return remap, top, nil
}

// writeFiles writes the recent-file ring's changes: pruned rows deleted,
// touched rows stamped in ring order from the stored counter, and the
// table trimmed to MaxRecentFiles newest — the trim is what makes the
// MERGED list (this instance's touches among a sibling's) the cap's length
// rather than each instance's.
func (h *History) writeFiles(tx *sql.Tx, ring []string) error {
	if len(h.touched) == 0 && len(h.removed) == 0 {
		return nil
	}
	for p := range h.removed {
		if _, err := tx.Exec(`DELETE FROM recent_files WHERE path = $1`, h.encodeFile(p)); err != nil {
			return fmt.Errorf("delete recent file: %w", err)
		}
	}
	g, err := readSeq(tx, fileSeqKey)
	if err != nil {
		return err
	}
	// Oldest touched first, so the head of the ring gets the highest
	// number. A touched file no longer in the ring fell off its cap and
	// has nothing to say.
	var stamped uint64
	for i := len(ring) - 1; i >= 0; i-- {
		p := ring[i]
		if !h.touched[p] {
			continue
		}
		stamped++
		stored := h.encodeFile(p)
		res, err := tx.Exec(`UPDATE recent_files SET last = $2 WHERE path = $1`, stored, int64(g+stamped))
		if err != nil {
			return fmt.Errorf("write recent file: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.Exec(`INSERT INTO recent_files (path, last) VALUES ($1, $2)`, stored, int64(g+stamped)); err != nil {
				return fmt.Errorf("write recent file: %w", err)
			}
		}
	}
	if err := writeSeq(tx, fileSeqKey, g+stamped); err != nil {
		return err
	}
	return trimFiles(tx)
}

// trimFiles deletes every recent-file row past the newest MaxRecentFiles.
func trimFiles(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT path, last FROM recent_files`)
	if err != nil {
		return fmt.Errorf("trim recent files: %w", err)
	}
	type row struct {
		path string
		last int64
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.path, &r.last); err != nil {
			rows.Close()
			return fmt.Errorf("trim recent files: %w", err)
		}
		all = append(all, r)
	}
	rows.Close()
	if len(all) <= MaxRecentFiles {
		return rows.Err()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].last > all[j].last })
	for _, r := range all[MaxRecentFiles:] {
		if _, err := tx.Exec(`DELETE FROM recent_files WHERE path = $1`, r.path); err != nil {
			return fmt.Errorf("trim recent files: %w", err)
		}
	}
	return nil
}

// writeSearches writes the search lists' changes, the writeFiles recipe
// per kind: forgotten rows deleted, touched rows stamped in list order
// (oldest first, so each list's head gets the highest number), and every
// kind trimmed to its newest MaxSearches once this instance's rows have
// merged with whatever a sibling stored.
func (h *History) writeSearches(tx *sql.Tx) error {
	if !h.searchesDirty() {
		return nil
	}
	// Sorted so two identical writes issue identical statements.
	removed := make([]string, 0, len(h.searchRemoved))
	for k := range h.searchRemoved {
		removed = append(removed, k)
	}
	sort.Strings(removed)
	for _, k := range removed {
		if _, err := tx.Exec(`DELETE FROM search_history WHERE k = $1`, k); err != nil {
			return fmt.Errorf("delete search: %w", err)
		}
	}
	g, err := readSeq(tx, searchSeqKey)
	if err != nil {
		return err
	}
	var stamped uint64
	for _, kind := range h.searchKinds() {
		list := h.searches[kind]
		for i := len(list) - 1; i >= 0; i-- {
			k := searchKey(kind, list[i])
			if !h.searchTouched[k] {
				continue
			}
			stamped++
			if _, err := tx.Exec(`INSERT INTO search_history (k, last) VALUES ($1, $2) ON CONFLICT (k) DO UPDATE SET last = excluded.last`,
				k, int64(g+stamped)); err != nil {
				return fmt.Errorf("write search: %w", err)
			}
		}
	}
	if err := writeSeq(tx, searchSeqKey, g+stamped); err != nil {
		return err
	}
	rows, err := readSearchRows(tx)
	if err != nil {
		return err
	}
	for _, k := range overflowSearchKeys(rows) {
		if _, err := tx.Exec(`DELETE FROM search_history WHERE k = $1`, k); err != nil {
			return fmt.Errorf("trim searches: %w", err)
		}
	}
	return nil
}

// writeSeq upserts one history_meta counter.
func writeSeq(tx *sql.Tx, key string, v uint64) error {
	_, err := tx.Exec(`INSERT INTO history_meta (k, v) VALUES ($1, $2) ON CONFLICT (k) DO UPDATE SET v = excluded.v`,
		key, int64(v))
	if err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}

// adoptWrite brings the index in line with a committed write: re-issued
// sequence numbers replace the local ones, bounds are rebuilt (a max can
// only be recomputed, not patched), and the pending set is emptied.
func (u *Index) adoptWrite(remap map[uint64]uint64, top uint64) {
	for n := range u.dirty {
		if m, ok := remap[n.self.last]; ok {
			n.self.last = m
		}
		n.delta = 0
	}
	u.rebuild(u.root)
	u.seq, u.base = top, top
	u.dirty = map[*useNode]bool{}
	u.deletes = nil
}

// ensureIgnored makes sure dir/.gitignore ignores the database, creating
// the file or appending the one line. Best-effort: a repository whose
// .ced/ cannot be written to could not have held the database either, and
// a failure here must not cost the write that already succeeded.
func ensureIgnored(dir string) {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ignoreLine {
			return
		}
	}
	var add string
	if len(data) == 0 {
		add = "# ced's navigation history — machine state, not project config.\n"
	} else if !strings.HasSuffix(string(data), "\n") {
		add = "\n"
	}
	add += ignoreLine + "\n"
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(add)
	_ = f.Close()
}
