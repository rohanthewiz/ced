// =============================================================================
// File: internal/lsp/progress_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"testing"
)

// TestParseProgress pins the normal form: string and integer tokens both
// key, an absent percentage is -1 rather than a false 0%, and a partial
// RESULT riding the same notification is not mistaken for work-done
// progress.
func TestParseProgress(t *testing.T) {
	p, ok := ParseProgress(json.RawMessage(`{"token":"abc","value":{"kind":"begin","title":"Loading packages","percentage":0}}`))
	if !ok || p.Token != `"abc"` || p.Kind != ProgressBegin || p.Title != "Loading packages" || p.Percent != 0 {
		t.Errorf("begin = %+v ok=%v", p, ok)
	}
	p, ok = ParseProgress(json.RawMessage(`{"token":7,"value":{"kind":"report","message":"12/40"}}`))
	if !ok || p.Token != "7" || p.Message != "12/40" || p.Percent != -1 {
		t.Errorf("report = %+v ok=%v", p, ok)
	}
	if _, ok := ParseProgress(json.RawMessage(`{"token":"r","value":[{"uri":"file:///a.go"}]}`)); ok {
		t.Error("a partial result is not work-done progress")
	}
	if _, ok := ParseProgress(json.RawMessage(`{"value":{"kind":"end"}}`)); ok {
		t.Error("a report with no token cannot be tracked")
	}
}

// TestParseShowMessage pins the message decode and that an empty one is
// refused — there would be nothing to show.
func TestParseShowMessage(t *testing.T) {
	typ, text, ok := ParseShowMessage(json.RawMessage(`{"type":1,"message":"go.mod not found"}`))
	if !ok || typ != MessageError || text != "go.mod not found" {
		t.Errorf("got %d %q %v", typ, text, ok)
	}
	if _, _, ok := ParseShowMessage(json.RawMessage(`{"type":3,"message":""}`)); ok {
		t.Error("an empty message should be refused")
	}
}
