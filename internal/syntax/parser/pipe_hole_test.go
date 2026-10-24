package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func pipeCallOfReturn(t *testing.T, src string) *ast.Call {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name != "main" {
			continue
		}
		ret, ok := fn.Body.Stmts[len(fn.Body.Stmts)-1].(*ast.Return)
		if !ok {
			t.Fatalf("last stmt = %T, want *ast.Return", fn.Body.Stmts[len(fn.Body.Stmts)-1])
		}
		call, ok := ret.Value.(*ast.Call)
		if !ok {
			t.Fatalf("return value = %T, want *ast.Call", ret.Value)
		}
		return call
	}
	t.Fatal("main not found")
	return nil
}

func TestParsePipeHoleSubstitutes(t *testing.T) {
	call := pipeCallOfReturn(t, `function main(): i32 { let x: i32 = 3; return x |> f(10, _); }`)
	if !call.IsPipe {
		t.Fatal("IsPipe = false, want true")
	}
	if call.PipeHole != 2 {
		t.Fatalf("PipeHole = %d, want 2", call.PipeHole)
	}
	if len(call.Args) != 2 {
		t.Fatalf("len(Args) = %d, want 2", len(call.Args))
	}
	if lit, ok := call.Args[0].(*ast.NumberLit); !ok || lit.Value != 10 {
		t.Errorf("Args[0] = %#v, want NumberLit 10", call.Args[0])
	}
	if id, ok := call.Args[1].(*ast.Ident); !ok || id.Name != "x" {
		t.Errorf("Args[1] = %#v, want Ident x (the substituted LHS)", call.Args[1])
	}
}

func TestParsePipeNoHolePrepends(t *testing.T) {
	call := pipeCallOfReturn(t, `function main(): i32 { let x: i32 = 3; return x |> f(10); }`)
	if call.PipeHole != 0 {
		t.Fatalf("PipeHole = %d, want 0 (prepended form)", call.PipeHole)
	}
	if id, ok := call.Args[0].(*ast.Ident); !ok || id.Name != "x" {
		t.Errorf("Args[0] = %#v, want Ident x (prepended LHS)", call.Args[0])
	}
}

func TestParsePipeTwoHolesRejected(t *testing.T) {
	_, err := Parse(`function main(): i32 { return 1 |> f(_, _); }`)
	if err == nil {
		t.Fatal("two `_` placeholders should be a parse error")
	}
	if !strings.Contains(err.Error(), "at most one `_` placeholder") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestParsePipeNestedHolesCompose(t *testing.T) {
	call := pipeCallOfReturn(t, `function main(): i32 { let x: i32 = 3; return 20 |> f(_, x |> f(5, _)); }`)
	if call.PipeHole != 1 {
		t.Fatalf("outer PipeHole = %d, want 1", call.PipeHole)
	}
	inner, ok := call.Args[1].(*ast.Call)
	if !ok || !inner.IsPipe {
		t.Fatalf("Args[1] = %#v, want inner piped Call", call.Args[1])
	}
	if inner.PipeHole != 2 {
		t.Errorf("inner PipeHole = %d, want 2", inner.PipeHole)
	}
}

func TestNonPipedUnderscoreUntouched(t *testing.T) {
	call := pipeCallOfReturn(t, `function main(): i32 { return f(10, _); }`)
	if call.IsPipe || call.PipeHole != 0 {
		t.Fatalf("plain call got pipe fields set: IsPipe=%v PipeHole=%d", call.IsPipe, call.PipeHole)
	}
	if id, ok := call.Args[1].(*ast.Ident); !ok || id.Name != "_" {
		t.Errorf("Args[1] = %#v, want bare Ident _", call.Args[1])
	}
}
