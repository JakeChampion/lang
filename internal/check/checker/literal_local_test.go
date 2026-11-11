package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

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

func TestLiteralLocalTakesOneType(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
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
		{"interpolated before the wider use", "let x = 2000000000; let y = x + x; let s = f\"{y}\"; let z: i64 = x; let w: i64 = y;", ""},
		{"interpolated after the wider use", "let x = 2000000000; let y = x + x; let z: i64 = x; let s = f\"{y}\"; let w: i64 = y;", ""},
		{"method call before the wider use", "let x = 5; let s = x.to_string(); let z: i64 = x;", ""},
		{"compared with a settled local", "let hi = 255; let i = 250; let b: u8 = i; if (i != hi) {} let c: u8 = hi;", ""},
		{"equal with a settled local on the left", "let hi = 255; let i = 250; let b: u8 = i; if (hi == i) {} let c: u8 = hi;", ""},
		{"ordered with a settled local", "let hi = 255; let i = 250; let b: u8 = i; if (hi < i) {} let c: u8 = hi;", ""},
		{"compared before either settles", "let hi = 255; let i = 250; if (i != hi) {} let b: u8 = i; let c: u8 = hi;", ""},
		{"arithmetic with a settled local", "let hi = 255; let i = 250; let b: u8 = i; let d = hi - i; let e: u8 = d; let c: u8 = hi;", ""},
		{"assigned a settled local", "let hi = 255; let i = 250; let b: u8 = i; hi = i; let c: u8 = hi;", ""},
		{"compared local takes the other's width", "let hi = 255; let i = 250; let b: u8 = i; if (i != hi) {} let c: i32 = hi;", ""},
		{"compared local out of range", "let hi = 300; let i = 250; let b: u8 = i; if (i != hi) {}", "E047"},

		{"two widths", "let x = 5; let a: i32 = x; let b: i64 = x;", ""},
		{"two widths through a second local", "let x = 5; let y = x; let a: i64 = x; let b: i32 = y;", "E003"},
		{"float is not an integer width", "let x = 5; let f: f64 = x;", "E003"},
		{"element of a literal array", "let a = 1; let xs = [a, 2]; let ys: i64[] = xs; let u: i64 = a;", ""},
		{"two widths through a literal array", "let a = 1; let xs = [a, 2]; let ys: i64[] = xs; let u: u8 = a;", "E003"},
		{"two widths through an append result", "let a = 1; let xs = [a, 2]; let ys = xs.append(3); let zs: i64[] = ys; let u: u8 = a;", "E003"},
		{"two widths through a with result", "let a = 1; let xs = [a, 2]; let ys = xs.with(1, 3); let zs: i64[] = ys; let u: u8 = a;", "E003"},
		{"two widths through an assigned append", "let a = 1; let xs = [a, 2]; let ys = [3]; ys = xs.append(4); let zs: i64[] = ys; let u: u8 = a;", "E003"},
		{"two widths through an operand in a literal array", "let a = 1; let xs = [a + 1, 2]; let ys: i64[] = xs; let u: u8 = a;", "E003"},
		{"two widths through an append argument", "let b = 1; let zs = [1, 2]; let ws = zs.append(b); let ys: i64[] = ws; let v: u8 = b;", "E003"},
		{"two widths through a with argument", "let b = 1; let zs = [1, 2]; let ws = zs.with(0, b); let ys: i64[] = ws; let v: u8 = b;", "E003"},
		{"two widths through an assigned append argument", "let b = 1; let zs = [1, 2]; zs = zs.append(b); let ys: i64[] = zs; let v: u8 = b;", "E003"},
		{"two widths through an operand in an append argument", "let b = 1; let zs = [1, 2]; let ws = zs.append(b + 1); let ys: i64[] = ws; let v: u8 = b;", "E003"},
		{"append argument takes the width of the array", "let b = 1; let zs = [1, 2]; let ws = zs.append(b); let ys: i64[] = ws; let v: i64 = b;", ""},
		{"two widths through an append on an array literal", "let b = 1; let zs = [1, 2].append(b); let ys: i64[] = zs; let v: u8 = b;", "E003"},
		{"two widths through a with on an array literal", "let b = 1; let zs = [1, 2].with(0, b); let ys: i64[] = zs; let v: u8 = b;", "E003"},
		{"two widths through an element of an appended array literal", "let b = 1; let zs = [b, 2].append(3).append(4); let ys: i64[] = zs; let v: u8 = b;", "E003"},
		{"append on an array literal takes the width of its use", "let zs = [1, 2].append(3); let ys: i64[] = zs;", ""},
		{"two widths through an append in a typed destination", "let b = 1; let zs: i64[] = [1, 2].append(b); let v: u8 = b;", "E003"},
		{"two widths through an element of an append in a typed destination", "let b = 1; let zs: i64[] = [b, 2].with(1, 3); let v: u8 = b;", "E003"},
		{"append in a typed destination gives its argument the width", "let b = 1; let zs: i64[] = [1, 2].append(b); let v: i64 = b;", ""},
		{"append on an array literal takes the width of a typed argument", "let x: i64 = 7i64; let zs = [1, 2].append(x); let ys: i64[] = zs;", ""},
		{"append result gives the array its width", "let xs = [1, 2]; let ys = xs.append(3); let zs: i64[] = ys; let ws: i64[] = xs;", ""},
		{"a typed append result gives its literal array local the width", "let zs = [1, 2]; let ws: i64[] = zs.append(4); let n: i32 = zs[0];", "E003"},
		{"a typed chain of appends gives its literal array local the width", "let zs = [1, 2]; let ws: i64[] = zs.append(4).with(0, 5); let n: i32 = zs[0];", "E003"},
		{"a wide literal in an append gives the array literal i64", "let zs = [1, 2].append(5000000000); let n: i32 = zs[0];", "E003"},
		{"a wide literal in a with gives the array literal i64", "let zs = [1, 2].with(0, 5000000000); let n: i64 = zs[1];", ""},
		{"a wide literal in an append gives the literal array local i64", "let zs = [1, 2]; let ws = zs.append(5000000000); let n: i32 = zs[0];", "E003"},
		{"an integer local in an append on a float array literal", "let b = 1; let zs = [1.5, 2.5].append(b);", "E038"},
		{"an append on an empty array literal with no type", "let ys = [].append(2);", "E020"},
		{"an append on an empty array literal in a typed destination", "let ys: i64[] = [].append(2).append(3);", ""},
		{"an integer local in a float array literal", "let b = 1; let zs = [1.5, 2.5, b];", "E034"},
		{"an integer local before a float in an array literal", "let b = 1; let zs = [b, 1.5];", "E034"},
		{"an integer local in an array literal in a float destination", "let b = 1; let zs: f64[] = [1, b];", "E034"},
		{"an integer local in a nested float array literal", "let b = 1; let zs = [[1.5], [b]];", "E034"},
		{"an integer literal in an append on a float array literal", "let zs = [1.5, 2.5].append(1); let ws = [1.5, 2.5].with(0, 1 + 2);", ""},
		{"an integer literal in an append on a float array local", "let ys = [1.5, 2.5]; let xs = ys.append(1); let q: f32[] = ys;", ""},
		{"a float destination of an append on an array literal", "let zs: f32[] = [1.5, 2.5].append(3); let ws: f64[] = [1, 2].append(1.5);", ""},
		{"a float argument of an append on a float array literal", "let x: f32 = 2.5f32; let zs = [1.5, 2].append(x); let ws: f32[] = zs;", ""},
		{"a float literal in an append on an integer array literal", "let zs = [1, 2].append(1.5);", "E038"},
		{"a method on an empty array literal", "let n = [].len();", "E020"},
		{"a method after an append on an empty array literal", "let n = [].append(2).len();", "E020"},
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

// A typed parameter gives an append on an array literal its element type, and
// the literal local in the argument has that type.
func TestLiteralLocalInAnAppendArgumentOfACall(t *testing.T) {
	const head = "function s64(xs: i64[]): i64 { return xs[0]; }\nfunction main(): i32 {\n"
	err := checkSource(t, head+"let b = 1; s64([1, 2].append(b)); let v: u8 = b;\nreturn 0;\n}\n")
	if err == nil || !strings.Contains(err.Error(), `variable "v": expected u8, got i64`) {
		t.Fatalf("got %v, want E003 for v", err)
	}
	if err := checkSource(t, head+"let x: i64 = 7i64; s64([1, 2].append(x));\nreturn 0;\n}\n"); err != nil {
		t.Fatal(err)
	}
}

// A typed parameter gives an append on a literal array local its element
// type, and the local takes that type.
func TestLiteralArrayLocalInAnAppendArgumentOfACall(t *testing.T) {
	const head = "function s64(xs: i64[]): i64 { return xs[0]; }\nfunction main(): i32 {\n"
	if err := checkSource(t, head+"let zs = [1, 2]; let m: i64 = s64(zs.append(4));\nreturn 0;\n}\n"); err != nil {
		t.Fatal(err)
	}
	err := checkSource(t, head+"let zs = [1, 2]; s64(zs.append(4).append(5)); let n: i32 = zs[0];\nreturn 0;\n}\n")
	if err == nil || !strings.Contains(err.Error(), `variable "n": expected i32, got i64`) {
		t.Fatalf("got %v, want E003 for n", err)
	}
}

// An append on an empty array literal with no type is one E020, with no
// diagnostic for the call.
func TestAppendOnAnEmptyArrayLiteralIsOneDiagnostic(t *testing.T) {
	err := checkSource(t, "function main(): i32 {\nlet ys = [].append(2);\nreturn 0;\n}\n")
	if err == nil || strings.Contains(err.Error(), "E043") || strings.Contains(err.Error(), "E038") {
		t.Fatalf("got %v, want E020 only", err)
	}
}

// The local retains its i32 storage and wrapping arithmetic. The later i64
// use converts the stored result instead of changing its storage width.
func TestLiteralLocalUseWidensWithoutChangingStorage(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
}

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
