package e2ecompiler

import "testing"

// mapReclaimIRCases exercise the release of map locals. Each `main` builds
// one or more FRESH, borrow-only (method-call receivers are borrows),
// non-escaping map locals, released at scope exit. The cases that build a
// SECOND map after the first goes dead stress the freelist: a double-free or
// corrupted map from the release would poison the recycled block and skew the
// result.
var mapReclaimIRCases = []struct {
	name string
	main string
	want int
}{
	// Basic i32-keyed borrow-only map: fresh, read via get_or, never reassigned
	// or returned -> reclaimable. 2 + 4 = 6.
	{"i32-borrow-only", `let m: Map[i32, i32] = Map { 1: 2, 3: 4 }; return m.get_or(1, 0) + m.get_or(3, 0);`, 6},
	// Two sequential reclaimable maps: the first is freed at its last use, the
	// second must allocate cleanly (possibly reusing the freed blocks). 5 + 9 = 14.
	{"two-sequential", `let a: Map[i32, i32] = Map { 1: 5 }; let x: i32 = a.get_or(1, 0); let b: Map[i32, i32] = Map { 2: 9 }; return x + b.get_or(2, 0);`, 14},
	// Grown map (past the initial cap of 8) then reclaimed: exercises the freed
	// keys/vals buffers being the grown (larger) allocations. sum 1..10 = 55.
	{"grown-then-reclaimed", `let m: Map[i32, i32] = Map {}; let i: i32 = 1; while (i <= 10) { m = m.insert(i, i); i = i + 1; } let s: i32 = 0; let j: i32 = 1; while (j <= 10) { s = s + m.get_or(j, 0); j = j + 1; } return s;`, 55},
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
