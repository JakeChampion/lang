package e2eselfhost

import (
	"os/exec"
	"testing"
)

// --- Map value columns holding `string[]` (#7910 (a), the self-host half) ----
//
// `Map[K, string[]]`: the box-column credit refused a pointer-element array
// value outright (one dec per element is not the whole release of a string[]),
// so the column fell to the shallow map free and every value — its buffer and
// every string in it — leaked. The credit now admits a string[] column whose
// every inserted value is an array literal of literals or registered
// fresh-string producers, and the map free routes it through the _vsa
// variants: each value released whole by __fern_str_arr_free on the register
// backends, by $__fern_arr_dec_ptr in the wasm release's value walk.
//
// The x86-64 and arm64 legs are the leak-matrix rows of the same names
// (native oracle — clean since the native (a) commit — and the sanitize leg);
// this file is the wasm leg, which asserts a balanced census and the
// interpreter's exit code.

const mapStrArrColumnInsertGetOrSrc = `import "core/map";
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    var m: Map[i32, string[]] = Map {};
    m = m.insert(i, [w(i), w(i + 1)]);
    return m.get_or(i, []).len() + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const mapStrArrColumnLiteralLenSrc = `import "core/map";
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    var m: Map[i32, string[]] = Map { 1: [w(i), w(i + 1)], 2: [w(i + 2)] };
    return m.len() + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

func TestSelfHostMapStrArrColumnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map string[]-column wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, src string }{
		{"insert_get_or", mapStrArrColumnInsertGetOrSrc},
		{"literal_len", mapStrArrColumnLiteralLenSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
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
