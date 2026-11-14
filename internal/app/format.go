// =============================================================================
// File: internal/app/format.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

package app

// Formatter wiring. The pure logic (config parsing, trust file,
// builtin Go tooling) lives in internal/format; this file is the
// bridge into the editor's event loop and modals.
//
// Two doors lead into ONE pipeline (runFormatter):
//
//	save / auto-save ──► runFormatOnSave ──(Go only)──┐
//	≡ File → Format file ──► formatActiveFile ────────┴──► runFormatter
//
// Only Go formats as a side effect of saving (format.FormatsOnSave):
// gofmt's layout is the language's single canonical one, so it can
// never overrule a choice the author made. Documents — JSON and every
// other format, including whatever a project's format.json lists —
// carry layouts people pick on purpose, so they are reformatted only
// when the user asks. The pipeline itself is identical behind either
// door:
//
//  1. Load <root>/.ced/format.json. Entry for this extension →
//     check the trust store: Allowed → run; Denied → done; Unknown
//     → open the trust prompt and re-enter the run on Allow.
//  2. No project entry + the file is Go → run the built-in
//     goimports/gofmt pass (no trust gate — the argv is ours, not
//     the repo's).
//  3. Otherwise offer to install a matching global default, if any.
//  4. exec.Command in a goroutine; post a formatDoneEvent on
//     completion so the main loop can reload the buffer (when the
//     user hasn't typed in the meantime) and flash a status.
//
// Auto-save reuses the same pipeline in quiet mode: no prompts, no
// flashes, skip-if-untrusted. See autosave.go for why.
//
// Keeping everything except the goroutine on the main loop means the
// usual rule still holds: tcell state is mutated only from the event
// dispatch.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/crash"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/format"
)

// formatDoneEvent is posted by runFormatter when the goroutine
// finishes. tabPath is how we re-find the right tab on the main
// loop — not the index, because tabs may have been reordered or
// closed in the meantime.
type formatDoneEvent struct {
	when    time.Time
	tabPath string
	label   string // command name (just argv[0]) for status messages
	err     error
	// quiet marks a run triggered by auto-save rather than an explicit
	// Save. Quiet runs still reload the buffer on success but never
	// flash — auto-save fires every idle pause, and goimports failing
	// on mid-thought code would otherwise spam the status bar.
	quiet bool
	// seq is the run's number from formatClaim; the done handler drops
	// an event whose seq is not the path's active run. rev is the tab's
	// EditRev when the run started — the moment the buffer equalled the
	// file the formatter was handed — so the handler can tell "the user
	// edited since" from "nothing moved" (-1 when no tab was open).
	seq int
	rev int
}

// formatRun is the bookkeeping for the one formatter run in flight on a
// path (App.formatActive). See formatClaim.
type formatRun struct {
	seq int // matches formatDoneEvent.seq
	rev int // the tab's EditRev at claim time, -1 with no tab
	// started is when the run was claimed; a run older than
	// formatRunStale is presumed lost and superseded (formatClaim).
	started time.Time
	// rerun records that another request (a save, auto-save, or ≡ Format
	// file) arrived while this run was in flight; it runs once this one
	// settles. rerunQuiet is false when ANY of the folded requests was an
	// explicit one, so the user who pressed Save still gets the flash.
	rerun      bool
	rerunQuiet bool
}

// When satisfies the tcell.Event interface.
func (e *formatDoneEvent) When() time.Time { return e.when }

// builtinCommandsFor resolves the built-in Go formatter pipeline
// (goimports, else gopls imports + gofmt, else gofmt). A package var
// so tests can stub it to nil — otherwise every test that saves a .go
// fixture would shell out to whatever Go tools the dev machine has on
// PATH, the same reason newTestApp kills the LSP integration.
var builtinCommandsFor = format.BuiltinCommandsFor

// runFormatOnSave is called after a successful disk write — by
// saveTabAt for explicit saves (quiet=false) and by autoSaveTab for
// idle auto-saves (quiet=true). Only Go files go any further; see the
// file comment for why everything else waits for the explicit verb.
func (a *App) runFormatOnSave(idx int, quiet bool) {
	if idx < 0 || idx >= len(a.tabs) {
		return
	}
	if !format.FormatsOnSave(a.tabs[idx].Path) {
		return
	}
	a.runFormatter(idx, quiet)
}

