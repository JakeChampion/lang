package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestUnsafeSyntaxMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, prefix    string
		pub, fip, async bool
	}{
		{"plain", "", false, false, false},
		{"public", "pub ", true, false, false},
		{"fip", "fip ", false, true, false},
		{"async", "async ", false, false, true},
		{"attribute", "@inline\n", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := Parse(tc.prefix + "unsafe function answer(): i32 { unsafe { return 42; } }")
			if err != nil {
				t.Fatal(err)
			}
			if len(prog.Funcs) != 1 {
				t.Fatalf("got %d functions", len(prog.Funcs))
			}
			fn := prog.Funcs[0]
			if !fn.Unsafe || fn.Public != tc.pub || fn.Fip != tc.fip || fn.Async != tc.async {
				t.Fatalf("lost declaration modifiers: %+v", fn)
			}
			if len(fn.Body.Stmts) != 1 {
				t.Fatalf("got %d body statements", len(fn.Body.Stmts))
			}
			block, ok := fn.Body.Stmts[0].(*ast.Block)
			if !ok || !block.IsUnsafe || fn.Body.IsUnsafe {
				t.Fatalf("lost the explicit unsafe block: %#v", fn.Body.Stmts[0])
			}
			clone := ast.CloneBlock(fn.Body)
			if !clone.Stmts[0].(*ast.Block).IsUnsafe || clone.Stmts[0] == block {
				t.Fatal("cloning lost the unsafe scope or retained its original block")
			}
		})
	}
}

func TestUnsafeRemainsAContextualIdentifier(t *testing.T) {
	prog, err := Parse("function unsafe(): i32 { let unsafe: i32 = 42; return unsafe; }")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Funcs) != 1 || prog.Funcs[0].Name != "unsafe" || prog.Funcs[0].Unsafe {
		t.Fatal("unsafe was treated as a modifier in an identifier position")
	}
}

func TestUnsafeNestedFunctionRefused(t *testing.T) {
	if _, err := Parse("function main(): i32 { unsafe function inner(): i32 { return 42; } return inner(); }"); err == nil {
		t.Fatal("unsupported unsafe nested function was accepted")
	}
}

func TestUnsafeBlockAtABranchTail(t *testing.T) {
	prog, err := Parse("function f(n: i32): i32 { match (n) { 0 => { unsafe { return 1; } }, _ => { return 2; } } }")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("got %T, want a match statement", prog.Funcs[0].Body.Stmts[0])
	}
	if block, ok := m.Arms[0].Body.Stmts[0].(*ast.Block); !ok || !block.IsUnsafe {
		t.Fatalf("an unsafe block with no value is not a statement: %#v", m.Arms[0].Body.Stmts[0])
	}
	prog, err = Parse("function g(): i32 { let v = { let a = 1; unsafe { a + 1 } }; return v; }")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("got %T, want a let", prog.Funcs[0].Body.Stmts[0])
	}
	outer, ok := v.Init.(*ast.BlockExpr)
	if !ok {
		t.Fatalf("got %T, want a value block", v.Init)
	}
	if inner, ok := outer.Tail.(*ast.BlockExpr); !ok || !inner.Unsafe {
		t.Fatalf("an unsafe block that ends in a value is not the tail value: %#v", outer.Tail)
	}
}
