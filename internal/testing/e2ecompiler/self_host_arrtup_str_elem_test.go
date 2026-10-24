package e2ecompiler

import (
	"os/exec"
	"testing"
)

const arrTupStrLiteralLenSrc = `function w(i: i32): string { let t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    let ps: (i32, string)[] = [(i, w(i)), (i + 1, w(i + 1))];
    return ps.len() + i % 3;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const arrTupStrErasedGenericSrc = `function w(i: i32): string { let t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function count[T](xs: T[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    let ps: (i32, string)[] = [(i, w(i)), (i + 1, w(i + 1))];
    return count(ps) + i % 3;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const arrTupArrErasedGenericSrc = `function count[T](xs: T[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    let ps: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2])];
    return count(ps) + i % 3;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const arrTupMixedErasedGenericSrc = `function w(i: i32): string { let t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function count[T](xs: T[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    let ps: (i32, string, i32[])[] = [(i, w(i), [i]), (i + 1, w(i + 1), [i + 1])];
    return count(ps) + i % 3;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

func TestSelfHostArrTupStrElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping array-of-tuples wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, src string }{
		{"str_literal_len", arrTupStrLiteralLenSrc},
		{"str_erased_generic", arrTupStrErasedGenericSrc},
		{"arr_erased_generic", arrTupArrErasedGenericSrc},
		{"mixed_erased_generic", arrTupMixedErasedGenericSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "arrtup_"+tc.name, wat)
			if exit != want {
				t.Fatalf("%s exited %d, want %d (interp oracle; 99 = over-release)", tc.name, exit, want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary on stderr (%q)", tc.name, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Errorf("%s: allocs=0 — the probe exercised no allocation", tc.name)
			}
			if allocs != frees || live != 0 {
				t.Errorf("%s: %s — every tuple box and its string / array must reclaim", tc.name, summary)
			}
		})
	}
}