// formatActiveFile is the explicit Format verb (≡ File → Format file):
// the door to the formatter for every kind that does not format on
// save, and a manual re-run for the one (Go) that does.
//
// The pipeline formats the file ON DISK, so a dirty buffer is saved
// first. For Go that save has already run the formatter, and running
// it a second time would only race the first run's write — so a Go
// save ends the verb. A save refused by the clobber guard (the file
// changed on disk) ends it too: formatting someone else's bytes is not
// what the user asked for, and the guard's prompt is already up.
func (a *App) formatActiveFile() {
	idx := a.activeTab
	if idx < 0 || idx >= len(a.tabs) {
		a.flash("No file to format")
		return
	}
	tab := a.tabs[idx]
	if tab.Path == "" {
		a.flash("Save the file first — formatters work on the file on disk")
		return
	}
	if tab.Dirty {
		if !a.saveTabAt(idx) {
			return
		}
		if format.FormatsOnSave(tab.Path) {
			return
		}
	}
	// An unavailable verb explains itself rather than failing silently:
	// the fix — a format.json entry — is something the user can do.
	if !a.runFormatter(idx, false) {
		a.flash(fmt.Sprintf("No formatter for %s — add one to .ced/format.json", filepath.Base(tab.Path)))
	}
}

// menuFormatFile is the ≡ File row for formatActiveFile.
func (a *App) menuFormatFile() {
	a.closeMenu()
	a.formatActiveFile()
}

// runFormatter runs the formatter pipeline for the tab at idx and
// reports whether any rung took the file — a run started, a trust or
// install prompt opened, or an error was surfaced. False means ced has
// no formatter for this file at all, which the explicit verb turns into
// a flash; the save path ignores it. It branches four ways:
//
//   - Project format.json has an entry for this extension → trust
//     check, prompt if needed, then run.
//   - No project entry but the file is Go and goimports/gofmt is
//     installed → run the built-in formatter. No trust prompt: the
//     argv is hardcoded in the binary, not supplied by the repo, so
//     there's nothing for the user to vet.
//   - No project entry, not (formattable) Go, but the user's global
//     format-defaults.json covers the extension → offer to install
//     the preference into the project.
//   - Nothing matches → silent no-op (the spec).
//
// Quiet mode never opens a modal and never flashes: an auto-save runs
// while the user is mid-thought, so an un-trusted config is silently
// skipped (the next explicit Save will prompt) and load errors wait
// for an explicit Save to be surfaced.
//
// Each branch is its own helper so the routing logic stays shallow
// and easy to follow. Errors loading config files are surfaced once
// (so a typo isn't silently ignored) but never block the save itself
// — that already happened before this function was called.
func (a *App) runFormatter(idx int, quiet bool) bool {
	if idx < 0 || idx >= len(a.tabs) {
		return false
	}
	tab := a.tabs[idx]
	if tab.Path == "" {
		return false
	}

	cfg, err := format.Load(a.rootDir)
	if err != nil {
		if !quiet {
			a.flash("format: " + err.Error())
		}
		// Reported as handled: the flash above IS the answer, and a
		// second "no formatter" flash would overwrite the real reason.
		return true
	}

	argv := cfg.CommandFor(tab.Path)
	if argv != nil {
		a.runWithTrust(idx, cfg, argv, quiet)
		return true
	}

	// Built-in formatting sits between the project config (which
	// overrides it) and the install offer (which it makes redundant for
	// the kinds ced handles — offering to install a gofmt default when
	// the builtin already runs one would be a nag with no upside).
	//
	// It is a two-rung ladder and the order is the design. The EXTERNAL
	// rung goes first so an installed tool — especially one the repo
	// pinned in its own node_modules — keeps its say: ced formatting
	// JSON its own way in a repo that has already chosen prettier would
	// be the editor arguing with the project. The IN-PROCESS rung is the
	// floor underneath, and it is what stops this being a feature that
	// only works on machines that happen to have a Node toolchain.
	if builtin := builtinCommandsFor(a.rootDir, tab.Path); len(builtin) > 0 {
		a.execFormatterChain(tab.Path, builtin, quiet)
		return true
	}
	if a.execInProcessFormat(tab.Path, quiet) {
		return true
	}

	// Project doesn't format this extension. See if the user has a
	// personal default we can offer to install — but never from an
	// auto-save, which must stay prompt-free.
	if !quiet {
		return a.maybeOfferInstall(idx, tab.Path)
	}
	return false
}

