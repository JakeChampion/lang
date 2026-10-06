package e2ecompiler

import "testing"

// TestSelfHostFloatAliasIsF64 pins `float` as the f64 alias, not f32.
//
// A `float`-annotated local (and param) was once tagged f32 alongside a real
// `f32` annotation. f32 arithmetic rounds through an f32 demote→promote pair,
// so the tag made the VALUE compute at f32 precision, and it propagated: an
// f64-declared local initialised from a `float` one inherited it (#5882).
//
// The check compares raw IEEE-754 bit patterns via f64_bits, so it fails on
// the arithmetic itself rather than on any rendering of it:
//
//	1.0f64 / 3.0 = 0x3FD5555555555555 = 4599676419421066581
//	1.0f32 / 3.0 = 0x3FD5555560000000 = 4599676419600023552  (mantissa truncated)
//
// `pick` answers 1 for the first and 2 for the second. The f32 rows are the
// other half of the contract: a genuine f32, by annotation or by an `as f32`
// cast, still has to round.
func TestSelfHostFloatAliasIsF64(t *testing.T) {
	const pick = `function pick(bits: i64): i32 {
    if (bits == 4599676419421066581i64) { return 1; }
    if (bits == 4599676419600023552i64) { return 2; }
    return 3;
}
`
	const f64Precision, f32Rounded = 1, 2
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name string
		prog string
		want int
	}{
		{
			"float-local",
			`function main(): i32 { let a: float = 1.0; return pick(f64_bits(a / 3.0)); }`,
			f64Precision,
		},
		{
			"float-param",
			`function f(x: float): i64 { return f64_bits(x / 3.0); }
function main(): i32 { return pick(f(1.0)); }`,
			f64Precision,
		},
		{
			// The propagation case: `d` is declared f64 and must stay f64 even
			// though its initialiser is a `float` local.
			"f64-local-from-float-local",
			`function main(): i32 { let c: float = 1.0; let d: f64 = c; return pick(f64_bits(d / 3.0)); }`,
			f64Precision,
		},
		{
			"f32-local-still-rounds",
			`function main(): i32 { let a: f32 = 1.0; return pick(f64_bits((a / 3.0) as f64)); }`,
			f32Rounded,
		},
		{
			"f32-param-still-rounds",
			`function g(x: f32): i64 { return f64_bits((x / 3.0) as f64); }
function main(): i32 { return pick(g(1.0)); }`,
			f32Rounded,
		},
		{
			"as-f32-cast-still-rounds",
			`function main(): i32 { let a: f64 = 1.0; let b = a as f32; return pick(f64_bits((b / 3.0) as f64)); }`,
			f32Rounded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, pick+tc.prog+"\n", "x86-64-linux")
			if exit != tc.want {
				t.Errorf("exit %d, want %d (1 = f64 precision, 2 = f32-rounded, 3 = neither)\n%s", exit, tc.want, stderr)
			}
		})
	}
}
