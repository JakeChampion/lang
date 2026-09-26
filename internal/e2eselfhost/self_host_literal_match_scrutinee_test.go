package e2eselfhost

import "testing"

// literalMatchScrutineeCases pin that a match on a scalar scrutinee evaluates
// it once. The literal-pattern desugar tests every arm against the scrutinee,
// so a call written there ran once per arm it reached (and twice for a range
// arm, which reads it at both ends). Each program counts the calls in a cell
// and folds the count into its exit code, checked against native.
var literalMatchScrutineeCases = []struct {
	name string
	src  string
	want int
}{
	// The third arm matches: one call, not three. r = 3, calls = 1.
	{"statement_int", `function tick(c: Cell[i32]): i32 { c.set(c.get() + 1); return c.get() + 4; }
function main(): i32 { var c: Cell[i32] = cell_new(0); var r: i32 = 0; match (tick(c)) { 3 => { r = 1; }, 4 => { r = 2; }, 5 => { r = 3; }, _ => { r = 4; } } return r * 10 + c.get(); }`, 31},
	{"value_int", `function tick(c: Cell[i32]): i32 { c.set(c.get() + 1); return c.get() + 4; }
function main(): i32 { var c: Cell[i32] = cell_new(0); var r: i32 = match (tick(c)) { 3 => 1, 4 => 2, 5 => 3, _ => 4 }; return r * 10 + c.get(); }`, 31},
	// A range arm reads the scrutinee at both bounds.
	{"range", `function tick(c: Cell[i32]): i32 { c.set(c.get() + 1); return c.get() + 6; }
function main(): i32 { var c: Cell[i32] = cell_new(0); var r: i32 = match (tick(c)) { 0..5 => 1, 5..=9 => 2, _ => 3 }; return r * 10 + c.get(); }`, 21},
	{"string", `import "std/string";
function next(c: Cell[i32]): string { c.set(c.get() + 1); if (c.get() == 1) { return "b"; } return "a"; }
function main(): i32 { var c: Cell[i32] = cell_new(0); var r: i32 = match (next(c)) { "a" => 1, "b" => 2, _ => 3 }; return r * 10 + c.get(); }`, 21},
	// Boolean arms take the same desugar as any other literal.
	{"boolean_value", `function tick(c: Cell[i32]): boolean { c.set(c.get() + 1); return c.get() > 5; }
function main(): i32 { var c: Cell[i32] = cell_new(0); var r: i32 = match (tick(c)) { true => 5, false => 6, _ => 7 }; return r * 10 + c.get(); }`, 61},
	{"boolean_statement", `function main(): i32 { var n: i32 = 3; var r: i32 = 0; match (n > 2) { true => { r = 4; }, _ => { r = 9; } } return r; }`, 4},
	// A bare name is read as written: nothing to cache.
	{"name", `function main(): i32 { var n: i32 = 4; return match (n) { 3 => 1, 4 => 2, _ => 3 }; }`, 2},
}

func TestSelfHostLiteralMatchScrutinee(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range literalMatchScrutineeCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