// runWithTrust drives the existing trust-check + run path, factored
// out so runFormatOnSave can stay a flat router. Behaviour matches
// the previous monolithic version exactly — denied stays silent,
// unknown opens the prompt, allowed runs. In quiet mode an unknown
// trust state is skipped instead of prompted: modals must never pop
// while the user is typing, and the next explicit Save will ask.
func (a *App) runWithTrust(idx int, cfg *format.Config, argv []string, quiet bool) {
	tab := a.tabs[idx]
	trust, err := format.LoadTrust(format.DefaultTrustPath())
	if err != nil {
		if !quiet {
			a.flash("format trust: " + err.Error())
		}
		return
	}
	switch trust.CheckTrust(a.rootDir, cfg.Hash()) {
	case format.TrustDenied:
		return
	case format.TrustUnknown:
		if !quiet {
			a.openFormatTrustPrompt(idx, cfg, argv)
		}
		return
	}
	a.execFormatter(tab.Path, argv, quiet)
}

// maybeOfferInstall checks whether the user has a global default
// formatter for this file's extension and, if so, prompts them to
// install it into the project's .ced/format.json. Skips
// silently when:
//
//   - no global defaults file exists (the common case for users
//     who haven't set personal preferences),
//   - the user's defaults don't cover this extension,
//   - the user already declined the install for this (project, ext),
//   - the project's format.json exists but is currently denied at
//     the trust level — piling on an install prompt while trust is
//     denied would be confusing UX.
//
// Reports whether the user saw anything — the prompt, or a load error
// flashed in its place — so the explicit Format verb knows whether to
// explain that no formatter exists.
func (a *App) maybeOfferInstall(idx int, tabPath string) bool {
	defaults, err := format.LoadDefaults(format.DefaultsPath())
	if err != nil {
		a.flash("format defaults: " + err.Error())
		return true
	}
	if defaults == nil {
		return false
	}
	ext := strings.TrimPrefix(filepath.Ext(tabPath), ".")
	if ext == "" {
		return false
	}
	// Pull the *template* (with $FILE intact) so we can write it
	// verbatim to the project's format.json on Yes — substituting
	// before persisting would bake the current file's absolute path
	// into the config and break for the next save, every other file
	// in the project, and every teammate who pulled the repo.
	template := append([]string(nil), defaults.Commands[ext]...)
	if len(template) == 0 {
		return false
	}

	tf, err := format.LoadTrust(format.DefaultTrustPath())
	if err != nil {
		a.flash("format trust: " + err.Error())
		return true
	}
	if tf.IsInstallDeclined(a.rootDir, ext) {
		return false
	}
	// If a project format.json exists and trust is currently denied,
	// don't pile on. The user already said no to formatting in this
	// project; offering to add a new entry would feel like a nag.
	if cfg, _ := format.Load(a.rootDir); cfg != nil {
		if tf.CheckTrust(a.rootDir, cfg.Hash()) == format.TrustDenied {
			return false
		}
	}

	a.openFormatInstallPrompt(idx, ext, template)
	return true
}

// openFormatTrustPrompt asks the user whether to allow this project's
// format.json to run commands on save. Yes records trust + runs the
// formatter on the file we just saved; No records denial and skips.
// Cancel (Esc) goes through the same deny path as No — there's no
// safe "decide later" because every save would re-fire the prompt,
// training the user to dismiss it without thinking.
//
// We capture the loaded *format.Config (not a fresh re-Load) so the
// hash we trust is the exact one we evaluated, not whatever the file
// looks like after an external edit between the prompt and the
// answer. Same defense as the (path, hash) trust key itself.
func (a *App) openFormatTrustPrompt(idx int, cfg *format.Config, argv []string) {
	if idx < 0 || idx >= len(a.tabs) {
		return
	}
	tab := a.tabs[idx]
	tabPath := tab.Path
	root := a.rootDir
	hash := cfg.Hash()

	msg := fmt.Sprintf("Allow %s to run its formatters?", filepath.Join(format.ConfigDir, format.ConfigFile))
	m := a.openConfirm("Trust this project's formatter?", msg, func(app *App) {
		// Yes — record allow, persist, and run. The prompt only opens
		// from an explicit Save, so the run is a loud one.
		app.persistTrust(root, hash, true)
		app.execFormatter(tabPath, argv, false)
	})
	// Cancel/No path: persist a denial so we don't re-prompt every
	// save. The hook lives on this confirm instance, so an unrelated
	// future confirm modal can't inherit the side effect.
	m.cancelHook = func(app *App) {
		app.persistTrust(root, hash, false)
	}
	// A security question standing between the user and a completed save,
	// so the host gets told the editor is blocked. The install prompt
	// below is deliberately NOT marked: it is an offer, and declining it
	// costs nothing — a push notification for it would be advertising.
	// See cats_glue.go.
	a.catsAsking("formatter trust")
}

