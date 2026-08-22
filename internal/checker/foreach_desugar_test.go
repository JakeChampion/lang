package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// The plain `for x in expr` form parses to an ast.ForEach (parser tests), which
// `desugarForEachProgram` lowers to the `.len()` + index C-style loop at the end
// of the parse. These tests pin that the lowering reproduces it exactly (Block of
// iter/len/idx + a For carrying a step so `continue` advances), and mints unique
// slot names for nested loops.
func TestForEachDesugarsToIndexLoop(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 {
		var sum: i32 = 0;
		for x in [1, 2, 3] { sum = sum + x; }
		return sum;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	// After Check, the ForEach has been replaced by the desugared Block.
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
		var s: i32 = 0;
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
