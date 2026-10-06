package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// errCodes joins the codes of a checker error's diagnostics in report order.
func errCodes(err error) string {
	es, _ := err.(diag.Errors)
	var codes []string
	for _, e := range es {
		if ce, ok := e.(*Error); ok {
			codes = append(codes, ce.ErrCode)
		}
	}
	return strings.Join(codes, ",")
}

// An unannotated local seeded by an untyped integer takes ONE integer type,
// decided by its first width-fixing use and i32 when no use fixes one (#10123).
// Before, native kept the local polymorphic and let every use settle it
// separately, so one slot could be read at two widths and the backends each
// picked their own.
func TestLiteralLocalTakesOneType(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string // "" = accepted
	}{
		{"widens to i64", "let x = 5; let y: i64 = x;", ""},
		{"narrows to u8", "let x = 5; let y: u8 = x;", ""},
		{"shift count", "let x = 5; let z: u64 = 1 as u64 << x;", ""},
		{"operand of a wider op", "let x = 5; let y: i64 = x + (1 as i64);", ""},
		{"assigned a typed value", "let x = 0; let n: i64 = 7i64; x = n; let y: i64 = x;", ""},
		{"through another literal local", "let x = 5; let y = x + 1; let z: u64 = y;", ""},
		{"range loop variable", "for i in 0..64 { let b: i64 = (1 as u64 << i) as i64; }", ""},
		{"cast does not decide", "let x = 5; let f: f64 = x as f64; let y: i64 = x;", ""},
		{"no use fixes a width", "let x = 5; let y = x * 2; return y;", ""},
		{"compared with a settled local", "let hi = 255; let i = 250; let b: u8 = i; if (i != hi) {} let c: u8 = hi;", ""},
		{"equal with a settled local on the left", "let hi = 255; let i = 250; let b: u8 = i; if (hi == i) {} let c: u8 = hi;", ""},
		{"ordered with a settled local", "let hi = 255; let i = 250; let b: u8 = i; if (hi < i) {} let c: u8 = hi;", ""},
		{"compared before either settles", "let hi = 255; let i = 250; if (i != hi) {} let b: u8 = i; let c: u8 = hi;", ""},
		{"arithmetic with a settled local", "let hi = 255; let i = 250; let b: u8 = i; let d = hi - i; let e: u8 = d; let c: u8 = hi;", ""},
		{"assigned a settled local", "let hi = 255; let i = 250; let b: u8 = i; hi = i; let c: u8 = hi;", ""},
		{"compared local takes the other's width", "let hi = 255; let i = 250; let b: u8 = i; if (i != hi) {} let c: i32 = hi;", "E003"},
		{"compared local out of range", "let hi = 300; let i = 250; let b: u8 = i; if (i != hi) {}", "E047"},

		{"two widths", "let x = 5; let a: i32 = x; let b: i64 = x;", "E003"},
		{"two widths through a second local", "let x = 5; let y = x; let a: i64 = x; let b: i32 = y;", "E003"},
		{"float is not an integer width", "let x = 5; let f: f64 = x;", "E003"},
		{"value fixed by the literal", "let x = 300; let b: u8 = x;", "E047"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, "function main(): i32 {\n"+c.body+"\nreturn 0;\n}\n")
			if c.code == "" {
				if err != nil {
					t.Fatalf("rejected, want accepted: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted, want %s", c.code)
			}
			if got := errCodes(err); got != c.code {
				t.Fatalf("got %s (%v), want %s", got, err, c.code)
			}
		})
	}
}

// The program from #10123's second comment: one local read as i32 and as i64.
// The interpreter answered 2, x86-64 answered 1, and wasm refused the module.
func TestLiteralLocalTwoWidthsIsRejected(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    let x = 2147483647;
    x = x + 1;
    let a: i32 = x;
    let b: i64 = x;
    if (b > 0i64) { return 1; }
    if (a < 0) { return 2; }
    return 3;
}
`)
	if got := errCodes(err); got != "E003" {
		t.Fatalf("got %s (%v), want E003", got, err)
	}
}

// The checker hands the lowering a settled local: the declaration, its
// initialiser, and every expression over it carry the one width, so no backend
// is left to pick a slot width of its own.
func TestLiteralLocalSettlesEveryRead(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    let t: i64 = 0 as i64;
    for i in 0..64 {
        let bits: i64 = (1 as u64 << i) as i64;
        t = t + bits;
    }
    let k = 1;
    let m = k + 2;
    return m;
}
`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ast.NumberType{
		"i":            {Width: 64, Signed: false},
		"__range_hi_1": {Width: 64, Signed: false},
		"k":            {Width: 32, Signed: true},
		"m":            {Width: 32, Signed: true},
	}
	for v, ty := range info.VarTypes {
		w, ok := want[v.Name]
		if !ok {
			continue
		}
		if !ast.Equal(ty, w) || !ast.Equal(v.Type, w) {
			t.Errorf("%s: VarTypes %s, decl %s, want %s", v.Name, ty, v.Type, w)
		}
		delete(want, v.Name)
	}
	for name := range want {
		t.Errorf("no local %s", name)
	}
	ast.WalkProgram(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.NumberLit:
			if x.Width == 0 && !x.IsFloat {
				t.Errorf("%v: literal %d left unsettled", x.P, x.Value)
			}
		case *ast.Binary:
			if x.IntWidth == 0 && !x.IsFloat {
				t.Errorf("%v: %q left without a width", x.P, x.Op)
			}
		}
		return true
	})
}
