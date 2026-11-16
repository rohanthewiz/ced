// =============================================================================
// File: internal/spell/ident_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package spell

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// flaggedPieces runs CheckIdent and returns the flagged pieces as strings,
// which is what the table tests assert on; offsets are pinned separately.
func flaggedPieces(t *testing.T, name string) []string {
	t.Helper()
	rs := []rune(name)
	var out []string
	for _, sp := range dict.CheckIdent(name) {
		if sp.Start < 0 || sp.End > len(rs) || sp.Start >= sp.End {
			t.Fatalf("CheckIdent(%q): bad span %+v", name, sp)
		}
		out = append(out, string(rs[sp.Start:sp.End]))
	}
	return out
}

// TestCheckIdent_FlagsTheMisspelledPiece is the feature in one table:
// every naming convention a tag uses is cut into its words, and only the
// word that is wrong comes back.
func TestCheckIdent_FlagsTheMisspelledPiece(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"userNmae", []string{"Nmae"}},       // camelCase
		{"UserNmae", []string{"Nmae"}},       // PascalCase
		{"user_nmae", []string{"nmae"}},      // snake_case
		{"user-nmae", []string{"nmae"}},      // kebab-case
		{"server.prot", []string{"prot"}},    // dotted (koanf, viper)
		{"HTTPSevrer", []string{"Sevrer"}},   // an acronym running into a word
		{"DATABSE_URL", []string{"DATABSE"}}, // SCREAMING_SNAKE: long pieces checked
		{"adress", []string{"adress"}},       // a plain lowercase word
		{"lenghtUtf8", []string{"lenght"}},   // only the piece touching the digit is skipped
		{"pasword", []string{"pasword"}},     // must not split as pas+word
		{"existant", []string{"existant"}},   // must not split as exist+ant
		{"messsage", []string{"messsage"}},   // a stuck key, though mess+sage tiles it
		{"craeted_at", []string{"craeted"}},  // the connector is fine, the word is not
		{"userNmaeAdress", []string{"Nmae", "Adress"}},
	} {
		if got := flaggedPieces(t, tc.name); !slices.Equal(got, tc.want) {
			t.Errorf("CheckIdent(%q) flagged %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestCheckIdent_StaysQuietOnConventions: the things a checker that
// looked whole names up would flag on every struct, and must not.
func TestCheckIdent_StaysQuietOnConventions(t *testing.T) {
	for _, name := range []string{
		"userName", "user_name", "user-name", "UserName", "USER_NAME",
		"id", "db", "ok", "x", // one or two letters
		"userID", "userIDs", "URLsByHost", "HTTPServer", "apiURL", // acronyms
		"API_KEY", "AWS_REGION", // SCREAMING: short pieces are acronyms
		"sha256Sum", "utf8", "ipv4Addr", "oauth2Token", "s3Bucket", // digits
		"firstname", "lastname", "createdat", "isactive", "userid", "apikey", // run-together
		"lastloginat", "updatedby",
		"omitempty", "addr", "cfg", "signup", // ident.txt
		"Content-Type", "X-Request-ID", // header names
		"", "_id", "--",
	} {
		if got := flaggedPieces(t, name); len(got) != 0 {
			t.Errorf("CheckIdent(%q) flagged %q, want nothing", name, got)
		}
	}
}

// TestCheckIdent_Offsets pins the rune offsets the editor turns into
// columns: a span covers exactly the misspelled piece.
func TestCheckIdent_Offsets(t *testing.T) {
	got := dict.CheckIdent("userNmae_adress")
	want := []Span{{Start: 4, End: 8}, {Start: 9, End: 15}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckIdent offsets = %+v, want %+v", got, want)
	}
}

// TestCamelPieces pins the boundary rules, including the plural-acronym
// exception that keeps "IDs" whole.
func TestCamelPieces(t *testing.T) {
	for _, tc := range []struct {
		run  string
		want []string
	}{
		{"userName", []string{"user", "Name"}},
		{"HTTPServer", []string{"HTTP", "Server"}},
		{"userIDs", []string{"user", "IDs"}},
		{"URLsByHost", []string{"URLs", "By", "Host"}},
		{"IDsAndURLs", []string{"IDs", "And", "URLs"}},
		{"ABCDef", []string{"ABC", "Def"}},
		{"lower", []string{"lower"}},
		{"UPPER", []string{"UPPER"}},
	} {
		rs := []rune(tc.run)
		var got []string
		for _, p := range camelPieces(rs, 0, len(rs)) {
			got = append(got, string(rs[p.Start:p.End]))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("camelPieces(%q) = %q, want %q", tc.run, got, tc.want)
		}
	}
}

// TestCompound_TwoTierRule pins the rule that decides run-together names:
// list words need four letters, trusted words need none, and the number of
// parts is capped.
func TestCompound_TwoTierRule(t *testing.T) {
	d := New("first", "name", "pas", "word", "on", "a", "b", "c", "d", "e")
	d.Add("at") // trusted
	cases := []struct {
		w    string
		want bool
	}{
		{"firstname", true}, // two four-plus list words
		{"nameat", true},    // a trusted connector
		{"pasword", false},  // "pas" is a list word, but too short to trust
		{"nameon", false},   // "on" is a list word, not a trusted one
		{"name", false},     // the whole word is not a compound of itself
		{"nnnameat", false}, // a triple letter is a stuck key
	}
	for _, tc := range cases {
		if got := d.compound(tc.w); got != tc.want {
			t.Errorf("compound(%q) = %v, want %v", tc.w, got, tc.want)
		}
	}
	// The cap: five trusted parts is past maxCompoundParts.
	for _, w := range []string{"aa", "bb", "cc", "dd", "ee"} {
		d.Add(w)
	}
	if d.compound("aabbccddee") {
		t.Errorf("compound accepted %d parts; the cap is %d", 5, maxCompoundParts)
	}
	if !d.compound("aabbccdd") {
		t.Errorf("compound refused %d parts; the cap is %d", 4, maxCompoundParts)
	}
}

// TestRecase pins the three shapes a piece can have and that a correction
// is put back in.
func TestRecase(t *testing.T) {
	for _, tc := range []struct{ model, word, want string }{
		{"nmae", "name", "name"},
		{"Nmae", "name", "Name"},
		{"NMAE", "name", "NAME"},
		{"nmae", "Wednesday", "wednesday"}, // the list's capital is not the identifier's
		{"N", "name", "Name"},              // one capital is a Title piece, not SCREAMING
		{"", "name", "name"},
	} {
		if got := Recase(tc.model, tc.word); got != tc.want {
			t.Errorf("Recase(%q, %q) = %q, want %q", tc.model, tc.word, got, tc.want)
		}
	}
}

// TestSuggestPiece_FitsTheIdentifier: corrections come back in the
// piece's case, never hold a character an identifier cannot, and include
// half-repairs for a run-together name no single word fixes.
func TestSuggestPiece_FitsTheIdentifier(t *testing.T) {
	for _, tc := range []struct{ piece, want string }{
		{"nmae", "name"},
		{"Nmae", "Name"},
		{"NMAE", "NAME"},
		{"adress", "address"},
		{"Adress", "Address"},
	} {
		got := dict.SuggestPiece(tc.piece, 8)
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("SuggestPiece(%q) = %v, want %q first", tc.piece, got, tc.want)
		}
	}
	if got := dict.SuggestPiece("frstname", 8); !slices.Contains(got, "firstname") {
		t.Errorf("SuggestPiece(%q) = %v, want it to offer %q", "frstname", got, "firstname")
	}
	for _, s := range dict.SuggestPiece("dont", 8) {
		if strings.ContainsAny(s, "'’") || !isASCIIWord(s) {
			t.Errorf("SuggestPiece offered %q, which no identifier can hold", s)
		}
	}
	for _, s := range dict.SuggestPiece("adress", 8) {
		if dict.CheckIdent(s) != nil {
			t.Errorf("SuggestPiece offered %q, which would itself be flagged", s)
		}
	}
	if got := dict.SuggestPiece("nmae", 2); len(got) > 2 {
		t.Errorf("SuggestPiece ignored max: %v", got)
	}
	if got := dict.SuggestPiece("", 8); got != nil {
		t.Errorf("SuggestPiece(\"\") = %v, want nil", got)
	}
}
