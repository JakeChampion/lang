package e2eselfhost

import (
	"os/exec"
	"testing"
)

// --- Arrays of tuples with string elements, and the erased-generic call (#7910 (c)) --
//
// Two refusals sat on `var ps: (i32, string)[] = [(i, w(i)), …]`, and the
// call through `count[T](xs: T[])` the issue named was only the second.
//
// The first is the element admission: the array-of-tuples credit ("ARRTUP:")
// proved each tuple's string element fresh with the SYNTACTIC test alone — a
// literal or an inline concat — so an element produced by a registered
// fresh-string function was refused and the whole array kept its shallow
// buffer-only dec. That leaked every tuple box and every string with no call
// in sight. The admission now reads the same fresh-string registry the tuple
// LOCAL's credit reads.
//
// The second is the argument position: the ARRTUP element-escape scan had no
// arm for a direct call with the array as a bare-ident argument, so the
// argument fell to the bare-ident leaf and counted as an escape — where the
// array-of-structs twin already admits a param proven to touch no element
// ("ELB:") or to keep it only counted ("CNT:"). An erased generic reading
// `xs.len()` is exactly such a param.
//
// The x86-64 and arm64 legs are the leak-matrix rows of the same names
// (native oracle, sanitize leg); this file is the wasm leg, which asserts a
// balanced census and the interpreter's exit code.

const arrTupStrLiteralLenSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    var ps: (i32, string)[] = [(i, w(i)), (i + 1, w(i + 1))];
    return ps.len() + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const arrTupStrErasedGenericSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function count[T](xs: T[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    var ps: (i32, string)[] = [(i, w(i)), (i + 1, w(i + 1))];
    return count(ps) + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const arrTupArrErasedGenericSrc = `function count[T](xs: T[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    var ps: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2])];
    return count(ps) + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const arrTupMixedErasedGenericSrc = `function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function count[T](xs: T[]): i32 { return xs.len(); }
function round(i: i32): i32 {
    var ps: (i32, string, i32[])[] = [(i, w(i), [i]), (i + 1, w(i + 1), [i + 1])];
    return count(ps) + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

func TestSelfHostArrTupStrElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping array-of-tuples wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

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
