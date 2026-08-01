// =============================================================================
// File: internal/skills/skills.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package skills is ced's inventory of agent SKILLS: the folders of
// markdown instructions the coding-agent ecosystem has standardised on,
// discovered from the places those tools already keep them.
//
// A skill is a directory holding a SKILL.md whose YAML frontmatter names
// and describes it:
//
//	~/.claude/skills/rweb-light-go-webserver/SKILL.md
//	  ---
//	  name: rweb-light-go-webserver
//	  description: Build HTTP web servers with the RWeb Go framework…
//	  ---
//	  # RWeb …the instructions themselves…
//
// Three directories are scanned, in increasing precedence:
//
//	~/.config/ced/skills   skills written for ced itself
//	~/.claude/skills       the user's personal skills (the standard spot)
//	<project>/.claude/skills   skills checked in beside the code
//
// Same-named skills SHADOW rather than stack, highest precedence wins:
// a project can override a personal skill by name, which is the whole
// reason a project-local directory is worth scanning. Two rows with one
// name in a picker is a bug report.
//
// Loading follows the same best-effort contract as every other ced
// config: a missing directory is the common case and says nothing at
// all, an unreadable file costs that one skill and nothing else, and the
// editor never fails to start over any of it.
//
// This package deliberately knows nothing about what a skill is FOR —
// see internal/app/skills.go for the editor half, which hands a chosen
// skill to the chat agent as context for the next turn.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileName is the file every skill directory must contain. Fixed by the
// ecosystem, not by us — the point of this package is reading the
// folders other tools already wrote.
const FileName = "SKILL.md"

// Source records which directory a skill came from, so the picker can
// say where an entry lives (two skills with similar names in different
// scopes is otherwise a guessing game).
type Source string

const (
	SourceCed     Source = "ced"
	SourceUser    Source = "user"
	SourceProject Source = "project"
)

// Dir is one directory to scan, paired with the source label its skills
// inherit. Callers pass these in increasing precedence order.
type Dir struct {
	Path   string
	Source Source
}

// Skill is one discovered skill.
type Skill struct {
	// Name is the frontmatter's name, falling back to the directory's
	// own name. It's the shadowing key, so it is lower-cased and
	// trimmed — hand-written frontmatter and a directory name must not
	// count as two different skills over a stray capital.
	Name string

	// Description is the frontmatter's description, whitespace-collapsed
	// onto one line. May be empty: a skill with no description is worse
	// to use but perfectly loadable, and refusing it would only hide it.
	Description string

	// Dir is the skill's own directory (absolute), and Path is the
	// SKILL.md inside it. Both are kept because a skill's supporting
	// files (scripts, references) live beside the markdown, and an agent
	// told about the directory can go read them.
	Dir  string
	Path string

	Source Source
}

// Summary is the one-line description used in pickers and prompt notes,
// clipped to at most max runes with an ellipsis. Clipping happens here
// rather than at the draw call because these labels are also what the
// fuzzy scorer searches — an unbounded paragraph would make every row
// score alike.
func (s Skill) Summary(max int) string {
	d := s.Description
	if max <= 0 || len([]rune(d)) <= max {
		return d
	}
	return string([]rune(d)[:max-1]) + "…"
}

// LoadFile reads one SKILL.md into a Skill. The directory name is the
// fallback identity: frontmatter is conventional, not enforced, and a
// skill whose author forgot the name key is still a usable skill.
//
// Only an unreadable file is an error. Everything else degrades — see
// the package comment for why that's the right trade here.
func LoadFile(path string, src Source) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	fields := parseFrontmatter(string(data))

	name := strings.ToLower(strings.TrimSpace(fields["name"]))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(filepath.Base(dir)))
	}
	if name == "" {
		return Skill{}, fmt.Errorf("%s: skill has no name", path)
	}
	return Skill{
		Name:        name,
		Description: collapseSpace(fields["description"]),
		Dir:         dir,
		Path:        path,
		Source:      src,
	}, nil
}

// LoadDir scans one directory for skill folders, returning what it could
// read plus the errors it hit. A missing directory yields nothing and no
// error — most users have never created two of these three, and telling
// them so on every start would be noise.
//
// Only immediate children are considered, and only those holding a
// SKILL.md: a directory of notes sitting next to the skills is not a
// broken skill, it's not a skill.
func LoadDir(d Dir) ([]Skill, []error) {
	if d.Path == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(d.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read %s: %w", d.Path, err)}
	}
	var (
		out  []Skill
		errs []error
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(d.Path, e.Name(), FileName)
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			continue
		}
		s, err := LoadFile(path, d.Source)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, s)
	}
	return out, errs
}

