package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestStreamResultColorlessTransform(t *testing.T) {
	src := `@import("test:dep/d", "body") async function body(): Stream[u8];
async function run(): i32 { let b: u8[] = body(); return b.len(); }
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check (stream result should collect to u8[]): %v", err)
	}
	sig, ok := info.FuncSigs["body"]
	if !ok {
		t.Fatalf("no FuncSig for body")
	}
	at, ok := sig.Result.(ast.ArrayType)
	if !ok {
		t.Fatalf("body's effective result should be rewritten to u8[]; got %T (%v)", sig.Result, sig.Result)
	}
	if n, ok := at.Elem.(ast.NumberType); !ok || n.NormalWidth() != 8 {
		t.Errorf("collected element type should be u8; got %v", at.Elem)
	}
}

func TestStreamParamColorlessTransform(t *testing.T) {
	src := `@import("test:dep/d", "sink") async function sink(s: Stream[u8]): i32;
async function run(): i32 { let xs: u8[] = [10 as u8, 20 as u8, 12 as u8]; return sink(xs); }
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check (stream param should accept u8[]): %v", err)
	}
	sig, ok := info.FuncSigs["sink"]
	if !ok || len(sig.Params) != 1 {
		t.Fatalf("no FuncSig (1 param) for sink; got %+v", sig)
	}
	at, ok := sig.Params[0].(ast.ArrayType)
	if !ok {
		t.Fatalf("sink's effective param should be rewritten to u8[]; got %T (%v)", sig.Params[0], sig.Params[0])
	}
	if n, ok := at.Elem.(ast.NumberType); !ok || n.NormalWidth() != 8 {
		t.Errorf("param element type should be u8; got %v", at.Elem)
	}
}
