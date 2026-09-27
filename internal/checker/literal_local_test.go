package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
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
		{"widens to i64", "var x = 5; var y: i64 = x;", ""},
		{"narrows to u8", "var x = 5; var y: u8 = x;", ""},
		{"shift count", "var x = 5; var z: u64 = 1 as u64 << x;", ""},
		{"operand of a wider op", "var x = 5; var y: i64 = x + (1 as i64);", ""},
		{"assigned a typed value", "var x = 0; var n: i64 = 7i64; x = n; var y: i64 = x;", ""},
		{"through another literal local", "var x = 5; var y = x + 1; var z: u64 = y;", ""},
		{"range loop variable", "for i in 0..64 { var b: i64 = (1 as u64 << i) as i64; }", ""},
		{"cast does not decide", "var x = 5; var f: f64 = x as f64; var y: i64 = x;", ""},
		{"no use fixes a width", "var x = 5; var y = x * 2; return y;", ""},

		{"two widths", "var x = 5; var a: i32 = x; var b: i64 = x;", "E003"},
		{"two widths through a second local", "var x = 5; var y = x; var a: i64 = x; var b: i32 = y;", "E003"},
		{"float is not an integer width", "var x = 5; var f: f64 = x;", "E003"},
		{"value fixed by the literal", "var x = 300; var b: u8 = x;", "E047"},
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
    var x = 2147483647;
    x = x + 1;
    var a: i32 = x;
    var b: i64 = x;
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
    var t: i64 = 0 as i64;
    for i in 0..64 {
        var bits: i64 = (1 as u64 << i) as i64;
        t = t + bits;
    }
    var k = 1;
    var m = k + 2;
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
