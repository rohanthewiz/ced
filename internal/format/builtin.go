// =============================================================================
// File: internal/format/builtin.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Built-in formatting via an EXTERNAL tool. The per-project format.json
// pipeline is opt-in and trust-gated because a cloned repo controls the
// argv; the built-in commands are different in kind — they're hardcoded
// in this binary, point at well-known programs, and only ever touch the
// file the user just saved. Running one therefore needs no consent
// flow, the same way the editor runs `git` without asking.
//
// This is the MIDDLE rung of the built-in ladder:
//
//	project .ced/format.json     the repo's own answer, trust-gated
//	this file                    the ecosystem's answer, if installed
//	inprocess.go                 ced's answer — always available
//
// # Go
//
// goimports is preferred because it's a strict superset of gofmt: it
// applies gofmt's formatting AND adds/removes import lines to match
// the code. When goimports isn't installed but gopls is, `gopls imports
// -w` provides the same import fixing (gopls is far more commonly
// installed than the standalone goimports these days), chained with a
// plain gofmt pass for the formatting half. With neither, plain gofmt
// still formats; with nothing on PATH the save behaves exactly as
// before — silent degradation, the same contract as the LSP
// integration. Go never falls through to the in-process rung: there is
// no pure-Go reimplementation of gofmt here, and there shouldn't be.
//
// # JSON, and why the list is short
//
// EVERY command here must rewrite the file IN PLACE. execFormatterChain
// runs an explicit argv through exec.Command with no shell — that is
// the whole reason a malicious format.json can't chain commands — so
// there is nowhere for a stdout-only formatter's output to go. That one
// constraint disqualifies the most obvious candidate: `jq` has no
// in-place flag at all (`jq . file > file` truncates the file before jq
// reads it, which is exactly the footgun ced must not build), so jq is
// deliberately absent despite being the tool most users would name
// first. Suggested additions must pass the same test.
//
// # Why a repo-local tool outranks a global one
//
// A prettier in <root>/node_modules/.bin is a statement BY THIS REPO
// that prettier is how it formats — it's pinned in package.json and
// every contributor gets the same one. A prettier on $PATH is a
// statement about the developer's laptop, which may have nothing to do
// with the project they're editing. Both are used, in that order, so
// the repo's answer wins where the repo has one.
//
// # Keeping the file's indentation
//
// Every JSON tool here defaults to two spaces, so without help a
// tab-indented (or four-space) file would be re-indented wholesale the
// first time it was formatted. ced reads the file's existing unit
// (indent.go) and hands it to the tool as flags — but ONLY when the
// repo has not configured the tool itself, because a biome.json or a
// .prettierrc is the repo's explicit answer and outranks anything ced
// infers from one file:
//
//	prettier   --config-precedence prefer-file: prettier itself drops
//	           our flags when it finds any config (.prettierrc, the
//	           package.json key, .editorconfig) — its resolution rules
//	           are too many for ced to re-implement faithfully
//	biome      flags only when <root> has no biome.json[c]
//	deno       flags only when <root> has no deno.json[c]
//
// A file with no indentation to read (minified, one line) gets no flags
// at all: the tool's own default applies, exactly as before.
//
// # PostgreSQL
//
// Two tools pass the in-place test: pg_format (pgFormatter, `-i`) and
// sql-formatter (`--fix`). The order follows the repo-local rule above:
//
//	<root>/node_modules/.bin/sql-formatter   the repo pinned it
//	pg_format on $PATH                       Postgres's own formatter
//	sql-formatter on $PATH                   the generic fallback
//
// pg_format outranks a GLOBAL sql-formatter because it is written for
// PostgreSQL alone; sql-formatter needs telling (`postgresql`), and a
// machine with both is likelier to be a Postgres developer's.
//
// Both get the file's own conventions — indent unit AND keyword case
// (sqlfmt.go's vote) — unless the tool is configured. Neither tool finds
// its config relative to the FILE: both search the process's working
// directory, which in ced is wherever the terminal panel last cd'd. So a
// config in the project root is handed over explicitly with -c, which
// makes it win regardless of cwd:
//
//	pg_format       <root>/.pg_format → `-c` it; ~/.pg_format or
//	                $XDG_CONFIG_HOME/pg_format/pg_format.conf → no flags
//	                (pg_format reads those itself); none → style flags
//	sql-formatter   <root>/.sql-formatter.json → `-c` it; none → `-c`
//	                with an inline JSON config naming the dialect + style

