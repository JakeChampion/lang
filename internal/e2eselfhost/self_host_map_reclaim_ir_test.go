package e2eselfhost

import "testing"

// mapReclaimIRCases exercise the Perceus map-local reclaim helper
// (__fern_map_free / $__fern_map_release). Each `main` builds one or more FRESH,
// borrow-only (method-call receivers are borrows), non-escaping map locals that
// slot_is_reclaimable_map admits — so emit_map_buffers_free fires and frees the
// keys/values buffers + the mapbox at scope exit. The cases that build a SECOND
// map after the first goes dead stress the freelist: a double-free or corrupted
// mapbox from the reclaim would poison the recycled block and skew the result.
//
// This is the regression gate for the wasm defect: before the __fern_map_free
// helper, emit_map_buffers_free emitted `op_raw_load_ptr`, which the wasm backend
// did not select, leaving the operand stack imbalanced so wasmtime rejected the
// module ("values remaining on stack at end of block"). The helper routes wasm to
// $__fern_map_release instead, so a reclaimable map local now compiles+runs on
// every backend — and the emit no longer has a comment fallback to slip through
// (#6917 / #6946), so a regression fails the compile rather than the run.
var mapReclaimIRCases = []struct {
	name string
	main string
	want int
}{
	// Basic i32-keyed borrow-only map: fresh, read via get_or, never reassigned
	// or returned -> reclaimable. 2 + 4 = 6.
	{"i32-borrow-only", `var m: Map[i32, i32] = Map { 1: 2, 3: 4 }; return m.get_or(1, 0) + m.get_or(3, 0);`, 6},
	// Two sequential reclaimable maps: the first is freed at its last use, the
	// second must allocate cleanly (possibly reusing the freed blocks). 5 + 9 = 14.
	{"two-sequential", `var a: Map[i32, i32] = Map { 1: 5 }; var x: i32 = a.get_or(1, 0); var b: Map[i32, i32] = Map { 2: 9 }; return x + b.get_or(2, 0);`, 14},
	// Grown map (past the initial cap of 8) then reclaimed: exercises the freed
	// keys/vals buffers being the grown (larger) allocations. sum 1..10 = 55.
	{"grown-then-reclaimed", `var m: Map[i32, i32] = Map {}; var i: i32 = 1; while (i <= 10) { m = m.insert(i, i); i = i + 1; } var s: i32 = 0; var j: i32 = 1; while (j <= 10) { s = s + m.get_or(j, 0); j = j + 1; } return s;`, 55},
}

func mapReclaimIRSrc(mainBody string) string {
	return "import \"core/map\";\n" + "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMapReclaimIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostMapReclaimIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range mapReclaimIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, mapReclaimIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
