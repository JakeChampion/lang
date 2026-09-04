package e2eselfhost

import (
	"os/exec"
	"testing"
)

// --- Map STRUCT value columns with rc fields (#7910 (b)) ---------------------
//
// A `Map[K, S]` whose value struct carries a string or array field. The
// column's one-dec free (__fern_map_free_va) takes each value box, so every
// box must first release its own fields: irlower routes such a column through
// __map_vals_struct_drop_<S>, a per-type helper each backend hand-writes over
// its own map layout (the raw {keys@0, vals@8} pair on the register backends,
// the rc-headered cap/vals/used box on wasm), walking sole-owned values through
// __struct_drop_<S>. The all-scalar struct column was already reclaimed; a
// struct with an rc field was refused the credit outright and leaked box and
// field alike.
//
// The insert-built form also needs the map's GROW to free its superseded
// buffers: a struct column is a flag-1 (raw-alias) column, so the type-level
// owncols bit stays clear, and the map local's own "MAPOWN:" credit — no
// keys() / values() / for-in read anywhere in the body — is what lets the
// insert take the reclaim-on-grow push instead.
//
// The x86-64 and arm64 legs are the leak-matrix rows of the same name
// (native oracle, sanitize leg); this file is the wasm leg, the backend with
// no native leak detector to compare against, so it asserts the census
// balances and the interpreter's exit code.

const mapStructColumnInsertMatchSrc = `import "core/map";
struct S { name: string, k: i32 }
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    var t: i32 = 0;
    var m: Map[i32, S] = Map {};
    m = m.insert(i, S { name: w(i), k: i });
    match (m.get(i)) { Some(s) => { t = t + s.k + s.name.len(); }, None => { t = t - 1000; } }
    return t % 101;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const mapStructColumnLiteralLenSrc = `import "core/map";
struct S { name: string, k: i32 }
function w(i: i32): string { var t: string = "x"; if (i % 2 == 0) { t = "yy"; } return "v-a-wide-payload-past-any-inline-threshold-" + t; }
function round(i: i32): i32 {
    var m: Map[i32, S] = Map { 1: S { name: w(i), k: i }, 2: S { name: w(i + 1), k: i + 1 } };
    return m.len() + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

const mapStructColumnArrFieldInsertSrc = `import "core/map";
struct A { xs: i32[], k: i32 }
function round(i: i32): i32 {
    var m: Map[i32, A] = Map {};
    m = m.insert(i, A { xs: [i, i + 1], k: i });
    return m.len() + i % 3;
}
function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }
`

func TestSelfHostMapStructColumnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map struct-column wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, src string }{
		{"strfield_insert_match", mapStructColumnInsertMatchSrc},
		{"strfield_literal_len", mapStructColumnLiteralLenSrc},
		{"arrfield_insert_len", mapStructColumnArrFieldInsertSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "mapsc_"+tc.name, wat)
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
				t.Errorf("%s: %s — the struct value column must reclaim its boxes AND their fields", tc.name, summary)
			}
		})
	}
}
