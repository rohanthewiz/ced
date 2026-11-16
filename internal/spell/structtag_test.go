// =============================================================================
// File: internal/spell/structtag_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package spell

import (
	"slices"
	"testing"
)

// tagNameStrings runs TagNames and returns "key=name" strings, which is
// what the table asserts on; columns are pinned separately.
func tagNameStrings(line string) []string {
	rs := []rune(line)
	var out []string
	for _, tn := range TagNames(rs) {
		out = append(out, tn.Key+"="+string(rs[tn.Start:tn.End]))
	}
	return out
}

// TestTagNames_ReadsEveryKeysName: every key of a tag is read, not just
// json, and the name is the value up to its first comma.
func TestTagNames_ReadsEveryKeysName(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"\tName string `json:\"usrName,omitempty\"`",
			[]string{"json=usrName"}},
		{"\tName string `json:\"name\" yaml:\"name\" db:\"user_name\" bson:\"_id\"`",
			[]string{"json=name", "yaml=name", "db=user_name", "bson=_id"}},
		{"\tPort int `koanf:\"server.port\" env:\"SERVER_PORT\"`",
			[]string{"koanf=server.port", "env=SERVER_PORT"}},
		{"\tA, B string `json:\"a\"` // see also `xml:\"bee\"`",
			[]string{"json=a", "xml=bee"}}, // a tag-shaped comment is read too
		{"\tName string `json:\"name\"xml:\"name\"`", // reflect accepts a missing space
			[]string{"json=name", "xml=name"}},
	} {
		if got := tagNameStrings(tc.line); !slices.Equal(got, tc.want) {
			t.Errorf("TagNames(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// TestTagNames_SkipsValuesThatAreNotNames: the identifier-shape rule
// keeps the check off option strings, mini-languages and prose.
func TestTagNames_SkipsValuesThatAreNotNames(t *testing.T) {
	for _, line := range []string{
		"\tX int `json:\"-\"`",
		"\tX int `json:\",omitempty\"`",
		"\tX int `gorm:\"column:user_name;type:varchar(100)\"`",
		"\tX int `validate:\"gte=0,lte=130\"`",
		"\tX int `description:\"The user's name\"`",
		"\tX int `pattern:\"^[a-z]+$\"`",
		"\tX int `default:\"10\"`",
		"\tX int `json:\"na\\\"me\"`", // an escape would shift the columns
		"\tX int `json:\"naïve\"`",    // not ASCII
	} {
		if got := tagNameStrings(line); len(got) != 0 {
			t.Errorf("TagNames(%q) = %q, want nothing", line, got)
		}
	}
}

// TestTagNames_StrictnessGuardsBacktickPairing: text between two
// backticks that is not a whole tag contributes nothing — the guard
// against a multi-line raw string closing on this line.
func TestTagNames_StrictnessGuardsBacktickPairing(t *testing.T) {
	for _, line := range []string{
		"WHERE id = 1` + `json:\"tail\"`", // pairs as " + ", then an unpaired tick
		"\tq := `SELECT name FROM users`",
		"\tX int `json:\"name\" stray`",
		"\tX int `json:\"unclosed`",
		"\tX int `json:name`",
		"\tX int ``",
		"no backticks here: json:\"name\"",
	} {
		if got := tagNameStrings(line); len(got) != 0 {
			t.Errorf("TagNames(%q) = %q, want nothing", line, got)
		}
	}
}

// TestTagNames_Columns pins the rune columns the editor paints at,
// including after a multi-byte rune earlier on the line.
func TestTagNames_Columns(t *testing.T) {
	line := []rune("// é `json:\"abc,omitempty\"`")
	got := TagNames(line)
	if len(got) != 1 {
		t.Fatalf("TagNames = %+v, want one name", got)
	}
	if s := string(line[got[0].Start:got[0].End]); s != "abc" {
		t.Errorf("columns [%d,%d) cover %q, want %q", got[0].Start, got[0].End, s, "abc")
	}
	if got[0].Start != 12 {
		t.Errorf("Start = %d, want 12 (runes, not bytes)", got[0].Start)
	}
}
