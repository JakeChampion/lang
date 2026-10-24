package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestOwnedArrayLendsIntoViewParam(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"concrete element", `function total(bs: [u8]): i32 { return bs.len(); }
function main(): i32 { let owned: u8[] = [1, 2, 3]; return total(owned); }`},
		{"not u8-specific", `function total(bs: [i32]): i32 { return bs.len(); }
function main(): i32 { let owned: i32[] = [1, 2, 3]; return total(owned); }`},
		{"generic view parameter", `function total[T](bs: [T]): i32 { return bs.len(); }
function main(): i32 { let owned: u8[] = [1, 2, 3]; return total(owned); }`},
		{"array literal at a view parameter", `function total(bs: [u8]): i32 { return bs.len(); }
function main(): i32 { return total([1, 2, 3]); }`},
		{"method argument", `struct Sink { n: i32 }
function (s: Sink) take(bs: [u8]): i32 { return bs.len() + s.n; }
function main(): i32 { let owned: u8[] = [1]; let s = Sink { n: 0 }; return s.take(owned); }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("check: %v", err)
			}
		})
	}
}

func TestOwnedArrayLendsIntoLocalView(t *testing.T) {
	cases := []struct{ name, src string }{
		{"local owner", `function main(): i32 { let owned: u8[] = [1, 2]; let v: [u8] = owned; return v.len(); }`},
		{"array literal", `function main(): i32 { let v: [u8] = [3 as u8, 250 as u8, 7 as u8]; return v[0] as i32; }`},
		{"literal element inference", `function main(): i32 { let v: [u8] = [3, 250, 7]; return v.len(); }`},
		{"empty literal", `function main(): i32 { let v: [u8] = []; return v.len(); }`},
		{"temporary owner", `function make(): u8[] { return [1, 2]; } function main(): i32 { let v: [u8] = make(); return v.len(); }`},
		{"generic element", `function count[T](owned: T[]): i32 { let v: [T] = owned; return v.len(); } function main(): i32 { return count([1, 2]); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, tc.src); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}

func TestArrayViewLendIsOneWayAndNonConsuming(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"view does not promote to owned", `function takes_owned(bs: u8[]): i32 { return bs.len(); }
function main(): i32 { let owned: u8[] = [1, 2]; let v: [u8] = owned[:]; return takes_owned(v); }`,
			"expected u8[], got [u8]"},
		{"own parameter consumes, so no lend", `function consume(own bs: [u8]): i32 { return bs.len(); }
function main(): i32 { let owned: u8[] = [1, 2]; return consume(owned); }`,
			"expected [u8], got u8[]"},
		{"local element types must match", `function main(): i32 { let owned: i32[] = [1, 2]; let v: [i64] = owned; return v.len(); }`,
			"expected [i64], got i32[]"},
		{"element types must match exactly", `function total(bs: [i64]): i32 { return bs.len(); }
function main(): i32 { let owned: i32[] = [1, 2]; return total(owned); }`,
			"expected [i64], got i32[]"},
		{"local storage cannot escape", `function bad(): [u8] { let owner: u8[] = [1]; let v: [u8] = owner; return v; } function main(): i32 { return 0; }`,
			"function-local storage"},
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

func TestArrayViewLendRewritesArgumentToFullRangeSlice(t *testing.T) {
	const src = `function total(bs: [u8]): i32 { return bs.len(); }
function main(): i32 { let owned: u8[] = [1, 2, 3]; return total(owned); }`
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
