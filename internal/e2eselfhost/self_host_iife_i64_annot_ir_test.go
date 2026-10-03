package e2eselfhost

import "testing"

// iifeI64AnnotIRCases pin a small-literal-branch i64/u64 if/match-EXPRESSION bound
// to an i64/u64-annotated local to the self-host IR path on x86-64 + wasm. An
// if/match-expression desugars to a 0-arg IIFE; the IR lowering already inlined it
// into an i64 temp when SOME branch carried an i64 value (e.g. `{ 5000000000 }`),
// but a fully-small-literal i64 expression — where i64-ness comes ONLY from the
// binding annotation (`let x: i64 = if (c) { 5 } else { 9 }`) — failed the
// branch-value width classifier and bailed the whole module to the legacy AST
// emitter. #2691 threaded a force_i64 flag from lower_i64 (the binding context is
// definitionally i64/u64) through lower_iife / lower_iife_match so the inline temp
// is marked i64 and each small-literal branch is widened into it. Each case is
// oracle-checked against the interpreter and returns <= 126. Mirrors
// self_host_structarray_call_field_ir_test.go.
//
// (Constant-condition forms like `if (1 < 2) { 5 } else { 9 }` take a separate
// const-fold path and are not widened here.)
//
// The later cases pin the other half of the same decision — the per-branch tag
// if_expr_rt computes, which wider_rt then combines — through the MATCH arm
// parser. #6388 fixed the computed-branch gap and covered it via parse_if_chain;
// parse_match_expr reaches if_expr_rt through a separate arm-combining loop
// (first-arm-wins seeded, then wider_rt per arm), so it is worth its own cases.
var iifeI64AnnotIRCases = []struct {
	name string
	main string
}{
	// if-expression, runtime condition, both branches small i64 literals. 5.
	{"ifexpr-i64-runtime", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	// u64 annotation takes the same path. 5.
	{"u64-ifexpr-runtime", `function main(): i32 { let n = 5; let x: u64 = if (n > 3) { 5 } else { 9 }; return x as i32; }`},
	// match-expression, small i64 literal arms. 5.
	{"matchexpr-i64", `function main(): i32 { let n = 1; let x: i64 = match (n) { 1 => 5, _ => 9 }; return x as i32; }`},
	// match-expression, three arms (non-default branch taken). 5.
	{"matchexpr-i64-wild", `function main(): i32 { let n = 2; let x: i64 = match (n) { 1 => 7, 2 => 5, _ => 9 }; return x as i32; }`},
	// if-expression result used in subsequent i64 arithmetic. 5 + 1 = 6.
	{"ifexpr-i64-arith", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5 } else { 9 }; let y: i64 = x + 1; return y as i32; }`},
	// nested else-if, small i64 literals (middle branch taken). 7.
	{"nested-elseif-i64", `function main(): i32 { let n = 1; let x: i64 = if (n > 5) { 1 } else if (n > 0) { 7 } else { 9 }; return x as i32; }`},
	// Regression: a branch with a real i64 (big-literal) value was already on the IR
	// path via the branch-value classifier — it must stay there. 5000000000 % 7 = 2.
	{"ifexpr-i64-bigbranch", `function main(): i32 { let n = 5; let x: i64 = if (n > 3) { 5000000000 } else { 1 }; return (x % 7) as i32; }`},

	// A COMPUTED match arm. if_expr_rt classified literals, bools, strings and
	// nested IIFEs and sent everything else to "i32", so an arm that computed its
	// value instead of naming it lost the width: wider_rt had nothing to widen
	// with, the IIFE was labelled i32, and the i64 `return` bailed the module.
	// Magnitude was never the trigger — the bare literal `2147483967i64` lowered
	// fine while `2147483967i64 * 2i64` did not. 604 /? 478 = Some(1), so 1.
	{"matchexpr-i64-binary-arm", `function main(): i32 { let x: i64 = match ((604i64) /? (478i64)) { Some(w) => w, None => (3i64 * 5i64) }; return x as i32; }`},
	// Same gap with the computed arm TAKEN, so the exit code is a direct oracle on
	// the width: 5000000000 / 1000000000 = 5 at 64-bit, but a 32-bit truncation
	// (705032704) / 1000000000 = 0. 1 /? 0 is None, so the None arm is the value.
	{"matchexpr-i64-binary-arm-taken", `function main(): i32 { let x: i64 = match ((1i64) /? (0i64)) { Some(w) => w, None => (5000000000i64 / 1000000000i64) }; return x as i32; }`},
	// A SHIFT arm. wider_rt over BOTH operands is what carries the width here, and
	// it is essential that the shift is not special-cased to its left operand:
	// "i32" doubles as if_expr_rt's unknown, so a shift whose value is a CALL
	// (`id(489i64) << 2147484177i64`) reads i32 on the left and recovers the width
	// only from the right. 5000000000 >> 30 = 4; truncated to 32 bits, 0.
	{"matchexpr-i64-shift-arm", `function main(): i32 { let x: i64 = match ((1i64) /? (0i64)) { Some(w) => w, None => (5000000000i64 >> 30i64) }; return x as i32; }`},
	// A CAST arm, which must stay on the "i32" default rather than being taught to
	// report its operand's width. There is no ExprUnary case in if_expr_rt, and
	// adding a naive one that returns if_expr_rt(operand) inverts the one construct
	// whose job is to CHANGE the width: `((5000000000i64 >> 30i64) as i32) & 7i32`
	// then reads i64 and bails a module that lowered before. The un-annotated
	// `return` position is what exposes it — with a `let x: i32 =` annotation the
	// binding supplies the type and the inversion hides. 5000000000 >> 30 = 4, & 7 = 4.
	{"matchexpr-cast-arm-narrows", `function main(): i32 { return (match ((1i64) /? (0i64)) { Some(w) => (w as i32), None => (((5000000000i64 >> 30i64) as i32) & 7i32) } & 255i32); }`},
	// A `!` branch, which reaches if_expr_rt as a unary and so takes the same "i32"
	// default. The comparison sibling — which irt_is_cmp tags "bool" — is covered in
	// self_host_ifexpr_binary_width_ir_test.go (#6388). 5.
	{"ifexpr-bool-not-arm", `function main(): i32 { let n = 5; let b: boolean = if (n > 3) { !(n > 100) } else { false }; let x: i32 = 0; if (b) { x = 5; } return x; }`},
}

// TestSelfHostIifeI64AnnotIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostIifeI64AnnotIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range iifeI64AnnotIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
