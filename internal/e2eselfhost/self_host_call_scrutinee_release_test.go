package e2eselfhost

import (
	"os/exec"
	"testing"
)

// --- Enum payload positions consumed straight off a producer call (#7910 (d)) --
//
// `match (mk(i)) { … }` over a registered fresh Option / Result / rc-enum
// producer left the returned box and everything in it unreleased: the
// scrutinee lived in a scratch slot nothing swept, while the same value
// BOUND to a local first was released by the call-bound consuming-match
// path. The direct form is now that binding — hoist_call_scrutinees rewrites
// the match onto a `$mscrut_L_C` temp before any analysis — and the binding
// path's admission was widened to what these positions carry: a string[]
// success payload, a nested Option over an rc payload (the two-level guarded
// release), and payloads built by a registered fresh-string producer.
//
// The x86-64 and arm64 legs are the leak-matrix rows of the same names
// (native oracle, sanitize leg); this file is the wasm leg, which asserts a
// balanced census and the interpreter's exit code.

const callScrutResultStrArrSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function mk(i: i32): Result[string[], string] {
    if (i % 2 == 0) { return Ok([w(i)]); }
    return Err(w(i));
}
function round(i: i32): i32 {
    var t: i32 = 0;
    match (mk(i)) { Ok(xs) => { t = t + xs.len(); }, Err(e) => { t = t + e.len(); } }
    return t % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const callScrutOptOptStrArrSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function mko(i: i32): Option[Option[string[]]] {
    if (i % 2 == 0) { return Some(Some([w(i)])); }
    return None;
}
function round(i: i32): i32 {
    var t: i32 = 0;
    match (mko(i)) { Some(o) => { match (o) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 1; } } }, None => { t = t + 2; } }
    return t % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const callScrutOptStrSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function mk(i: i32): Option[string] {
    if (i % 2 == 0) { return None; }
    return Some(w(i));
}
function round(i: i32): i32 {
    var t: i32 = 0;
    match (mk(i)) { Some(s) => { t = t + s.len(); }, None => { t = t + 1; } }
    return t % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const callBoundOptStrArrSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function mk(i: i32): Option[string[]] {
    if (i % 2 == 0) { return None; }
    return Some([w(i), w(i + 1)]);
}
function round(i: i32): i32 {
    var t: i32 = 0;
    var r: Option[string[]] = mk(i);
    match (r) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 1; } }
    return t % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

// The user rc-enum producer is NOT in the wasm list below: its call-bound
// release is refused on every backend (the leak-matrix row pins that gap), so
// the shape has no balance to assert here.
const callScrutUserEnumSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
enum E { Full(string[]), Note(string), Nil }
function mk(i: i32): E {
    if (i % 3 == 0) { return E.Full([w(i)]); }
    if (i % 3 == 1) { return E.Note(w(i)); }
    return E.Nil;
}
function round(i: i32): i32 {
    var t: i32 = 0;
    match (mk(i)) { E.Full(xs) => { t = t + xs.len(); }, E.Note(s) => { t = t + s.len(); }, E.Nil => { t = t + 1; } }
    return t % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

func TestSelfHostCallScrutineeReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping call-scrutinee release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, src string }{
		{"result_strarr", callScrutResultStrArrSrc},
		{"optopt_strarr", callScrutOptOptStrArrSrc},
		{"opt_str", callScrutOptStrSrc},
		{"bound_opt_strarr", callBoundOptStrArrSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "callscrut_"+tc.name, wat)
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
				t.Errorf("%s: %s — the call's box and every payload position must reclaim", tc.name, summary)
			}
		})
	}
}