package format

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// lookPath is swappable in tests so builtin resolution doesn't depend
// on which tools the machine running the tests happens to have.
var lookPath = exec.LookPath

// nodeBinDir is where an npm project puts the executables of its own
// dependencies. Checked before $PATH for every JavaScript-ecosystem
// formatter — see the file comment.
const nodeBinDir = "node_modules/.bin"

// jsonFormatters lists the external JSON formatters ced knows, in
// preference order, as (binary, args-before-the-file). Every one
// rewrites in place; see the file comment for why that is a hard
// requirement rather than a convenience.
//
// prettier leads because it is by a wide margin the most commonly
// installed and the one a project is most likely to have pinned;
// biome is its faster drop-in and uses the same defaults; deno fmt is
// last because a machine with deno installed is usually editing Deno
// projects, where the file is likelier to be covered by a deno.json
// the project would rather ced didn't second-guess.
//
// indentArgs turns a detected Indent into that tool's flags; configs
// names the root-level files whose presence means the repo owns the
// tool's style, so no flags are passed (see the file comment). prettier
// lists none because its own --config-precedence makes that call.
var jsonFormatters = []struct {
	bin        string
	args       []string
	configs    []string
	indentArgs func(Indent) []string
}{
	{"prettier", []string{"--write"}, nil, prettierIndentArgs},
	{"biome", []string{"format", "--write"}, []string{"biome.json", "biome.jsonc"}, biomeIndentArgs},
	{"deno", []string{"fmt"}, []string{"deno.json", "deno.jsonc"}, denoIndentArgs},
}

// indentSampleBytes bounds how much of a file is read to detect its
// indentation. The command list is built on the main loop, so the read
// must stay small no matter how large the file; the first 64KB of a
// structured document holds thousands of indented lines, far more
// evidence than the narrowest-run rule needs.
const indentSampleBytes = 64 << 10

// prettierIndentArgs asks prettier for the file's unit while letting any
// prettier config the repo has override it.
func prettierIndentArgs(in Indent) []string {
	args := []string{"--config-precedence", "prefer-file"}
	if in.Tabs {
		return append(args, "--use-tabs")
	}
	return append(args, "--tab-width", strconv.Itoa(in.Width))
}

// biomeIndentArgs spells the unit in biome's flag vocabulary.
func biomeIndentArgs(in Indent) []string {
	if in.Tabs {
		return []string{"--indent-style=tab"}
	}
	return []string{"--indent-style=space", "--indent-width=" + strconv.Itoa(in.Width)}
}

// denoIndentArgs spells the unit in deno fmt's flag vocabulary. Spaces
// are deno's default, so only the width needs saying.
func denoIndentArgs(in Indent) []string {
	if in.Tabs {
		return []string{"--use-tabs"}
	}
	return []string{"--indent-width=" + strconv.Itoa(in.Width)}
}

// BuiltinCommandsFor returns the built-in external formatter pipeline
// for filePath — a list of argvs to run in order, each rewriting the
// file in place — or nil when ced knows no installed tool for it.
// Each argv carries the resolved binary path and the file's absolute
// path, ready for exec.Command.
//
// rootDir is the project root, used to find a repo-local tool before
// falling back to $PATH. An empty rootDir just skips that lookup.
//
// Precedence contract: a project format.json entry overrides this —
// callers must consult Config.CommandFor first. Returning nil does NOT
// mean "don't format": it means this rung declined, and the caller
// falls through to InProcessFormat.
func BuiltinCommandsFor(rootDir, filePath string) [][]string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	switch kindFor(filePath) {
	case kindGo:
		return goCommands(abs)
	case kindJSON:
		return jsonCommands(rootDir, abs)
	case kindSQL:
		return sqlCommands(rootDir, abs)
	default:
		return nil
	}
}

