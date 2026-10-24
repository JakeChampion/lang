package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const fnValueArrayResultSrc = `import "core/iter" as iter;
function sum_through(f: (i32) => f64[], n: i32): f64 {
    let t: f64 = 0.0;
    let i: i32 = 0;
    while (i < n) { for y in f(i) { t = t + y; } i = i + 1; }
    return t;
}
function main(): i32 {
    let xs: f64[] = [1.5, 2.5];
    let fo = iter.of(xs).flat_map((x: f64): f64[] => { return [x, x * 2.0]; }).collect();
    let ls: i64[] = [4000000000i64, 5i64];
    let lo = iter.of(ls).flat_map((x: i64): i64[] => { return [x, x + 1i64]; }).collect();
    let ss: string[] = ["ab", "cde"];
    let so = iter.of(ss).flat_map((x: string): string[] => { return [x, x + "!"]; }).collect();
    let is: i32[] = [3, 4];
    let io = iter.of(is).flat_map((x: i32): i32[] => { return [x, x * 10]; }).collect();
    let t: f64 = sum_through((k: i32): f64[] => { return [k as f64 * 0.5, 1.25]; }, 4);
    let r: i32 = (fo[0] as i32) + (fo[1] as i32) + (fo[3] as i32);
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
	t.Run("wasm32-wasi", func(t *testing.T) {
		stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi"))
		if exit != 118 {
			t.Fatalf("exit=%d, want 118\n%s", exit, stderr)
		}
	})
	t.Run("x86-64", func(t *testing.T) {
		bin := cli.x86Binary(t, src)
		stderr, exit := runWithStdin(t, cli.runner, bin, nil)
		if exit != 118 {
			t.Fatalf("exit=%d, want 118 (stderr %q)", exit, stderr)
		}
	})
}
