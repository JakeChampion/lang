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