// goCommands resolves the Go pipeline. Preference order:
//
//	goimports -w            (format + fix imports, one tool)
//	gopls imports -w        (fix imports…)
//	  then gofmt -w         (…then format — two tools, same outcome)
//	gofmt -w                (format only; imports degrade silently)
func goCommands(abs string) [][]string {
	if bin, err := lookPath("goimports"); err == nil {
		return [][]string{{bin, "-w", abs}}
	}
	var cmds [][]string
	if bin, err := lookPath("gopls"); err == nil {
		cmds = append(cmds, []string{bin, "imports", "-w", abs})
	}
	if bin, err := lookPath("gofmt"); err == nil {
		cmds = append(cmds, []string{bin, "-w", abs})
	}
	return cmds
}

// jsonCommands resolves the first installed JSON formatter, repo-local
// copies first. Unlike the Go pipeline this never chains: each of these
// tools formats completely on its own, and running two would just mean
// the second one reformatting the first one's output to a different
// house style.
//
// The file is sampled for its indentation only once a tool has been
// found, so a machine with no JSON formatter never pays the read.
func jsonCommands(rootDir, abs string) [][]string {
	for _, f := range jsonFormatters {
		bin, ok := resolveTool(rootDir, f.bin)
		if !ok {
			continue
		}
		argv := append([]string{bin}, f.args...)
		if in := sampleIndent(abs); in.Known() && !hasAnyFile(rootDir, f.configs) {
			argv = append(argv, f.indentArgs(in)...)
		}
		return [][]string{append(argv, abs)}
	}
	return nil
}

// sqlCommands resolves the PostgreSQL formatter (see the file comment
// for the order and the config rules). Like JSON it never chains.
func sqlCommands(rootDir, abs string) [][]string {
	if rootDir != "" {
		local := filepath.Join(rootDir, filepath.FromSlash(nodeBinDir), "sql-formatter")
		if isExecutable(local) {
			return [][]string{sqlFormatterArgv(local, rootDir, abs)}
		}
	}
	if bin, err := lookPath("pg_format"); err == nil {
		return [][]string{pgFormatArgv(bin, rootDir, abs)}
	}
	if bin, err := lookPath("sql-formatter"); err == nil {
		return [][]string{sqlFormatterArgv(bin, rootDir, abs)}
	}
	return nil
}

// pgFormatArgv builds pg_format's in-place command line.
func pgFormatArgv(bin, rootDir, abs string) []string {
	argv := []string{bin, "-i"}
	if rootDir != "" && hasAnyFile(rootDir, []string{".pg_format"}) {
		return append(argv, "-c", filepath.Join(rootDir, ".pg_format"), abs)
	}
	if pgFormatUserConfig() {
		return append(argv, abs)
	}
	st := sampleSQLStyle(abs)
	switch {
	case st.indent.Tabs:
		argv = append(argv, "--tabs")
	case st.indent.Width > 0:
		argv = append(argv, "--spaces", strconv.Itoa(st.indent.Width))
	}
	if st.caseKnown {
		// 1 = lower, 2 = upper (pg_format's own numbering).
		argv = append(argv, "--keyword-case", map[bool]string{false: "1", true: "2"}[st.upper])
	}
	return append(argv, abs)
}

// pgFormatUserConfig reports whether pg_format will find a per-user
// config on its own, in which case ced passes no style flags: the user
// has said how they want SQL formatted.
func pgFormatUserConfig() bool {
	if home, err := os.UserHomeDir(); err == nil && hasAnyFile(home, []string{".pg_format"}) {
		return true
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		if home, err := os.UserHomeDir(); err == nil {
			xdg = filepath.Join(home, ".config")
		}
	}
	return xdg != "" && hasAnyFile(filepath.Join(xdg, "pg_format"), []string{"pg_format.conf"})
}