// openFormatInstallPrompt asks whether to install the user's global
// default formatter for this extension into the project's
// .ced/format.json. Yes merges the entry, auto-trusts the
// resulting config (the user's consent here implies trust — same
// reasoning as "you wrote the file yourself"), and runs the
// formatter on the freshly-saved file. No persists a per-extension
// decline so the prompt won't fire on every save in this project.
//
// The prompt uses the same Yes/No confirm modal as the trust prompt
// so we stay within the existing modal vocabulary instead of
// inventing a new shape for one feature.
//
// argvTemplate is the unsubstituted argv (with $FILE intact) — it's
// what we persist into format.json so the config is portable across
// machines and reusable for every future save of this extension.
// Substitution happens at run time inside execFormatter via
// substituteFile, mirroring the path Config.CommandFor takes for
// already-installed entries.
func (a *App) openFormatInstallPrompt(idx int, ext string, argvTemplate []string) {
	if idx < 0 || idx >= len(a.tabs) {
		return
	}
	tab := a.tabs[idx]
	tabPath := tab.Path
	root := a.rootDir
	// Use just the executable name (argvTemplate[0]) in the prompt —
	// the full argv with flags would crowd the modal and most of the
	// time the user only cares which formatter is being added.
	formatterName := argvTemplate[0]

	title := "Install formatter for this project?"
	msg := fmt.Sprintf("Add %s for .%s to %s?", formatterName, ext,
		filepath.Join(format.ConfigDir, format.ConfigFile))

	m := a.openConfirm(title, msg, func(app *App) {
		// Yes — merge into project config, trust the new hash, run.
		hash, err := format.InstallCommandIntoProject(root, ext, argvTemplate)
		if err != nil {
			app.flash("install failed: " + err.Error())
			return
		}
		// Auto-trust: the user just consented to the exact contents
		// they wrote. Re-prompting would be busywork. Also clear any
		// past "declined" entry for this ext so installing now means
		// the prompt was really opt-in, not a leftover dismissal.
		app.persistTrust(root, hash, true)
		app.persistInstallDecline(root, ext, false)
		// Re-sync tree / git / finder immediately so the new
		// .ced/format.json appears in the sidebar (and the finder
		// index) without waiting for the 10-second tick. Previously
		// this path refreshed only tree + git and quietly left the
		// finder stale — exactly the forget-one bug workspaceChanged
		// exists to prevent.
		app.workspaceChanged()
		// Substitute $FILE at run time, never at install time. This
		// keeps the on-disk template portable while still pointing
		// the formatter at the file the user just saved.
		app.execFormatter(tabPath, substituteFile(argvTemplate, tabPath), false)
	})
	m.cancelHook = func(app *App) {
		app.persistInstallDecline(root, ext, true)
	}
}

// substituteFile expands $FILE in every arg of template using the
// absolute path of filePath. Used by the install Yes path because
// InstallCommandIntoProject persists the template verbatim — we
// can't rely on Config.CommandFor here, which loads from disk.
func substituteFile(template []string, filePath string) []string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	out := make([]string, len(template))
	for i, arg := range template {
		out[i] = strings.ReplaceAll(arg, format.FileToken, abs)
	}
	return out
}

// persistInstallDecline writes a per-extension install decision to
// the trust store. Mirrors persistTrust so the Yes / No / Esc
// branches of the install prompt all share one IO path with
// consistent error reporting.
func (a *App) persistInstallDecline(root, ext string, declined bool) {
	tf, _ := format.LoadTrust(format.DefaultTrustPath())
	if tf == nil {
		tf = &format.TrustFile{Projects: map[string]format.TrustEntry{}}
	}
	tf.SetInstallDeclined(root, ext, declined)
	if err := format.SaveTrust(format.DefaultTrustPath(), tf); err != nil {
		a.flash("format trust: " + err.Error())
	}
}

