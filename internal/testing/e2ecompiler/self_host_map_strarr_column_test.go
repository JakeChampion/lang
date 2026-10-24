package e2ecompiler

import (
	"os/exec"
	"testing"
)

const mapStrArrColumnInsertGetOrSrc = `import "core/map";
function w(i: i32): string { let t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    let m: Map[i32, string[]] = Map {};
    m = m.insert(i, [w(i), w(i + 1)]);
    return m.get_or(i, []).len() + i % 3;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const mapStrArrColumnLiteralLenSrc = `import "core/map";
function w(i: i32): string { let t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    let m: Map[i32, string[]] = Map { 1: [w(i), w(i + 1)], 2: [w(i + 2)] };
    return m.len() + i % 3;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

func TestSelfHostMapStrArrColumnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map string[]-column wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

	for _, tc := range []struct{ name, src string }{
		{"insert_get_or", mapStrArrColumnInsertGetOrSrc},
		{"literal_len", mapStrArrColumnLiteralLenSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat, stderr, err := loadCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"}, "-target", "wasm32-wasi")
			if err != nil {
				t.Fatalf("compile: %v\n%s", err, stderr)
			}
			stderr, exit := wasmLcRun(t, dir, "mapsa_"+tc.name, wat)
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
				t.Errorf("%s: %s — every value's strings and buffer must reclaim with the column", tc.name, summary)
			}
		})
	}
}
