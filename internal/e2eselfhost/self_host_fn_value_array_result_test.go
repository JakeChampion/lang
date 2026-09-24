package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// An array returned through a function value takes its element width from
// the value's declared result (#9496). iter.flat_map at f64 walked the
// lambda's f64[] result at the 4-byte stride on the wasm AST lowering, which
// pushed an i32 into an f64 push (an invalid module once the clone was fully
// concrete, a wrong answer before). Each element kind is here, and a user
// function iterating a `(i32) => f64[]` result directly.
const fnValueArrayResultSrc = `import "core/iter" as iter;
function sum_through(f: (i32) => f64[], n: i32): f64 {
    var t: f64 = 0.0;
    var i: i32 = 0;
    while (i < n) { for y in f(i) { t = t + y; } i = i + 1; }
    return t;
}
function main(): i32 {
    var xs: f64[] = [1.5, 2.5];
    var fo = iter.flat_map(iter.of(xs), (x: f64): f64[] => { return [x, x * 2.0]; });
    var ls: i64[] = [4000000000i64, 5i64];
    var lo = iter.flat_map(iter.of(ls), (x: i64): i64[] => { return [x, x + 1i64]; });
    var ss: string[] = ["ab", "cde"];
    var so = iter.flat_map(iter.of(ss), (x: string): string[] => { return [x, x + "!"]; });
    var is: i32[] = [3, 4];
    var io = iter.flat_map(iter.of(is), (x: i32): i32[] => { return [x, x * 10]; });
    var t: f64 = sum_through((k: i32): f64[] => { return [k as f64 * 0.5, 1.25]; }, 4);
    var r: i32 = (fo[0] as i32) + (fo[1] as i32) + (fo[3] as i32);
    if (lo[1] != 4000000001i64 || lo[3] != 6i64) { return 90; }
    r = r + so[1].len() + so[3].len() + io[1] + io[3] + (t * 4.0) as i32;
    return r % 251;
}
`

func TestSelfHostFnValueArrayResult(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "fn_value_array_result.fern")
	if err := os.WriteFile(src, []byte(fnValueArrayResultSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, lw := range []struct{ name, env string }{{"semantic", "FERN_SEM_IR=1"}, {"ast", "FERN_SEM_IR="}} {
		t.Run("wasm32-wasi/"+lw.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", lw.env))
			if exit != 118 {
				t.Fatalf("exit=%d, want 118\n%s", exit, stderr)
			}
		})
		t.Run("x86-64/"+lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != 118 {
				t.Fatalf("exit=%d, want 118 (stderr %q)", exit, stderr)
			}
		})
	}
}