// persistTrust writes a trust decision to the on-disk trust store.
// Pulled out so both the Yes branch (in the trust callback) and the
// No branch (in the cancel hook) share one error-handling path —
// they should agree on what "best-effort" means.
func (a *App) persistTrust(root, hash string, trusted bool) {
	tf, _ := format.LoadTrust(format.DefaultTrustPath())
	if tf == nil {
		tf = &format.TrustFile{Projects: map[string]format.TrustEntry{}}
	}
	tf.SetTrust(root, hash, trusted)
	if err := format.SaveTrust(format.DefaultTrustPath(), tf); err != nil {
		a.flash("format trust: " + err.Error())
	}
}

// execFormatter shells out to argv with the file path already
// substituted in. Runs in a goroutine and posts a formatDoneEvent on
// completion so the main loop can reload the buffer and flash a
// status — exactly the same pattern runCustomAction uses. quiet runs
// (auto-save) skip the "running…" flash and mark the done-event so
// its handler stays silent too.
//
// We deliberately use exec.Command (not sh -c) with an explicit argv
// so a shell-injection vector via a malicious format.json is just
// not available: each arg is passed as-is to execve, no shell
// interpretation, no globbing, no command chaining.
func (a *App) execFormatter(tabPath string, argv []string, quiet bool) {
	if len(argv) == 0 {
		return
	}
	a.execFormatterChain(tabPath, [][]string{argv}, quiet)
}

