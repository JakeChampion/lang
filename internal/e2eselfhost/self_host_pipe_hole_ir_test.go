package e2eselfhost

import (
	"testing"
)

// The `_` topic placeholder in piped calls (`x |> f(a, _)` → `f(a, x)`)
// desugars in the self-host parser's pipe_desugar (mirroring the native
// parsePipe): a direct `_` arg is replaced by the piped LHS instead of
// the LHS being prepended. Pure parse-time rewrite — the self-host IR
// path sees an ordinary ExprCall. Exit codes oracle-check the arithmetic.
var pipeHoleIRCases = []struct {
	name string
	main string
	want int
}{
	// Hole in the second slot: sub(10, 3) = 7.
	{"hole-second", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { var x: i32 = 3; return x |> sub(10, _); }`, 7},
	// Position-distinguishing middle slot: pick(1, 20, 3) = 1 + 200 + 3.
	{"hole-middle", `function pick(a: i32, b: i32, c: i32): i32 { return a + b * 10 + c; }
function main(): i32 { return 20 |> pick(1, _, 3); }`, 204},
	// Nested pipes: inner hole binds inner LHS → sub(20, sub(5, 3)) = 18.
	{"nested", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { var x: i32 = 3; return 20 |> sub(_, x |> sub(5, _)); }`, 18},
	// Chained hole stages: sub(9,4)=5, then sub(8,5)=3.
	{"chained", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 4 |> sub(9, _) |> sub(8, _); }`, 3},
	// A plain (prepending) stage after a hole stage still prepends.
	{"hole-then-prepend", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 4 |> sub(10, _) |> sub(2); }`, 4},
	// The hole as a named argument's value: diff(a = 0, b = 9) = -9 (#10121).
	{"named-hole", `function diff(a: i32 = 0, b: i32 = 0): i32 { return a - b; }
function main(): i32 { return (9 |> diff(b = _)) + 20; }`, 11},
	// A named hole after a positional argument: pick(1, 0, 20) = 1 + 0 + 20.
	{"named-hole-after-positional", `function pick(a: i32, b: i32 = 0, c: i32 = 0): i32 { return a + b * 10 + c; }
function main(): i32 { return 20 |> pick(1, c = _); }`, 21},
}

// TestSelfHostPipeHoleIRX86_64 compiles each case with the self-host CLI for
// x86-64 and checks the exit code.
func TestSelfHostPipeHoleIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	for _, tc := range pipeHoleIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != tc.want {
				t.Errorf("%s exited %d, want %d\n%s", tc.name, code, tc.want, stderr)
			}
		})
	}
}
