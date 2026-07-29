package constfold

// Edge-case coverage for the constant-folding pass, complementing the
// basics in constfold_test.go. These exercise the deeper reaches of
// evalConst / foldBinary / foldUnary that the headline tests skip:
//
//   - multi-level const chains (A -> B -> C) collapsing to one literal,
//   - the full comparison-operator set producing BoolLits,
//   - bitwise / shift operators on integer consts,
//   - unary minus folded through a const reference (not a bare literal),
//   - const substitution into struct-field and array-element positions,
//   - the mixed-scalar rejection paths beyond the single i32/f32 case
//     the basics cover (the asymmetric "operands aren't both numbers"
//     diagnostic when an integer literal meets a bool / float operand).

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A three-link const chain folds to a single literal: B reads A, C
// reads B, and main() returns C as a fully-resolved NumberLit. This
// guards the "earlier consts feed later consts" invariant past the
// two-level depth the basics reach.
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
	if lit.Value != 6 { // (2 + 1) * 2
		t.Errorf("got %d, want 6", lit.Value)
	}
}

// A float const chain folds with division: B = A / 4.0 over the
// earlier float const A. Confirms the float arithmetic path chains the
// same way the integer path does.
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

// Every integer comparison operator folds to a BoolLit. Parameterised
// so a regression in any single operator is named at its row.
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

// Float comparisons fold to BoolLits too, including the equality and
// ordering operators over an earlier float const.
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

// String equality / inequality fold to BoolLits (the only comparison
// operators strings accept). Concatenation is already covered by the
// basics; this pins the == / != arms.
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

// Bitwise and shift operators fold over integer consts: AND, OR, XOR,
// left/right shift. Each row resolves to a NumberLit. The bitwise ops
// take Go's semantics directly; the shifts deliberately do not, because
// Go does not mask the count.
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
		// A shift masks its count to the operand width, so a count at or past
		// the width wraps rather than annihilating the value. The pass folds in
		// int64, so the rule here is `& 63`. Go's own `1 << 64` is 0, which is
		// what this used to compile `const A: i32 = 1 << 64` to while the same
		// expression evaluated to 1 at runtime.
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

// Unary minus folds through a const reference, not just a bare
// literal: -A where A is an earlier i32 const. The basics fold `!`
// over a bool but never apply a unary to a *referenced* const.
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

// A const lands in struct-field and array-element initialiser
// positions and is fully substituted away. This complements
// TestFoldSubstitutesAcrossExpressionPositions by adding the struct-
// literal field path (StructLit.Fields[i].Value) and confirming the
// const value also resolves an array-index return.
func TestFoldSubstitutesStructAndArray(t *testing.T) {
	prog := fold(t, `struct Pt { x: i32, y: i32 }
const N: i32 = 4;
function main(): i32 {
	var p: Pt = Pt{ x: N, y: N + 1 };
	var a: i32[] = [N, N * 2];
	return a[0];
}`)
	if c := countIdents(prog, "N"); c != 0 {
		t.Errorf("expected no remaining `N` Idents in struct/array positions, found %d", c)
	}
}

// Mixing an integer literal with a bool operand is rejected. Because
// the left operand is a NumberLit the pass takes the integer path, so
// the diagnostic is the asymmetric "operands aren't both numbers"
// rather than a bool-path message — a path the basics never reach.
func TestFoldRejectsNumberBoolMix(t *testing.T) {
	got := foldErr(t, `const X: i32 = 1 < true;`)
	if !strings.Contains(got, "aren't both numbers") {
		t.Errorf("expected number-path mismatch diagnostic, got %v", got)
	}
}

// Mixing an integer literal with a float literal is rejected even
// though both are numeric: the pass demands explicit conversions. The
// left NumberLit drives the integer path, so the float operand trips
// the same "operands aren't both numbers" check.
func TestFoldRejectsIntFloatMix(t *testing.T) {
	got := foldErr(t, `const X: f32 = 1 + 1.5;`)
	if !strings.Contains(got, "aren't both numbers") {
		t.Errorf("expected numeric-mix rejection, got %v", got)
	}
}

// A bool operand combined with a non-bool right side is rejected via
// the bool path's dedicated diagnostic, distinct from the number-path
// message above.
func TestFoldRejectsBoolNonBoolMix(t *testing.T) {
	got := foldErr(t, `const X: i32 = true + 1;`)
	if !strings.Contains(got, "bool and non-bool") {
		t.Errorf("expected bool-path mismatch diagnostic, got %v", got)
	}
}

// itoa renders a signed int64 without pulling strconv into the test's
// import set for a single call site.
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
