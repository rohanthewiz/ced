// =============================================================================
// File: internal/theme/load.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// load.go is the registry: built-in themes plus whatever the user has
// dropped into ~/.config/ced/themes/*.json, merged into one ordered list.
//
// House rules this file exists to enforce:
//
//   - **A user theme shadows a built-in IN PLACE.** Naming your file
//     "tokyo-night.json" replaces Tokyo Night at Tokyo Night's position
//     in the list, rather than appending a second row with the same
//     label. Two identically-named rows in a picker is a bug report.
//   - **A broken theme file is a warning, never a failure.** Registry
//     returns the specs it could read alongside the errors it hit; the
//     editor flashes the first and keeps going. Losing the ability to
//     start the editor over a stray comma in a color file would be a
//     terrible trade.
//   - **The file is the same sparse shape the built-ins use.** Eight
//     core keys is a complete theme; everything else derives (palette.go).
//     Nothing here expands a palette on disk.
package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileFormat mirrors a theme file on disk. It's exported because it
// documents the format users hand-write:
//
//	{
//	  "name":   "midnight",       // optional — defaults to the filename
//	  "label":  "Midnight",       // optional — defaults to the name
//	  "dark":   true,             // optional — inferred from bg luminance
//	  "colors": {"bg": "#0b0d13", "fg": "#dfe4f2", …}
//	}
//
// Dark is a pointer so "absent" is distinguishable from "false": absent
// means "work it out from the background", which is right far more often
// than it is wrong and saves the author a decision.
type FileFormat struct {
	Name   string  `json:"name,omitempty"`
	Label  string  `json:"label,omitempty"`
	Dark   *bool   `json:"dark,omitempty"`
	Colors Palette `json:"colors"`
}

// ThemeFileExt is the extension Registry scans for. JSON, not YAML, to
// match config.json / mcp.json / actions.json — one file format for
// everything under ~/.config/ced means no second parser and no surprise
// about which one this file wants.
const ThemeFileExt = ".json"

// LoadFile reads and validates one theme file. The palette is checked
// (Normalize) but stored sparse: what the author wrote is what gets
// written back if they later edit it through the editor.
//
// Name defaults to the filename stem so the minimal viable theme file is
// just a colors block — the author names it by naming the file.
func LoadFile(path string) (Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("read %s: %w", path, err)
	}
	var ff FileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return Spec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(ff.Colors) == 0 {
		return Spec{}, fmt.Errorf("%s: no \"colors\" block", path)
	}
	// Validate now, discard the expansion: a theme that can't resolve
	// must not reach the picker, but the sparse form is what we keep.
	norm, err := Normalize(ff.Colors)
	if err != nil {
		return Spec{}, fmt.Errorf("%s: %w", path, err)
	}

	name := strings.ToLower(strings.TrimSpace(ff.Name))
	if name == "" {
		name = strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if name == "" {
		return Spec{}, fmt.Errorf("%s: theme has no name", path)
	}
	label := strings.TrimSpace(ff.Label)
	if label == "" {
		label = name
	}
	dark := IsDark(norm["bg"])
	if ff.Dark != nil {
		dark = *ff.Dark
	}
	return Spec{
		Name:   name,
		Label:  label,
		Dark:   dark,
		Colors: ff.Colors,
		Source: SourceUser,
		Path:   path,
	}, nil
}

// Registry returns every theme available right now: the built-ins, with
// any same-named user theme swapped in at the built-in's position, plus
// the user's own new themes appended in filename order.
//
// dir is the user themes directory; "" (no resolvable config location)
// yields the built-ins alone. Errors from individual files are collected
// and returned rather than aborting the scan — one bad file must not
// hide the nine good ones next to it.
func Registry(dir string) ([]Spec, []error) {
	specs := Builtins()
	if dir == "" {
		return specs, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// No themes directory is the overwhelmingly common case —
			// not a condition worth telling the user about.
			return specs, nil
		}
		return specs, []error{fmt.Errorf("read %s: %w", dir, err)}
	}

	index := make(map[string]int, len(specs))
	for i, s := range specs {
		index[s.Name] = i
	}

	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ThemeFileExt) {
			continue
		}
		spec, err := LoadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if i, ok := index[spec.Name]; ok {
			specs[i] = spec // shadow the built-in in place
			continue
		}
		index[spec.Name] = len(specs)
		specs = append(specs, spec)
	}
	return specs, errs
}

// Find returns the spec with the given name from a registry. Matching is
// case-insensitive because config.json and the command line are both
// hand-typed.
func Find(specs []Spec, name string) (Spec, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, s := range specs {
		if s.Name == want {
			return s, true
		}
	}
	return Spec{}, false
}

// Resolve looks a theme up in the registry and resolves it to a Theme.
// A name that isn't there, or a theme that won't normalize, falls back
// to the shipped default and returns the reason — the caller flashes it.
// The editor always ends up with a usable palette; there is no path
// where a bad theme preference leaves the screen unreadable.
func Resolve(specs []Spec, name string) (Theme, Spec, error) {
	spec, ok := Find(specs, name)
	if !ok {
		def, _ := Find(Builtins(), DefaultName)
		return Default(), def, fmt.Errorf("unknown theme %q", name)
	}
	th, err := spec.Resolve()
	if err != nil {
		def, _ := Find(Builtins(), DefaultName)
		return Default(), def, err
	}
	return th, spec, nil
}

// Encode renders a spec as a theme file. Colors are written in canonical
// key order (palette.go's Keys), not Go's map order, so a generated file
// reads top-down the way the derivation table thinks: core eight first,
// then surfaces, signals, and syntax.
//
// full expands the palette to every key — that's what "Customize theme…"
// writes, because a user editing a copy of a built-in wants to see the
// whole board, including the values that were only ever derived. Passing
// false keeps the palette exactly as authored.
func Encode(s Spec, full bool) ([]byte, error) {
	colors := s.Colors
	if full {
		norm, err := Normalize(s.Colors)
		if err != nil {
			return nil, err
		}
		colors = norm
	}

	var buf bytes.Buffer
	buf.WriteString("{\n")
	fmt.Fprintf(&buf, "  %q: %q,\n", "name", s.Name)
	fmt.Fprintf(&buf, "  %q: %q,\n", "label", s.Label)
	fmt.Fprintf(&buf, "  %q: %t,\n", "dark", s.Dark)
	buf.WriteString("  \"colors\": {\n")
	first := true
	for _, k := range Keys() {
		v, ok := colors[k]
		if !ok {
			continue
		}
		if !first {
			buf.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&buf, "    %q: %q", k, v)
	}
	buf.WriteString("\n  }\n}\n")
	return buf.Bytes(), nil
}

// Save writes a theme file to dir, named <name>.json, creating the
// directory if needed. Returns the path written so the caller can open
// it in a tab — editing the file you just created is the whole point of
// the "Customize theme…" flow.
//
// Writes atomically (temp + rename), the same discipline as the config
// writer: a crash mid-write must not leave a half-parsed theme behind
// that then fails to load on the next start.
func Save(dir string, s Spec, full bool) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no config directory resolved — cannot save theme")
	}
	out, err := Encode(s, full)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, s.Name+ThemeFileExt)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}
