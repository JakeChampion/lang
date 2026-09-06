package e2e

import "testing"

// An int→float cast pushed its target INTO the operand expression: the
// checker's CastExpr case ran `settleNumeric(inner, target)` for every
// non-float inner, and settleFloat stamps a FloatWidth on a `+ - * /` binary
// and recurses into both sides. `(7 / 2) as f64` therefore became float
// division and produced a different wrong answer on each engine (#8456):
//
//	interp   3.5              — the operands were retyped, so `/` was f64 `/`
//	x86-64   0                — an f64 landing in a slot read as an integer
//	arm64    0
//	wasm     module rejected  — the validator caught the same confusion
//
// `(7 / 2) as f32` gave 1080033300 on both natives, the f32 bit pattern read
// as an integer, and `(3 * 4) as f64` gave 0 — division was not special; any
// binary settleFloat reaches was.
//
// The variable form was correct on all four engines all along, because its
// operands had already committed to i32 and settleFloat leaves a settled
// operand alone. Each case below computes both forms in one program and
// exits non-zero if they disagree or if the value is wrong.
func TestIntExprCastToFloatConvertsTheResult(t *testing.T) {
	cases := []struct {
		name string
		ft   string
		// expr is instantiated twice: with the literals, and with `p` / `q`.
		expr string
		p, q string
		want string
	}{
		{"divide_f64", "f64", "@p / @q", "7", "2", "3.0"},
		{"divide_f32", "f32", "@p / @q", "7", "2", "3.0"},
		{"modulo_f64", "f64", "@p % @q", "7", "2", "1.0"},
		{"modulo_f32", "f32", "@p % @q", "7", "2", "1.0"},
		{"multiply_f64", "f64", "@p * @q", "3", "4", "12.0"},
		{"add_f64", "f64", "@p + @q", "3", "4", "7.0"},
		{"subtract_f64", "f64", "@p - @q", "3", "4", "0.0 - 1.0"},
		{"subtract_f32", "f32", "@p - @q", "3", "4", "0.0 - 1.0"},
		{"negative_divide_f64", "f64", "(@p - @q) / 2", "0", "7", "0.0 - 3.0"},
		{"nested_divide_f64", "f64", "(@p / @q) / @q", "9", "2", "2.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lit := expand(c.expr, c.p, c.q)
			vars := expand(c.expr, "p", "q")
			src := "function main(): i32 {\n" +
				"    var fromLiterals: " + c.ft + " = (" + lit + ") as " + c.ft + ";\n" +
				"    var p: i32 = " + c.p + ";\n" +
				"    var q: i32 = " + c.q + ";\n" +
				"    var fromVars: " + c.ft + " = (" + vars + ") as " + c.ft + ";\n" +
				"    if (fromLiterals != fromVars) { return 7; }\n" +
				"    if (fromLiterals != " + c.want + ") { return 8; }\n" +
				"    return 0;\n}\n"
			assertExitsZeroEverywhere(t, src)
		})
	}
}

// The i32 a cast stands in for a still-polymorphic operand was spelled
// `NumberType{Width: 32}`, which is u32: `(3 - 4) as f64` wrapped to
// 4294967295 on every engine. The narrowing rule carried the same spelling, so
// `((0 - 7) / 2) as u8` divided as u32 — 252 from literals, 253 from a
// variable. Both forms in one program, exit non-zero on a disagreement.
func TestNarrowingCastComputesAtI32(t *testing.T) {
	src := `function main(): i32 {
    var fromLiterals: u8 = ((0 - 7) / 2) as u8;
    var z: i32 = 0;
    var fromVars: u8 = ((z - 7) / 2) as u8;
    if (fromLiterals != fromVars) { return 7; }
    if (fromLiterals != 253) { return 8; }
    return 0;
}
`
	assertExitsZeroEverywhere(t, src)
}

// A const operand reaches the cast through constfold's substitution — as a
// width-stamped literal when the const is declared, polymorphic when it is
// not — and both have to compute the subtraction signed.
func TestConstOperandCastToFloatIsSigned(t *testing.T) {
	src := `const P: i32 = 3;
const Q = 4;
function main(): i32 {
    var declared: f64 = (P - Q) as f64;
    if (declared != 0.0 - 1.0) { return 7; }
    var undeclared: f64 = (Q - P - 5) as f64;
    if (undeclared != 0.0 - 4.0) { return 8; }
    return 0;
}
`
	assertExitsZeroEverywhere(t, src)
}

// The cast still settles a BARE literal at its target, which is what lets a
// value wider than the i32 default reach an f64 at all. Pinned alongside the
// fix, because the fix is precisely a carve-out from that rule.
func TestBareLiteralCastToFloatStillSettlesAtTarget(t *testing.T) {
	src := `function main(): i32 {
    var a: f64 = 7 as f64;
    if (a != 7.0) { return 7; }
    var b: f64 = -7 as f64;
    if (b != 0.0 - 7.0) { return 8; }
    var c: f64 = 4611686018427387904 as f64;
    if (c != 4611686018427387904.0) { return 9; }
    return 0;
}
`
	assertExitsZeroEverywhere(t, src)
}

// expand instantiates a two-operand template.
func expand(tmpl, p, q string) string {
	out := ""
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] == '@' && i+1 < len(tmpl) {
			switch tmpl[i+1] {
			case 'p':
				out, i = out+p, i+1
				continue
			case 'q':
				out, i = out+q, i+1
				continue
			}
		}
		out += string(tmpl[i])
	}
	return out
}