// execFormatterChain runs a pipeline of formatter argvs sequentially in
// one goroutine, posting a single formatDoneEvent when the chain
// completes (or on the first failure — later stages must not run over
// a file an earlier stage rejected). Exists for the builtin
// gopls-imports-then-gofmt pair; the single-command paths flow through
// it via execFormatter so there is exactly one exec + event wiring.
func (a *App) execFormatterChain(tabPath string, cmds [][]string, quiet bool) {
	if len(cmds) == 0 || len(cmds[0]) == 0 {
		return
	}
	scr := a.screen
	// Base name only: the builtin path hands us a fully-resolved
	// binary (/opt/…/goimports) and status flashes read better as
	// just the tool name. Config-supplied bare names pass through
	// unchanged.
	label := filepath.Base(cmds[0][0])
	// One run per path: a request that arrives mid-run is queued, never
	// started beside it — see formatClaim. The claim also records the
	// in-flight mark BEFORE the goroutine starts, so the reconcile tick
	// can never observe the window between the formatter's write and the
	// done-event that adopts its mtime (formatRunBegin).
	seq, rev, ok := a.formatClaim(tabPath, quiet)
	if !ok {
		return
	}
	if !quiet {
		a.flash(label + "…")
	}
	go func() {
		defer crash.Recover()
		var err error
		for _, argv := range cmds {
			if len(argv) == 0 {
				continue
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			var out []byte
			out, err = cmd.CombinedOutput()
			if err == nil {
				continue
			}
			// Distinguish "binary not installed" from "formatter ran
			// but failed" — the first is a silent skip (per the spec)
			// that still lets the rest of the chain run, the second is
			// a status flash so the user sees breakage.
			var pathErr *exec.Error
			if errors.As(err, &pathErr) && errors.Is(pathErr.Err, exec.ErrNotFound) {
				err = nil
				continue
			}
			if len(out) > 0 {
				preview := string(out)
				if i := indexNewline(preview); i >= 0 {
					preview = preview[:i]
				}
				if len(preview) > 80 {
					preview = preview[:80] + "…"
				}
				err = fmt.Errorf("%v: %s", err, preview)
			}
			break
		}
		_ = scr.PostEvent(&formatDoneEvent{
			when:    time.Now(),
			tabPath: tabPath,
			label:   label,
			err:     err,
			quiet:   quiet,
			seq:     seq,
			rev:     rev,
		})
	}()
}

// execInProcessFormat runs ced's own formatter over a file it has just
// saved, reporting whether it took the job. False means "not my kind"
// — the caller falls through to the install offer, exactly as it did
// before this rung existed.
//
// Three things make this share the external path's machinery rather
// than shortcut it:
//
//   - It goes through formatDoneEvent, so there is exactly ONE place
//     that adopts a formatter's write (handleFormatDone → ReloadAsEdit,
//     one structural undo step on top of the user's history). A
//     synchronous in-line reload here would be a second adoption path,
//     and the one that quietly forgot the dirty-buffer case.
//   - It is bracketed by formatRunBegin/End, so the reconcile tick
//     cannot mistake ced's own write for somebody else's and cost the
//     user their undo history.
//   - It runs on a goroutine even though the work is fast. The read,
//     the parse and the write are all IO on a file of unknown size, and
//     the house rule is that the main loop does not block on IO.
//
// The file is re-read from DISK rather than encoded from the buffer:
// the external rung operates on the saved file, and the two rungs must
// produce the same result from the same starting point. Save has
// already written the buffer out, so the disk copy is the buffer.
func (a *App) execInProcessFormat(tabPath string, quiet bool) bool {
	if tabPath == "" || !format.FormatsInProcess(tabPath) {
		return false
	}
	// "json formatter", not "json" or "format json": this label is
	// substituted into four different sentences ("…", "Formatted with
	// %s", "%s: already formatted", "%s failed: %v"), and it has to read
	// as the NAME OF A TOOL in all of them — which is exactly what the
	// external rung's label already is ("goimports", "prettier").
	label := format.InProcessName(tabPath) + " formatter"
	scr := a.screen
	// Claimed BEFORE the goroutine starts, for the same reasons as the
	// external chain — see formatClaim. A queued request still "took the
	// job": it will run when the one in flight settles.
	seq, rev, ok := a.formatClaim(tabPath, quiet)
	if !ok {
		return true
	}
	if !quiet {
		a.flash(label + "…")
	}
	go func() {
		defer crash.Recover()
		err := formatFileInPlace(tabPath)
		_ = scr.PostEvent(&formatDoneEvent{
			when:    time.Now(),
			tabPath: tabPath,
			label:   label,
			err:     err,
			quiet:   quiet,
			seq:     seq,
			rev:     rev,
		})
	}()
	return true
}

// formatFileInPlace reads path, formats it, and writes it back only if
// the bytes actually changed.
//
// Skipping an unchanged write is not just an optimisation: a rewrite
// bumps the mtime, which every other layer reads as "this file moved".
// A save of an already-formatted file should be indistinguishable from
// a save with no formatter at all.
//
// A parse failure returns the error WITHOUT writing anything. The
// validator has already underlined the offending character; rewriting
// text nobody could parse is how a formatter destroys work.
func formatFileInPlace(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// The in-process passes (JSON, SQL) work on UTF-8 text; a file ced
	// holds as Windows-1252 (editor/encoding.go) is refused rather than
	// handed to code that would read its high bytes as U+FFFD. JSON is
	// UTF-8 by definition, so this only ever meets a legacy .sql file.
	if !utf8.Valid(src) {
		return fmt.Errorf("%s isn't UTF-8 — the built-in formatter only rewrites UTF-8 files", filepath.Base(path))
	}
	out, ok, err := format.InProcessFormat(path, src)
	if err != nil || !ok {
		// !ok here would mean FormatsInProcess and InProcessFormat
		// disagreed, which their shared kind table makes impossible —
		// but treating it as "nothing to do" beats writing nil over the
		// user's file if that ever stops being true.
		return err
	}
	if bytes.Equal(out, src) {
		return nil
	}
	return editor.WriteFileAtomic(path, out)
}

// indexNewline returns the index of the first newline in s, or -1.
// Tiny helper kept local because strings.IndexByte('\n') reads
// awkwardly in the middle of error formatting.
func indexNewline(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}

// handleFormatDone surfaces the result of a background formatter run,
// after a generation check: only the path's active run (formatClaim) may
// act, and a request queued behind it starts once it has settled.
// On success it reloads the affected tab from disk so the buffer
// shows the formatted output — but only if the user hasn't started
// editing again in the meantime (Dirty=true). Trampling unsaved
// edits would be the worst possible UX outcome of this feature.
//
// Quiet runs (auto-save) reload the same way but suppress the
// routine flashes: an auto-save fires on every idle pause, and
// goimports rejecting half-written code isn't news the user needs
// every two seconds. The one exception is a reload failure — that's
// rare, actionable breakage, so it's surfaced even when quiet.
func (a *App) handleFormatDone(e *formatDoneEvent) {
	if e == nil {
		return
	}
	// Generation check: only the run this path is waiting on may adopt
	// anything. Anything else is a stale answer and is dropped whole —
	// including its reconcile mark, which formatClaim already released
	// when it superseded the run (ending it twice would un-suppress the
	// tick under the run that replaced it).
	run := a.formatActive[e.tabPath]
	if run == nil || run.seq != e.seq {
		return
	}
	delete(a.formatActive, e.tabPath)
	// Before any other early return: the run is over either way, and a
	// count that leaked would suppress the reconcile tick for that file.
	a.formatRunEnd(e.tabPath)
	a.settleFormatRun(e, run)
	// After the settle, whichever way it went: a request that arrived
	// mid-run (or the re-save below) runs now, against what is on disk.
	a.formatRerunIfQueued(e.tabPath, run)
}

// settleFormatRun is the body of handleFormatDone for the run that is
// actually current: surface an error, keep a dirty buffer, put back a
// save the formatter may have trampled, or adopt the formatted text.
func (a *App) settleFormatRun(e *formatDoneEvent, run *formatRun) {
	if e.err != nil {
		if !e.quiet {
			a.flash(fmt.Sprintf("%s failed: %v", e.label, e.err))
		}
		return
	}
	for _, tab := range a.tabs {
		if tab.Path != e.tabPath {
			continue
		}
		if tab.Dirty {
			// The formatter's write was OURS, so adopt its version even
			// though the buffer keeps the user's newer edits. Without
			// this the tab would look exactly like "someone else wrote
			// your file while you had unsaved changes" — a clobber
			// conflict (reconcile.go) — and ced would block the user's
			// next save behind a question about a write it made itself.
			// Their edits still win on save, which is what the flash
			// below promises.
			_ = tab.AdoptDisk()
			// The disk holds the FORMATTED save, which the buffer never
			// held, so undoing back to the pre-format text must not read
			// clean. Unknown until the next save.
			tab.ForgetSavedText()
			if !e.quiet {
				a.flash(fmt.Sprintf("%s ran — kept your edits (file on disk was reformatted)", e.label))
			}
			return
		}
		if tab.EditRev != e.rev {
			// Clean, yet the buffer moved after this run began: the user
			// edited and SAVED again while the formatter was working on
			// the OLDER text (or undid back to the saved text). This run's
			// write may have landed on top of that save, so adopting it
			// would swap the newer text out of the buffer AND leave it off
			// disk — the N-116 loss, silent apart from the undo stack.
			// Leaving it would be no better: a clean tab over a file that
			// holds older text. So the buffer (which is what the user
			// last saved) goes back on disk — our own write, so no clobber
			// guard — and the formatter runs again over THAT.
			if err := tab.Save(); err != nil {
				a.flash(fmt.Sprintf("Save failed: %v", err))
				return
			}
			a.requestFileDiff(tab.Path)
			a.lspDidSave(tab)
			if !run.rerun {
				run.rerun, run.rerunQuiet = true, e.quiet
			}
			return
		}
		// ReloadAsEdit, never Reload: the formatter's write is the tail of
		// the user's own save, not news from outside, so adopting it must
		// not reset the undo baseline. A plain Reload here destroyed the
		// whole history on every quiet auto-save — roughly every idle
		// pause in a Go file — which read as undo being broken.
		changed, err := tab.ReloadAsEdit()
		if err != nil {
			a.flash(fmt.Sprintf("%s ran but reload failed: %v", e.label, err))
			return
		}
		if !e.quiet {
			// The user pressed Save and is owed an answer either way:
			// silence after the "goimports…" flash reads as a hang. But
			// only a run that moved bytes may claim it formatted anything.
			if changed {
				a.flash(fmt.Sprintf("Formatted with %s", e.label))
			} else {
				a.flash(fmt.Sprintf("%s: already formatted", e.label))
			}
		}
		return
	}
	// Tab was closed before the formatter finished — silent no-op.
}

// formatClaim registers a formatter run on tabPath and reports whether
// the caller may start it. While a run is already in flight on the path
// the request is folded into that run's rerun flag instead (ok=false),
// and it starts when the run in flight settles.
//
// One run per path is the fix for N-116. Two overlapping runs used to be
// allowed: save (run #1 starts on text A), keep typing, save again
// (text B on disk, run #2 starts). If #1 finished last-but-one it wrote
// format(A) over B, and the done handler — matching by path alone, on a
// tab that read clean because of the second save — adopted it, so B
// vanished from both the buffer and the disk. With one run per path and
// the claim-time EditRev riding the event (seq, rev), the handler can
// see the buffer moved, refuse the stale adoption, and put B back.
//
//	save A ──► claim #1 (rev=a) ──► gofmt(A) ··· done #1: rev moved
//	save B ──► queued (rerun)                     → re-save B, rerun
//	                                              ──► claim #2 ──► gofmt(B)
//
// The claim also takes the reconcile-suppression count (formatRunBegin),
// so every formatter launch makes both marks in one call.
func (a *App) formatClaim(tabPath string, quiet bool) (seq, rev int, ok bool) {
	if r := a.formatActive[tabPath]; r != nil {
		if time.Since(r.started) < formatRunStale {
			if !r.rerun {
				r.rerun, r.rerunQuiet = true, quiet
			} else {
				r.rerunQuiet = r.rerunQuiet && quiet
			}
			return 0, 0, false
		}
		// The run's done-event never came: tcell drops a PostEvent when
		// its queue is full (N-124), and a formatter can hang (N-126a).
		// Without this, one lost event would queue every later format of
		// this file behind a run that will never settle. Release its marks
		// and start fresh; if its event does turn up, the seq no longer
		// matches and handleFormatDone ignores it.
		delete(a.formatActive, tabPath)
		a.formatRunEnd(tabPath)
	}
	a.formatSeq++
	rev = -1
	if t := a.tabForPath(tabPath); t != nil {
		rev = t.EditRev
	}
	if a.formatActive == nil {
		a.formatActive = map[string]*formatRun{}
	}
	a.formatActive[tabPath] = &formatRun{seq: a.formatSeq, rev: rev, started: time.Now()}
	a.formatRunBegin(tabPath)
	return a.formatSeq, rev, true
}

// formatRerunIfQueued starts the request folded into run while it was in
// flight. Skipped when the tab is gone, or dirty: a dirty buffer is not
// on disk, so formatting the disk now would format text the user has
// moved past — their next save formats what they actually wrote.
func (a *App) formatRerunIfQueued(tabPath string, run *formatRun) {
	if !run.rerun {
		return
	}
	tab := a.tabForPath(tabPath)
	if tab == nil || tab.Dirty {
		return
	}
	if idx := a.tabIndexOf(tab); idx >= 0 {
		a.runFormatter(idx, run.rerunQuiet)
	}
}

// formatRunStale is how long a claimed run may go without its done-event
// before formatClaim presumes the event lost and starts afresh. Well past
// any real formatter (gofmt takes milliseconds, prettier a second or two)
// so a slow run is never superseded while it is still writing.
const formatRunStale = 2 * time.Minute

// formatRunBegin / formatRunEnd / formatRunning track how many formatter
// runs are in flight per file path.
//
// They exist for one race. The formatter rewrites the file from a
// goroutine, and the background tree tick can stat that file in the
// window before formatDoneEvent reaches the loop — where an mtime WE
// caused looks exactly like somebody else's edit. On a clean tab that
// cost the undo history (reconcile takes the disk copy via
// ReloadUndoable); on a dirty one it was worse, recording a conflict that
// suspends auto-save and raises a modal about ced's own write.
//
// A count rather than a flag: formatter runs are one per path now
// (formatClaim), but git Restore holds the same mark (gitrestore.go) and
// may overlap a formatter run on the same file. Main-loop only: incremented
// before the goroutine starts, decremented when handleFormatDone accepts
// the run's event (or formatClaim supersedes a lost one), so no locking is
// needed.
//
// Deliberately NOT consulted by saveGuard / autoSaveGuard: those have the
// same theoretical window, but hitting it needs a keypress inside ~100ms
// and the failure mode is a prompt rather than lost history. Teaching
// every guard about formatter internals costs more than it buys.
func (a *App) formatRunBegin(path string) {
	if path == "" {
		return
	}
	if a.formatRuns == nil {
		a.formatRuns = map[string]int{}
	}
	a.formatRuns[path]++
}

// formatRunEnd drops one in-flight run for path, deleting the entry at
// zero so the map can't accumulate one key per file ever formatted.
func (a *App) formatRunEnd(path string) {
	if path == "" || a.formatRuns == nil {
		return
	}
	if n := a.formatRuns[path] - 1; n > 0 {
		a.formatRuns[path] = n
	} else {
		delete(a.formatRuns, path)
	}
}

// formatRunning reports whether a formatter is mid-write on path.
func (a *App) formatRunning(path string) bool {
	return a.formatRuns[path] > 0
}

// formatHash exposes the current project's format.json hash for tests
// and is otherwise unused. It returns "" when no config is loaded —
// the same signal as "no formatting configured". Pulled into a
// method so tests don't have to re-implement the load path.
func (a *App) formatHash() string {
	cfg, _ := format.Load(a.rootDir)
	return cfg.Hash()
}

// Compile-time check that formatDoneEvent really is a tcell.Event.
// Catches signature drift if the interface ever grows a method.
var _ tcell.Event = (*formatDoneEvent)(nil)
