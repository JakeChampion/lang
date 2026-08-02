package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Three constructs from the enumerated mode-0 decline set (#5977), each of which
// sent its whole module to the legacy AST emitter.
//
// They are grouped because they share a shape: none is a missing FEATURE, each is
// a piece of type information the IR lowering fails to carry one step further
// than it already does.
//
//   - an i64 `const` READ. A const desugars to a zero-arg accessor, so the bare
//     ident is a call returning i64. infer_expr_width already knew that; lower_i64's
//     ident arm did not, so every use of an i64 const bailed.
//   - iterating a THREE-deep array. `arrarr_elem` could only name a scalar inner
//     kind, so `for plane in cube` bound `plane` as a plain array and the third
//     `for` level saw a scalar.
//   - an un-annotated Option ALIAS. `var u = o` did not copy o's opt_type, so the
//     later `match (u)` could not recover the payload — annotating `u` worked,
//     which is what made this look like a match gap rather than a propagation one.
//
// Each case asserts the `-decide` route AND the answer, because a regression here
// is silent: the AST emitter computes these correctly, so only the route shows it.
func TestSelfHostMode0GapsIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// i64 / u64 module consts. The unused-const and i32-const controls
		// already lowered, so the gap is specifically the READ.
		{"i64-const-read", "const BIG: i64 = 5000000000;\nfunction main(): i32 { var v: i64 = BIG; return (v % 7) as i32; }", 2},
		{"i64-const-arith", "const BIG: i64 = 5000000000;\nfunction main(): i32 { return (BIG % 97) as i32; }", 73},
		{"u64-const-read", "const BIG: u64 = 18000000000;\nfunction main(): i32 { var v: u64 = BIG; return (v % 7) as i32; }", 3},
		{"i32-const-control", "const SMALL: i32 = 97;\nfunction main(): i32 { return SMALL - 55; }", 42},

		// Array nesting depth. Two levels already worked; three is the new one,
		// and the mixed control checks the inner element type still reaches the
		// innermost loop.
		{"array-3deep-foreach", "function main(): i32 { var cube: i32[][][] = [[[1]], [[2, 3]]]; var sum = 0; for plane in cube { for row in plane { for v in row { sum = sum + v; } } } return sum; }", 6},
		{"array-2deep-control", "function main(): i32 { var g: i32[][] = [[1], [2, 3]]; var sum = 0; for row in g { for v in row { sum = sum + v; } } return sum; }", 6},
		{"array-3deep-index", "function main(): i32 { var cube: i32[][][] = [[[1]], [[2, 3]]]; return cube[1][0][1]; }", 3},

		// Option alias propagation, with the annotated form as the control that
		// always worked.
		{"option-alias-match", "function main(): i32 { var o = Some(20); var u = o; match (u) { Some(x) => { return x + 22; }, None => { return 0; } } }", 42},
		{"option-alias-annotated-control", "function main(): i32 { var o: Option[i32] = Some(20); var u: Option[i32] = o; match (u) { Some(x) => { return x + 22; }, None => { return 0; } } }", 42},
		{"result-alias-match", "function mk(): Result[i32, string] { return Ok(20); }\nfunction main(): i32 { var r = mk(); var u = r; match (u) { Ok(x) => { return x + 22; }, Err(e) => { return e.len(); } } }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the construct is back on the AST emitter, which computes it correctly, so only the route shows it", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, "run", watPath)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
