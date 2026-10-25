package constfold

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestFoldChainThreeLevels(t *testing.T) {
	prog := fold(t, `const A: i32 = 2;
const B: i32 = A + 1;
const C: i32 = B * 2;
function main(): i32 { return C; }`)
	if len(prog.Consts) != 0 {
		t.Errorf("expected const decls stripped, got %v", prog.Consts)
	}
	lit, ok := returnLit(t, prog).(*ast.NumberLit)
	if !ok {
		t.Fatalf("return should be NumberLit, got %T", returnLit(t, prog))
	}
	if lit.Value != 6 {
		t.Errorf("got %d, want 6", lit.Value)
	}
}

func TestFoldChainFloatDivision(t *testing.T) {
	prog := fold(t, `const A: f32 = 2.0;
const B: f32 = A / 4.0;
function main(): f32 { return B; }`)
	lit, ok := returnLit(t, prog).(*ast.FloatLit)
	if !ok {
		t.Fatalf("return should be FloatLit, got %T", returnLit(t, prog))
	}
	if lit.Value != 0.5 {
		t.Errorf("got %v, want 0.5", lit.Value)
	}
}

func TestFoldComparisonOperators(t *testing.T) {
	cases := []struct {
		op   string
		l, r int64
		want bool
	}{
		{"==", 5, 5, true},
		{"==", 5, 6, false},
		{"!=", 5, 6, true},
		{"<", 3, 5, true},
		{"<", 5, 3, false},
		{"<=", 5, 5, true},
		{">", 6, 2, true},
		{">=", 5, 5, true},
		{">=", 4, 5, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.op, func(t *testing.T) {
			src := "const A: i32 = " + itoa(c.l) + ";\n" +
				"const R: boolean = A " + c.op + " " + itoa(c.r) + ";\n" +
				"function main(): boolean { return R; }"
			prog := fold(t, src)
			lit, ok := returnLit(t, prog).(*ast.BoolLit)
			if !ok {
				t.Fatalf("%d %s %d: return should be BoolLit, got %T", c.l, c.op, c.r, returnLit(t, prog))
			}
			if lit.Value != c.want {
				t.Errorf("%d %s %d: got %v, want %v", c.l, c.op, c.r, lit.Value, c.want)
			}
		})
	}
}

func TestFoldComparisonFloat(t *testing.T) {
	prog := fold(t, `const X: f32 = 1.5;
const Y: boolean = X == 1.5;
function main(): boolean { return Y; }`)
	lit, ok := returnLit(t, prog).(*ast.BoolLit)
	if !ok {
		t.Fatalf("return should be BoolLit, got %T", returnLit(t, prog))
	}
	if !lit.Value {
		t.Errorf("got %v, want true", lit.Value)
	}
}

func TestFoldComparisonString(t *testing.T) {
	prog := fold(t, `const A: string = "a";
const EQ: boolean = A == "a";
const NE: boolean = A != "b";
function main(): boolean { return EQ; }`)
	lit, ok := returnLit(t, prog).(*ast.BoolLit)
	if !ok {
		t.Fatalf("return should be BoolLit, got %T", returnLit(t, prog))
	}
	if !lit.Value {
		t.Errorf("got %v, want true", lit.Value)
	}
}

func TestFoldBitwiseAndShift(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want int64
	}{
		{"or", "0xF0 | 0x0F", 0xFF},
		{"and", "0xF6 & 0x03", 0x02},
		{"xor", "0x0F ^ 0x03", 0x0C},
		{"shl", "1 << 4", 16},
		{"shr", "256 >> 2", 64},
		{"shl_count_64", "1 << 64", 1},
		{"shl_count_65", "1 << 65", 2},
		{"shr_count_65", "256 >> 65", 128},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			src := "const R: i32 = " + c.expr + ";\n" +
				"function main(): i32 { return R; }"
			prog := fold(t, src)
			lit, ok := returnLit(t, prog).(*ast.NumberLit)
			if !ok {
				t.Fatalf("return should be NumberLit, got %T", returnLit(t, prog))
			}
			if lit.Value != c.want {
				t.Errorf("%s: got %d, want %d", c.expr, lit.Value, c.want)
			}
		})
	}
}

func TestFoldUnaryMinusOverConst(t *testing.T) {
	prog := fold(t, `const A: i32 = 10;
const N: i32 = -A;
function main(): i32 { return N; }`)
	lit, ok := returnLit(t, prog).(*ast.NumberLit)
	if !ok {
		t.Fatalf("return should be NumberLit, got %T", returnLit(t, prog))
	}
	if lit.Value != -10 {
		t.Errorf("got %d, want -10", lit.Value)
	}
}

func TestFoldSubstitutesStructAndArray(t *testing.T) {
	prog := fold(t, `struct Pt { x: i32, y: i32 }
const N: i32 = 4;
function main(): i32 {
	let p: Pt = Pt{ x: N, y: N + 1 };
	let a: i32[] = [N, N * 2];
	return a[0];
}`)
	if c := countIdents(prog, "N"); c != 0 {
		t.Errorf("expected no remaining `N` Idents in struct/array positions, found %d", c)
	}
}

func TestIllTypedConstIsTheCheckersError(t *testing.T) {
	for src, want := range map[string]string{
		`const X: i32 = 1 - true;`:     `type error at 1:18: operator "-" requires an integer type, got boolean`,
		`const X: string = 1;`:         `type error at 1:1: const X: expected string, got i32`,
		`const X: f32 = 1 + 1.5;`:      `type error at 1:18: operator "+" requires float, got i32`,
		`const X: i32 = true + 1;`:     `type error at 1:21: operator "+" requires an integer type, got boolean`,
		`const S: string = "a" - "b";`: `type error at 1:23: operator "-" requires an integer type, got string`,
		"const A: i32 = 1 + true;\nconst B: i32 = A + 1;\nfunction main(): i32 { return A + B; }": `type error at 1:18: operator "+" requires an integer type, got boolean`,
	} {
		if got := foldErr(t, src); got != want {
			t.Errorf("%s\n  error %q, want %q", src, got, want)
		}
	}
}

func TestWellTypedUnfoldableConstIsE093(t *testing.T) {
	got := foldErr(t, `const X: i32 = 1 / 0 + (1 + true);`)
	if !strings.Contains(got, "division by zero") {
		t.Errorf("a zero divisor outranks a sibling's type error, as the self-host's consteval does; got %v", got)
	}
	got = foldErr(t, `const X: f32 = 5;`)
	if !strings.Contains(got, "1:1: const X: declared type f32") {
		t.Errorf("a well-typed initializer the folder cannot settle stays the folder's E093; got %v", got)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
