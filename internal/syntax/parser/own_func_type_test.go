package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func parseTypeOfParam(t *testing.T, src string) ast.Type {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Funcs) == 0 || len(prog.Funcs[0].Params) == 0 {
		t.Fatalf("no parameter to read")
	}
	return prog.Funcs[0].Params[0].Type
}

// `own` inside a function type marks a consuming slot, and it is part of the
// type's spelling — so a consuming function value cannot be handed to a
// lending position.
func TestFuncTypeParsesOwnParam(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(g: (own i32[], string) => i32): void {}`)
	ft, ok := ty.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected a function type, got %T", ty)
	}
	if !ft.OwnAt(0) || ft.OwnAt(1) {
		t.Fatalf("own flags = %v, want [true false]", ft.ParamOwn)
	}
	if got, want := ft.String(), "(own i32[], string) => i32"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// A consuming slot over a primitive is the marker and the primitive, never a
// handle: `own` reads as a handle only where it does not open a slot.
func TestFuncTypeOwnSlotOverPrimitive(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(g: (own string) => i32): void {}`)
	ft, ok := ty.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected a function type, got %T", ty)
	}
	if !ft.OwnAt(0) {
		t.Fatalf("own flags = %v, want the slot consuming", ft.ParamOwn)
	}
	if _, ok := ft.Params[0].(ast.StringType); !ok {
		t.Errorf("slot type = %T (%v), want string", ft.Params[0], ft.Params[0])
	}
}

func TestFuncTypeWithoutOwnHasNoFlags(t *testing.T) {
	ty := parseTypeOfParam(t, `function f(g: (i32[]) => i32): void {}`)
	ft := ty.(*ast.FuncType)
	if ft.AnyOwn() || ft.ParamOwn != nil {
		t.Fatalf("own flags = %v, want none", ft.ParamOwn)
	}
}

// `own R` is also a resource-HANDLE type. The consuming marker is only read
// where a `=>` follows the matching `)`, so a grouped or tuple type keeps the
// handle meaning it had.
func TestOwnHandleTypeInParensUnchanged(t *testing.T) {
	ty := parseTypeOfParam(t, `resource Conn;
function f(h: (own Conn)): void {}`)
	if _, ok := ty.(ast.HandleType); !ok {
		t.Fatalf("expected a handle type, got %T (%v)", ty, ty)
	}
}

// Outside a function type's parameter slot, `own` / `borrow` in a type is a
// resource handle and must be followed by the resource's name. Anything else is
// a P001 at the word, pointing at the parameter-modifier spelling.
func TestOwnBorrowInTypeNeedsResourceName(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
	}{
		{"let annotation", "function main(): i32 { let x: own i32[] = [1]; return x.len(); }", 1, 31},
		{"parameter type", "function f(xs: own i32[]): i32 { return xs.len(); }", 1, 16},
		{"borrow parameter", "function f(s: borrow string): i32 { return s.len(); }", 1, 15},
		{"return type", "function f(): own string { return \"\"; }", 1, 15},
		{"tuple element", "function f(p: (own i32[], i32)): i32 { return 0; }", 1, 16},
		{"function-type result", "function f(g: (i32) => own string): i32 { return 0; }", 1, 24},
		{"type argument", "function f(o: Option[own i32]): i32 { return 0; }", 1, 22},
		{"view element", "function f(v: [own i32]): i32 { return 0; }", 1, 16},
		{"empty function-type slot", "function f(g: (own) => i32): i32 { return 0; }", 1, 16},
		{"bare word", "function main(): i32 { let x: own = 1; return 0; }", 1, 31},
		{"bare word parameter", "function f(xs: own): i32 { return 0; }", 1, 16},
		{"bare word array", "function main(): i32 { let x: own[] = []; return 0; }", 1, 31},
		{"borrow in a function-type slot", "function f(g: (borrow i32) => i32): i32 { return 0; }", 1, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.src)
			var batch diag.Errors
			var pe *Error
			if !errors.As(err, &batch) || len(batch) != 1 || !errors.As(batch[0], &pe) {
				t.Fatalf("got %v, want exactly one parse error", err)
			}
			if pe.ErrCode != "P001" || !strings.Contains(pe.Msg, "must be followed by a resource name") {
				t.Errorf("got %s %q, want P001 naming the resource requirement", pe.ErrCode, pe.Msg)
			}
			if pe.Pos.Line != c.line || pe.Pos.Col != c.col {
				t.Errorf("at %d:%d, want %d:%d", pe.Pos.Line, pe.Pos.Col, c.line, c.col)
			}
		})
	}
}

// A resource is module-local, so `pub` and `pub(package)` do not apply to one.
// Both are refused rather than parsed and dropped, which would let a formatter
// print the declaration without them.
func TestPubResourceIsRejected(t *testing.T) {
	for _, src := range []string{"pub resource Conn;\n", "pub(package) resource Conn;\n"} {
		_, err := Parse(src)
		if got := firstCode(err); got != "P001" {
			t.Errorf("%q: got %v, want P001", src, err)
		}
	}
}

// A lambda spells a consuming parameter the way a declaration does.
func TestArrowLambdaParsesOwnParam(t *testing.T) {
	prog, err := Parse(`function f(): i32 {
    let g: (own i32[]) => i32 = (own xs: i32[]) => xs.len();
    return g([1]);
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var lam *ast.Lambda
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if l, ok := n.(*ast.Lambda); ok && lam == nil {
			lam = l
		}
		return true
	})
	if lam == nil {
		t.Fatal("no lambda parsed")
	}
	if len(lam.Params) != 1 || !lam.Params[0].Own {
		t.Fatalf("lambda params = %+v, want one own param", lam.Params)
	}
}
