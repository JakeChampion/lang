package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestForEachDesugarsToIndexLoop(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 {
		let sum: i32 = 0;
		for x in [1, 2, 3] { sum = sum + x; }
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	blk, ok := prog.Funcs[0].Body.Stmts[1].(*ast.Block)
	if !ok {
		t.Fatalf("ForEach should lower to a Block, got %T", prog.Funcs[0].Body.Stmts[1])
	}
	if len(blk.Stmts) != 4 {
		t.Fatalf("expected 4 inner stmts (iter / len / idx / for), got %d", len(blk.Stmts))
	}
	loop, ok := blk.Stmts[3].(*ast.For)
	if !ok {
		t.Fatalf("last stmt should be a For loop (so continue hits the step), got %T", blk.Stmts[3])
	}
	if loop.Step == nil {
		t.Errorf("desugared For must carry a step so `continue` advances the index")
	}
}

func TestForEachNestedDesugarUniqueSlots(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 {
		let s: i32 = 0;
		for a in [1, 2] {
			for b in [3, 4] { s = s + a + b; }
		}
		return s;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	seen := map[string]bool{}
	var walk func(s ast.Stmt)
	walk = func(s ast.Stmt) {
		switch x := s.(type) {
		case *ast.Block:
			for _, c := range x.Stmts {
				walk(c)
			}
		case *ast.For:
			walk(x.Body)
		case *ast.Var:
			if strings.HasPrefix(x.Name, "__foreach_") {
				if seen[x.Name] {
					t.Errorf("duplicate synthetic slot name %q across nested loops", x.Name)
				}
				seen[x.Name] = true
			}
		}
	}
	for _, s := range prog.Funcs[0].Body.Stmts {
		walk(s)
	}
	if len(seen) != 6 {
		t.Errorf("expected 6 synthetic slot names (3 per nested loop), got %d: %v", len(seen), seen)
	}
}

func TestForEachInsideNestedFuncDesugars(t *testing.T) {
	prog, err := parser.Parse(`function total(xs: i32[]): i32 {
		function sum(acc: i32): i32 {
			let n: i32 = acc;
			for x in xs { n = n + x; }
			return n;
		}
		return sum(0);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var found func(s ast.Stmt) bool
	found = func(s ast.Stmt) bool {
		switch x := s.(type) {
		case *ast.ForEach:
			return true
		case *ast.Block:
			for _, in := range x.Stmts {
				if found(in) {
					return true
				}
			}
		case *ast.FuncDecl:
			return found(x.Body)
		case *ast.For:
			return found(x.Body)
		}
		return false
	}
	if found(prog.Funcs[0].Body) {
		t.Error("a ForEach survived inside the nested function's body; IR cannot lower one")
	}
}

func TestForEachOverIteratorLowersToNextLoop(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "core/iter";
function main(): i32 {
	let n: i32 = 0;
	let r = 0..3;
	for x in r { n = n + x; }
	return n;
}`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var main *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "main" {
			main = fn
		}
	}
	blk, ok := main.Body.Stmts[2].(*ast.Block)
	if !ok || len(blk.Stmts) != 2 {
		t.Fatalf("the foreach should lower to a Block of the iterand binding and a loop, got %#v", main.Body.Stmts[2])
	}
	loop, ok := blk.Stmts[1].(*ast.Loop)
	if !ok {
		t.Fatalf("second statement should be the Loop, got %T", blk.Stmts[1])
	}
	pull, ok := loop.Body.(*ast.Block).Stmts[0].(*ast.Match)
	if !ok || len(pull.Arms) != 2 || pull.Arms[0].VariantName != "Some" || pull.Arms[1].VariantName != "None" {
		t.Fatalf("the loop should match next() on Some / None, got %#v", loop.Body)
	}
}

func TestForEachNotIterableE083(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"struct", "struct Point { x: i32, y: i32 }\nfunction main(): i32 { let p: Point = Point { x: 1, y: 2 }; for c in p { } return 0; }\n", "type Point"},
		{"scalar", "function main(): i32 { for c in 42 { } return 0; }\n", "type i32"},
		{"unbounded-type-parameter", "function each[T](v: T): i32 { for x in v { } return 0; }\nfunction main(): i32 { return each(1); }\n", "type T"},
		{"iter-returns-no-iterator", "struct Counter { n: i32 }\nfunction (c: Counter) iter(): i32 { return c.n; }\nfunction main(): i32 { let k: Counter = Counter { n: 3 }; for n in k { } return 0; }\n", "type Counter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if err == nil {
				t.Fatal("want E083, got a clean check")
			}
			msg := err.Error()
			if !hasCode(err, "E083") || !strings.Contains(msg, "cannot iterate over a value of "+tc.want) {
				t.Errorf("want E083 naming %q, got:\n%v", tc.want, msg)
			}
			if hasCode(err, "E034") || hasCode(err, "E043") {
				t.Errorf("the array lowering's diagnostics leaked:\n%v", msg)
			}
		})
	}
}

func TestForEachOverIteratorChecks(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"adapter-chain", "import \"core/iter\";\nfunction main(): i32 { let n: i32 = 0; for x in iter.range(0, 9).filter((v: i32): boolean => v > 2) { n = n + x; } return n; }\n"},
		{"pattern-header", "import \"core/iter\";\nfunction main(): i32 { let n: i32 = 0; for (i, w) in iter.of([\"a\"]).enumerate() { n = n + i + w.len(); } return n; }\n"},
		{"bounded-type-parameter", "import \"core/iter\";\nfunction total[I: iter.Iterator[i32]](it: I): i32 { let n: i32 = 0; for v in it { n = n + v; } return n; }\nfunction main(): i32 { return total(iter.range(0, 4)); }\n"},
		{"iter-method", "import \"core/iter\";\nstruct Bag { xs: string[] }\nfunction (b: Bag) iter(): iter.ArrayIter[string] { return iter.of(b.xs); }\nfunction main(): i32 { let n: i32 = 0; let b: Bag = Bag { xs: [\"a\"] }; for s in b { n = n + s.len(); } return n; }\n"},
		{"ordmap", "import \"std/ordmap\";\nfunction main(): i32 { let m: ordmap.OrdMap[string, i32] = ordmap.ordmap_new(); m = m.insert(\"a\", 1); let n: i32 = 0; for (k, v) in m { n = n + k.len() + v; } return n; }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkModuleSource(t, tc.src); err != nil {
				t.Fatalf("want a clean check, got:\n%v", err)
			}
		})
	}
	err := checkModuleSource(t, "import \"core/iter\";\nfunction main(): i32 { for s in iter.of([\"a\"]) { let n: i32 = s; } return 0; }\n")
	if !hasCode(err, "E003") {
		t.Errorf("the element should bind at the Iterator's element type (string), got: %v", err)
	}
}

func TestForEachInDeferLowers(t *testing.T) {
	prog, err := parser.Parse(`function f(xs: i32[]): i32 { defer { for x in xs { let y: i32 = x; } } return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if _, ok := n.(*ast.ForEach); ok {
			t.Fatalf("a ForEach survived the check inside a defer")
		}
		return true
	})
	err = checkSource(t, "struct S { n: i32 }\nfunction main(): i32 { let xs: i32[] = [1]; defer { for y in xs { let x: i32 = S { n: y }; } } return 0; }\n")
	if !hasCode(err, "E003") {
		t.Errorf("the loop body inside a defer should be checked, got: %v", err)
	}
}
