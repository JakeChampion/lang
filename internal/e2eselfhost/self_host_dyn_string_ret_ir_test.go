package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// dynStringRetIRCases pin #5142: a string method chained directly on a
// `dyn Trait` dispatch's STRING result must lower against the string
// layout. Traits erase before lowering, so the dispatch result carried
// no type and `d.name().len()` fell to the generic-deref lowering —
// reading the string DATA word as the length (0 / garbage). The fix
// resolves the result type name-based off the qualified
// "<Type>.<method>" return-registry entries, exactly how the backend
// enumerates the dispatch arms. Exit codes are the oracle; every case
// was validated native-first (`fern -interp` + `-target x86-64`).
var dynStringRetIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// The issue reproducer: `.len()` chained on the dyn string result.
	{"chained-len",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "hello" }; var d: dyn Named = p; return d.name().len(); }`, 5},
	// Byte-index chained on the dyn string result ('h' == 104).
	{"chained-index",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "hello" }; var d: dyn Named = p; if (d.name()[0] == 104) { return 30; } return 3; }`, 30},
	// Concat chained on the dyn string result, then .len() of the sum.
	{"chained-concat",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "hello" }; var d: dyn Named = p; return (d.name() + "!").len() + 40; }`, 46},
	// Materialised via a `var` first — the shape that already worked
	// (the slot carries the type); regression guard.
	{"via-var",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { var p: P = P { tag: "hello" }; var d: dyn Named = p; var s: string = d.name(); return s.len() + 50; }`, 55},
	// i32-returning dyn method — must stay correct (no over-tracking).
	{"i32-ret-regression",
		`trait V { function v(self: Self): i32; } struct Q { n: i32 } impl V for Q { function v(self: Self): i32 { return self.n; } } function main(): i32 { var q: Q = Q { n: 60 }; var d: dyn V = q; return d.v() + 1; }`, 61},
	// i32[]-returning dyn method with chained .len() — must stay
	// correct (reaches the array path, not the string one).
	{"arr-ret-regression",
		`trait M { function make(self: Self): i32[]; } struct R { n: i32 } impl M for R { function make(self: Self): i32[] { return [self.n, self.n]; } } function main(): i32 { var r: R = R { n: 5 }; var d: dyn M = r; return d.make().len() + 70; }`, 72},
}

// TestSelfHostDynStringRetIRX86_64 routes each case through the
// self-hosted x86-64 driver (asm_run) and asserts the exit code, AND
// probes the routing (asm_pathprobe_run) to pin each case to the "ir"
// path.
func TestSelfHostDynStringRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynStringRetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
