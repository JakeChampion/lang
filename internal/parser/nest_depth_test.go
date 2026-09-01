package parser

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Deeply nested input used to take the process down: recursive descent ran
// out of goroutine stack, and a stack overflow is a fatal runtime error that
// no recover() can turn back into a parse error. It surfaced as a nightly
// FuzzCheck failure (#7941) — that target parses before it checks, so the
// fuzz worker died rather than reporting a case. Every construct that nests
// must now bottom out in a P005 diagnostic.
func TestDeepNestingIsADiagnosticNotACrash(t *testing.T) {
	// Well past maxNestDepth, and past the ~60k levels that exhausted the
	// stack before the guard existed.
	const n = 100000
	cases := []struct {
		name string
		src  string
	}{
		{"parens", "function f(): i32 { return " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + "; }"},
		{"unclosed parens", "function f(): i32 { return " + strings.Repeat("(", n) + "1; }"},
		{"array literals", "function f(): i32 { return " + strings.Repeat("[", n) + strings.Repeat("]", n) + "; }"},
		{"blocks", "function f(): i32 { " + strings.Repeat("{", n) + strings.Repeat("}", n) + " return 1; }"},
		{"unary minus", "function f(): i32 { return " + strings.Repeat("-", n) + "1; }"},
		{"unary not", "function f(): i32 { return " + strings.Repeat("!", n) + "1; }"},
		{"slice types", "function f(): " + strings.Repeat("[", n) + "i32" + strings.Repeat("]", n) + " { return 1; }"},
		{"tuple patterns", "function f(): i32 { match (x) { " + strings.Repeat("(", n) + "a" + strings.Repeat(", b)", n) + " => 1 } }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A crash here takes the test binary with it, so reaching the
			// assertion at all is most of what this proves.
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("%d levels parsed without error; expected P005", n)
			}
			if !strings.Contains(err.Error(), "nests deeper than") {
				t.Fatalf("got %v, want the P005 nesting-depth diagnostic", err)
			}
		})
	}
}

// The bound has to sit far above anything real. The deepest source in this
// repository reaches 183 when this was written; nesting an order of
// magnitude past that must still parse, or the guard has become a bug.
func TestNestingWellInsideTheBoundStillParses(t *testing.T) {
	for _, n := range []int{1, 10, 100, 500, 1000} {
		src := "function f(): i32 { return " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + "; }"
		if _, err := Parse(src); err != nil {
			t.Errorf("%d levels of parens: %v", n, err)
		}
	}
}

// TestRecursionGuardsCoverEveryCycle is the structural half of the fix.
// Guarding the functions that recurse today only holds while they stay the
// functions that recurse: a refactor routing a new cycle around them
// re-opens the crash, and no behavioural test would notice until a fuzzer
// found the shape again. So build the parser's call graph and require that
// deleting the enter()-guarded methods leaves it acyclic — i.e. every
// recursion cycle runs through a depth guard.
func TestRecursionGuardsCoverEveryCycle(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	calls := map[string]map[string]bool{} // parser method -> methods it calls
	guarded := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if calls[name] == nil {
				calls[name] = map[string]bool{}
			}
			goast.Inspect(fn.Body, func(n goast.Node) bool {
				sel, ok := n.(*goast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*goast.Ident); !ok || id.Name != "p" {
					return true
				}
				if sel.Sel.Name == "enter" {
					guarded[name] = true
				}
				calls[name][sel.Sel.Name] = true
				return true
			})
		}
	}
	if len(guarded) == 0 {
		t.Fatal("found no p.enter() call sites — this test's AST walk has broken")
	}

	// Kahn's algorithm over the call graph with the guarded methods deleted.
	// Anything left with a non-zero in-degree at the end sits on a cycle.
	live := func(m string) bool { return !guarded[m] && calls[m] != nil }
	indeg := map[string]int{}
	for f := range calls {
		if live(f) {
			indeg[f] = 0
		}
	}
	for f := range calls {
		if !live(f) {
			continue
		}
		for g := range calls[f] {
			if live(g) {
				indeg[g]++
			}
		}
	}
	var queue []string
	for f, d := range indeg {
		if d == 0 {
			queue = append(queue, f)
		}
	}
	settled := 0
	for len(queue) > 0 {
		f := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		settled++
		for g := range calls[f] {
			if !live(g) {
				continue
			}
			indeg[g]--
			if indeg[g] == 0 {
				queue = append(queue, g)
			}
		}
	}
	if settled != len(indeg) {
		var stuck []string
		for f, d := range indeg {
			if d > 0 {
				stuck = append(stuck, f)
			}
		}
		t.Errorf("recursion cycle(s) bypass every depth guard, so deep input can still "+
			"exhaust the stack; add p.enter()/defer p.leave() to one method per cycle. "+
			"Still on a cycle: %v", stuck)
	}
}