// sqlFormatterArgv builds sql-formatter's in-place command line. Its
// -c takes a file path or an inline JSON object; the inline form is how
// the dialect and the file's style reach it without writing a config.
func sqlFormatterArgv(bin, rootDir, abs string) []string {
	argv := []string{bin, "--fix"}
	if rootDir != "" && hasAnyFile(rootDir, []string{".sql-formatter.json"}) {
		return append(argv, "-c", filepath.Join(rootDir, ".sql-formatter.json"), abs)
	}
	cfg := map[string]any{"language": "postgresql"}
	st := sampleSQLStyle(abs)
	switch {
	case st.indent.Tabs:
		cfg["useTabs"] = true
	case st.indent.Width > 0:
		cfg["tabWidth"] = st.indent.Width
	}
	if st.caseKnown {
		cfg["keywordCase"] = map[bool]string{false: "lower", true: "upper"}[st.upper]
	}
	// Marshalling a map of strings, bools and ints cannot fail.
	js, _ := json.Marshal(cfg)
	return append(argv, "-c", string(js), abs)
}

// sampleSQLStyle reads the conventions of the SQL file at path from its
// first indentSampleBytes. An unreadable file reports no conventions,
// which means "pass no style" — the tool's defaults.
func sampleSQLStyle(path string) sqlStyle {
	f, err := os.Open(path)
	if err != nil {
		return sqlStyle{}
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, indentSampleBytes))
	if err != nil {
		return sqlStyle{}
	}
	return detectSQLStyle(head)
}

// isExecutable reports whether path is a regular file with an execute
// bit, following symlinks (a node_modules/.bin entry is normally one).
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// sampleIndent detects the indentation of the file at path from its
// first indentSampleBytes. An unreadable file reports the zero Indent,
// which means "pass no flags" — the tool's default, the behaviour
// before indentation was preserved at all.
func sampleIndent(path string) Indent {
	f, err := os.Open(path)
	if err != nil {
		return Indent{}
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, indentSampleBytes))
	if err != nil {
		return Indent{}
	}
	return DetectIndent(head)
}

// hasAnyFile reports whether any of names exists directly in dir. An
// empty dir (no project root) has none.
func hasAnyFile(dir string, names []string) bool {
	if dir == "" {
		return false
	}
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// resolveTool finds name in the project's own node_modules/.bin, then
// on $PATH. The local copy is checked with a stat rather than through
// lookPath because lookPath consults $PATH by definition and would miss
// a directory nobody has added to it.
//
// The local candidate must be a regular, executable file: an npm
// install that failed halfway can leave a dangling symlink in .bin, and
// handing exec.Command a path that isn't runnable would turn a missing
// dependency into a formatter error on every save.
func resolveTool(rootDir, name string) (string, bool) {
	if rootDir != "" {
		local := filepath.Join(rootDir, filepath.FromSlash(nodeBinDir), name)
		// Stat, not Lstat: a .bin entry is normally a symlink into the
		// package that provides it, and following it is the point.
		if isExecutable(local) {
			return local, true
		}
	}
	if bin, err := lookPath(name); err == nil {
		return bin, true
	}
	return "", false
}

// BuiltinHandles reports whether ced's built-in path (either rung) has
// any opinion about how to FORMAT this file. Used by the install-offer
// branch to stay quiet about a file ced already formats — offering to
// install a global default for JSON when the built-in already handles it
// would be a nag with no upside, which is the call already made for Go.
//
// Listed kinds rather than "!= kindNone" because kindCSV is validated
// but never formatted; a CSV file must stay open to the install offer.
func BuiltinHandles(filePath string) bool {
	switch kindFor(filePath) {
	case kindGo, kindJSON, kindSQL:
		return true
	default:
		return false
	}
}
