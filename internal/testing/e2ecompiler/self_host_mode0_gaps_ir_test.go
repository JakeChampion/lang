package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Three constructs from the mode-0 decline set (#5977). They are grouped because
// they share a shape: none is a missing FEATURE, each is a piece of type
// information the lowering has to carry one step further.
//
//   - an i64 `const` READ. A const desugars to a zero-arg accessor, so the bare
//     ident is a call returning i64.
//   - iterating a THREE-deep array. `for plane in cube` binds `plane` as an
//     array of arrays, so the third `for` level sees arrays, not scalars.
//   - an un-annotated Option ALIAS. `let u = o` carries o's Option type, so the
//     later `match (u)` recovers the payload as it would with `u` annotated.
//
// Each case asserts the `-decide` route AND the answer: a regression is a
// refused module, and the route names it.
func TestSelfHostMode0GapsIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// i64 / u64 module consts. The unused-const and i32-const controls
		// already lowered, so the gap is specifically the READ.
		{"i64-const-read", "const BIG: i64 = 5000000000;\nfunction main(): i32 { let v: i64 = BIG; return (v % 7) as i32; }", 2},
		{"i64-const-arith", "const BIG: i64 = 5000000000;\nfunction main(): i32 { return (BIG % 97) as i32; }", 73},
		{"u64-const-read", "const BIG: u64 = 18000000000;\nfunction main(): i32 { let v: u64 = BIG; return (v % 7) as i32; }", 3},
		{"i32-const-control", "const SMALL: i32 = 97;\nfunction main(): i32 { return SMALL - 55; }", 42},

		// Array nesting depth. Two levels already worked; three is the new one,
		// and the mixed control checks the inner element type still reaches the
		// innermost loop.
		{"array-3deep-foreach", "function main(): i32 { let cube: i32[][][] = [[[1]], [[2, 3]]]; let sum = 0; for plane in cube { for row in plane { for v in row { sum = sum + v; } } } return sum; }", 6},
		{"array-2deep-control", "function main(): i32 { let g: i32[][] = [[1], [2, 3]]; let sum = 0; for row in g { for v in row { sum = sum + v; } } return sum; }", 6},
		{"array-3deep-index", "function main(): i32 { let cube: i32[][][] = [[[1]], [[2, 3]]]; return cube[1][0][1]; }", 3},

		// Option alias propagation, with the annotated form as the control that
		// always worked.
		{"option-alias-match", "function main(): i32 { let o = Some(20); let u = o; match (u) { Some(x) => { return x + 22; }, None => { return 0; } } }", 42},
		{"option-alias-annotated-control", "function main(): i32 { let o: Option[i32] = Some(20); let u: Option[i32] = o; match (u) { Some(x) => { return x + 22; }, None => { return 0; } } }", 42},
		{"result-alias-match", "function mk(): Result[i32, string] { return Ok(20); }\nfunction main(): i32 { let r = mk(); let u = r; match (u) { Ok(x) => { return x + 22; }, Err(e) => { return e.len(); } } }", 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the construct no longer lowers, and the value alone would not show it", tc.name, route)
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