// Registry scans every directory and merges the results into one list,
// sorted by name. dirs are in increasing precedence: a later directory's
// skill REPLACES an earlier one of the same name rather than appearing
// beside it.
func Registry(dirs []Dir) ([]Skill, []error) {
	var (
		byName = map[string]Skill{}
		errs   []error
	)
	for _, d := range dirs {
		found, e := LoadDir(d)
		errs = append(errs, e...)
		for _, s := range found {
			byName[s.Name] = s // later dir wins — see the doc comment
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	// Map iteration is random; sorting is what makes the picker stable
	// from run to run (the same reason mcp.Load sorts its inventory).
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

// Find looks a skill up by name, case-insensitively — names come from
// hand-written frontmatter and hand-typed directory names.
func Find(list []Skill, name string) (Skill, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, s := range list {
		if s.Name == want {
			return s, true
		}
	}
	return Skill{}, false
}

// -----------------------------------------------------------------------------
// Frontmatter
// -----------------------------------------------------------------------------

// parseFrontmatter pulls the leading `---` fenced block's scalar keys out
// of a SKILL.md. It is deliberately NOT a YAML parser: the frontmatter
// this editor cares about is two string keys, and pulling in a YAML
// dependency to read them would be a poor trade for a project whose
// whole shape is "one static binary, no CGO, few deps".
//
// What it does understand, because real skill files in the wild use all
// of it: quoted values, block scalars (`|`, `>`, with their -/+ chomping
// variants), and plain values continued on following indented lines.
// What it ignores: nested maps and lists (any key whose value is a
// structure is skipped rather than mangled). Unknown keys are kept —
// harmless, and cheaper than a whitelist that would need updating.
//
// A file with no frontmatter yields no fields, which the caller treats
// as "fall back to the directory name", not as an error.
func parseFrontmatter(text string) map[string]string {
	lines := strings.Split(text, "\n")
	i := 0
	// Tolerate a UTF-8 BOM and blank lines ahead of the fence: an editor
	// that added either shouldn't cost the user their skill's name.
	if len(lines) > 0 {
		lines[0] = strings.TrimPrefix(lines[0], "\ufeff")
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "---" {
		return nil
	}
	i++

	fields := map[string]string{}
	for i < len(lines) {
		line := lines[i]
		if trimmed := strings.TrimSpace(line); trimmed == "---" || trimmed == "..." {
			break
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			i++
			continue
		}
		// Only top-level keys: an indented line here belongs to a
		// structure we're skipping, since continuations are consumed by
		// the key that owns them.
		if line != strings.TrimLeft(line, " \t") {
			i++
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			i++
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		i++

		if key == "" {
			continue
		}
		switch {
		case strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">"):
			fold := strings.HasPrefix(val, ">")
			var block []string
			block, i = readIndented(lines, i)
			sep := "\n"
			if fold {
				sep = " "
			}
			fields[key] = strings.TrimSpace(strings.Join(block, sep))
		case val == "":
			// Either an empty value or the head of a nested map/list. A
			// continuation that is itself a key line ("  name: x") or a
			// list item ("  - x") is a structure — skip it wholesale.
			var block []string
			block, i = readIndented(lines, i)
			if isStructure(block) {
				continue
			}
			fields[key] = strings.TrimSpace(strings.Join(block, " "))
		default:
			var block []string
			block, i = readIndented(lines, i)
			fields[key] = unquote(strings.TrimSpace(strings.Join(append([]string{val}, block...), " ")))
		}
	}
	return fields
}

// readIndented consumes the run of indented (or blank) lines starting at
// i — a scalar's continuation — returning them trimmed along with the
// index of the first line that isn't part of it.
func readIndented(lines []string, i int) ([]string, int) {
	var out []string
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			// A blank line inside a block is only a continuation if more
			// indented content follows; peek before consuming it.
			if j := i + 1; j < len(lines) && strings.TrimSpace(lines[j]) != "" &&
				lines[j] != strings.TrimLeft(lines[j], " \t") {
				out = append(out, "")
				i = j
				continue
			}
			return out, i
		}
		if line == strings.TrimLeft(line, " \t") {
			return out, i
		}
		out = append(out, strings.TrimSpace(line))
		i++
	}
	return out, i
}

// isStructure reports whether a continuation block is a nested map or
// list rather than a wrapped scalar — the shape parseFrontmatter skips.
func isStructure(block []string) bool {
	for _, l := range block {
		if strings.HasPrefix(l, "- ") || strings.Contains(l, ": ") || strings.HasSuffix(l, ":") {
			return true
		}
	}
	return false
}

// unquote strips one layer of matching quotes from a scalar. YAML escape
// sequences are left alone: a description is prose, and the frontmatter
// in practice is either unquoted or plainly quoted.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// collapseSpace folds a description onto one line: picker rows, chips
// and transcript notes are all single-line surfaces, and a description
// wrapped across three lines in the file would otherwise arrive with its
// newlines intact and break every one of them.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
