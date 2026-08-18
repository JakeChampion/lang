package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// An owned `T[]` lends into a borrowed `[T]` PARAMETER, the array-side
// counterpart of the `str` → `string` borrow (#4813). Before #6798 the caller
// had to spell `xs[0:xs.len()]`, which is why `[T]` appeared exactly once in
// the whole stdlib: a view-taking API was hostile to its callers.
func TestOwnedArrayLendsIntoViewParam(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"concrete element", `function total(bs: [u8]): i32 { return bs.len(); }
function main(): i32 { var owned: u8[] = [1, 2, 3]; return total(owned); }`},
		{"not u8-specific", `function total(bs: [i32]): i32 { return bs.len(); }
function main(): i32 { var owned: i32[] = [1, 2, 3]; return total(owned); }`},
		{"generic view parameter", `function total[T](bs: [T]): i32 { return bs.len(); }
function main(): i32 { var owned: u8[] = [1, 2, 3]; return total(owned); }`},
		{"array literal at a view parameter", `function total(bs: [u8]): i32 { return bs.len(); }
function main(): i32 { return total([1, 2, 3]); }`},
		{"method argument", `struct Sink { n: i32 }
function (s: Sink) take(bs: [u8]): i32 { return bs.len() + s.n; }
function main(): i32 { var owned: u8[] = [1]; var s = Sink { n: 0 }; return s.take(owned); }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("check: %v", err)
			}
		})
	}
}

// The lend is one-directional and parameter-only. A view never promotes back
// to an owned array (the callee would free storage it does not own), an `own`
// parameter consumes its argument so lending to it is the #4294 corruption
// shape, and an owning sink outside an argument position keeps the strict
// rule — the source array's lifetime is not tied to the binding.
func TestArrayViewLendIsParameterOnlyAndOneWay(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"view does not promote to owned", `function takes_owned(bs: u8[]): i32 { return bs.len(); }
function main(): i32 { var owned: u8[] = [1, 2]; var v: [u8] = owned[:]; return takes_owned(v); }`,
			"expected u8[], got [u8]"},
		{"own parameter consumes, so no lend", `function consume(own bs: [u8]): i32 { return bs.len(); }
function main(): i32 { var owned: u8[] = [1, 2]; return consume(owned); }`,
			"expected [u8], got u8[]"},
		{"var initialiser is an owning sink", `function main(): i32 { var owned: u8[] = [1, 2]; var v: [u8] = owned; return v.len(); }`,
			"cannot assign u8[] to variable of type [u8]"},
		{"element types must match exactly", `function total(bs: [i64]): i32 { return bs.len(); }
function main(): i32 { var owned: i32[] = [1, 2]; return total(owned); }`,
			"expected [i64], got i32[]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// The lend is a real coercion, not just a typing relaxation: `T[]` is the
// array while `[T]` is a `{data_ptr, len}` pair, so the checker rewrites the
// accepted argument into the full-range slice `xs[:]`. Every backend, the
// interpreter, and the rc passes then see the shape they already handle for
// an explicit slice — nothing downstream needs to learn about the coercion.
func TestArrayViewLendRewritesArgumentToFullRangeSlice(t *testing.T) {
	const src = `function total(bs: [u8]): i32 { return bs.len(); }
function main(): i32 { var owned: u8[] = [1, 2, 3]; return total(owned); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	ret, ok := prog.Funcs[1].Body.Stmts[1].(*ast.Return)
	if !ok {
		t.Fatalf("second statement is %T, want *ast.Return", prog.Funcs[1].Body.Stmts[1])
	}
	call, ok := ret.Value.(*ast.Call)
	if !ok {
		t.Fatalf("returned value is %T, want *ast.Call", ret.Value)
	}
	sl, ok := call.Args[0].(*ast.SliceExpr)
	if !ok {
		t.Fatalf("argument is %T, want *ast.SliceExpr (the materialised view)", call.Args[0])
	}
	if sl.Low != nil || sl.High != nil {
		t.Errorf("bounds = (%v, %v), want both nil (the full range)", sl.Low, sl.High)
	}
	if !ast.Equal(sl.ElemType, ast.NumberType{Width: 8, Signed: false, Spelling: "u8"}) {
		t.Errorf("ElemType = %v, want u8", sl.ElemType)
	}
	if id, ok := sl.Source.(*ast.Ident); !ok || id.Name != "owned" {
		t.Errorf("Source = %#v, want ident owned", sl.Source)
	}
}
