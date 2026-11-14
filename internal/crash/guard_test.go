// =============================================================================
// File: internal/crash/guard_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package crash

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// panicsInBackground starts a guarded goroutine that panics with v —
// the shape every ced goroutine has.
func panicsInBackground(v any) {
	go func() {
		defer Recover()
		panic(v)
	}()
}

// TestRecover_HandsThePanicToTheHandler is the core promise: a panic in
// a guarded goroutine reaches the installed handler with its value and
// the stack of the frame that panicked, instead of killing the process.
func TestRecover_HandsThePanicToTheHandler(t *testing.T) {
	got := make(chan *Panic, 1)
	restore := SetHandler(func(p *Panic) { got <- p })
	defer restore()

	panicsInBackground("boom")

	select {
	case p := <-got:
		if p.Value != "boom" {
			t.Fatalf("Value = %v, want boom", p.Value)
		}
		if !strings.Contains(string(p.Stack), "panicsInBackground") {
			t.Fatalf("stack does not name the panicking frame:\n%s", p.Stack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never received the panic")
	}
}

// TestRecover_NoPanicNeverCallsTheHandler pins the quiet path: a
// goroutine that ends normally must not look like a crash.
func TestRecover_NoPanicNeverCallsTheHandler(t *testing.T) {
	called := make(chan struct{}, 1)
	restore := SetHandler(func(*Panic) { called <- struct{}{} })
	defer restore()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer Recover()
	}()
	<-done
	select {
	case <-called:
		t.Fatal("handler called for a goroutine that did not panic")
	default:
	}
}

// TestRecover_WithoutAHandlerRepanicsTheOriginalValue pins the fallback:
// with no editor to rescue for, the panic continues exactly as Go would
// have raised it — same value, not the wrapper.
func TestRecover_WithoutAHandlerRepanicsTheOriginalValue(t *testing.T) {
	restore := SetHandler(nil)
	defer restore()

	var got any
	func() {
		defer func() { got = recover() }()
		func() {
			defer Recover()
			panic("raw")
		}()
	}()
	if got != "raw" {
		t.Fatalf("re-panicked with %#v, want the original \"raw\"", got)
	}
}

// TestSetHandler_RestoreOnlyRemovesItsOwnHandler pins the folder-switch
// safety: an App closing after a newer one installed its handler must
// not uninstall the newer one.
func TestSetHandler_RestoreOnlyRemovesItsOwnHandler(t *testing.T) {
	var hits []string
	restoreA := SetHandler(func(*Panic) { hits = append(hits, "A") })
	restoreB := SetHandler(func(*Panic) { hits = append(hits, "B") })
	defer restoreB()

	restoreA() // A is no longer installed — this must be a no-op

	h := handler.Load()
	if h == nil || *h == nil {
		t.Fatal("restoring A uninstalled B")
	}
	(*h)(&Panic{})
	if len(hits) != 1 || hits[0] != "B" {
		t.Fatalf("installed handler ran %v, want [B]", hits)
	}
}

// TestPanic_ErrorCarriesTheGoroutineStack pins that a re-raised Panic
// prints the ORIGINAL goroutine's trace — the main loop's own trace
// would only show the event dispatch.
func TestPanic_ErrorCarriesTheGoroutineStack(t *testing.T) {
	p := &Panic{Value: errors.New("nil map"), Stack: []byte("goroutine 7 [running]:\nmain.worker()")}
	msg := p.Error()
	if !strings.HasPrefix(msg, "nil map [in a background goroutine]") || !strings.Contains(msg, "main.worker()") {
		t.Fatalf("Error() = %q", msg)
	}
}

// TestFirstLine_UnwrapsAndTrims pins the one-line summary used for the
// exit note and INDEX.txt.
func TestFirstLine_UnwrapsAndTrims(t *testing.T) {
	if got := FirstLine("line one\nline two"); got != "line one" {
		t.Errorf("FirstLine = %q, want %q", got, "line one")
	}
	if got := FirstLine(&Panic{Value: "inner"}); got != "inner" {
		t.Errorf("FirstLine(*Panic) = %q, want the wrapped value", got)
	}
	if got := (&Panic{Value: 42}).Summary(); got != "42" {
		t.Errorf("Summary = %q, want 42", got)
	}
}

// TestEveryGoroutineRecovers walks ced's non-test Go source and requires
// every `go` statement to start a guarded goroutine: either a function
// literal whose FIRST statement is `defer crash.Recover()`, or a named
// function/method (or a `name := func…` closure) of the same package
// whose body starts that way. A new goroutine without the guard would
// take the editor down with the terminal still in raw mode and the
// user's edits unrescued — the N-118 crash this package exists for.
func TestEveryGoroutineRecovers(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found from the test's directory: %v", err)
	}
	pkgs := map[string][]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Hidden dirs (.git, .claude's helper programs), docs and
			// fixtures are not the editor.
			if p != root && (strings.HasPrefix(name, ".") || name == "ai_docs" || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		pkgs[filepath.Dir(p)] = append(pkgs[filepath.Dir(p)], f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	for dir, files := range pkgs {
		bodies := namedBodies(files)
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				g, ok := n.(*ast.GoStmt)
				if !ok {
					return true
				}
				checked++
				where := fset.Position(g.Pos())
				switch fn := g.Call.Fun.(type) {
				case *ast.FuncLit:
					if !startsGuarded(fn.Body) {
						t.Errorf("%s: goroutine does not start with `defer crash.Recover()`", where)
					}
				default:
					name := calleeName(fn)
					found := bodies[name]
					if len(found) == 0 {
						t.Errorf("%s: `go %s(…)` names nothing declared in %s — wrap it in a func literal that defers crash.Recover()", where, name, dir)
					}
					for _, b := range found {
						if !startsGuarded(b) {
							t.Errorf("%s: `go %s(…)`: %s does not start with `defer crash.Recover()`", where, name, name)
						}
					}
				}
				return true
			})
		}
	}
	// The walk itself is part of the pin: a broken root or filter that
	// finds no goroutines would pass vacuously.
	if checked < 50 {
		t.Fatalf("only %d go statements found — is the walk looking in the right place?", checked)
	}
}

// namedBodies maps every function, method and `name := func…` closure in
// a package to its body (several when a name is reused).
func namedBodies(files []*ast.File) map[string][]*ast.BlockStmt {
	out := map[string][]*ast.BlockStmt{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if x.Body != nil {
					out[x.Name.Name] = append(out[x.Name.Name], x.Body)
				}
			case *ast.AssignStmt:
				for i, rhs := range x.Rhs {
					lit, ok := rhs.(*ast.FuncLit)
					if !ok || i >= len(x.Lhs) {
						continue
					}
					if id, ok := x.Lhs[i].(*ast.Ident); ok {
						out[id.Name] = append(out[id.Name], lit.Body)
					}
				}
			}
			return true
		})
	}
	return out
}

// calleeName is the name a `go` statement calls: f for f(), m for x.m().
func calleeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// startsGuarded reports whether a body's first statement is
// `defer crash.Recover()` (or `defer Recover()` inside this package).
func startsGuarded(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	d, ok := b.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	switch fn := d.Call.Fun.(type) {
	case *ast.SelectorExpr:
		x, ok := fn.X.(*ast.Ident)
		return ok && x.Name == "crash" && fn.Sel.Name == "Recover"
	case *ast.Ident:
		return fn.Name == "Recover"
	}
	return false
}
