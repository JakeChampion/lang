package parser

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeepNestingIsADiagnosticNotACrash(t *testing.T) {
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

func TestNestingWellInsideTheBoundStillParses(t *testing.T) {
	for _, n := range []int{1, 10, 100, 500, 1000} {
		src := "function f(): i32 { return " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + "; }"
		if _, err := Parse(src); err != nil {
			t.Errorf("%d levels of parens: %v", n, err)
		}
	}
}

func TestRecursionGuardsCoverEveryCycle(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	calls := map[string]map[string]bool{}
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
				if sel.Sel.Name == "enter" || sel.Sel.Name == "enterUnits" {
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
