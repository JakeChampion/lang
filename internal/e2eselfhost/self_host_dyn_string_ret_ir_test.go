package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
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
	// The SIBLING classifiers had the same dyn hole (found by probing
	// after the expr_is_str fix): a string[]-returning dyn method with a
	// chained element read, an i64-returning one in 64-bit arithmetic,
	// and an f64-returning one in float arithmetic.
	// d.names()[1].len() = len("worlds!") = 7.
	{"strarr-ret-chained",
		`trait N { function names(self: Self): string[]; } struct P { a: string, b: string } impl N for P { function names(self: Self): string[] { return [self.a, self.b]; } } function main(): i32 { var p: P = P { a: "hello", b: "worlds!" }; var d: dyn N = p; return d.names()[1].len(); }`, 7},
	// d.big() = 6e9 (needs 64-bit tracking); / 1e9 = 6.
	{"i64-ret-chained",
		`trait G { function big(self: Self): i64; } struct Q { n: i32 } impl G for Q { function big(self: Self): i64 { return (self.n as i64) * 3000000000; } } function main(): i32 { var q: Q = Q { n: 2 }; var d: dyn G = q; var v: i64 = d.big() / 1000000000; return v as i32; }`, 6},
	// (d.ratio() * 2.0) as i32 = (2.5 * 2.0) = 5.
	{"f64-ret-chained",
		`trait F { function ratio(self: Self): f64; } struct Q { n: i32 } impl F for Q { function ratio(self: Self): f64 { return (self.n as f64) / 4.0; } } function main(): i32 { var q: Q = Q { n: 10 }; var d: dyn F = q; return (d.ratio() * 2.0) as i32; }`, 5},
	// Struct-returning dyn method with chained field access — was
	// already correct; regression guard.
	{"struct-ret-chained",
		`trait S { function mk(self: Self): P2; } struct P2 { x: i32, y: i32 } struct Q { n: i32 } impl S for Q { function mk(self: Self): P2 { return P2 { x: self.n, y: self.n + 1 }; } } function main(): i32 { var q: Q = Q { n: 20 }; var d: dyn S = q; return d.mk().y; }`, 21},
}

// TestSelfHostDynStringRetIRX86_64 routes each case through the
// self-hosted x86-64 driver (asm_run) and asserts the exit code, AND
// probes the routing (asm_pathprobe_run) to pin each case to the "ir"
// path.
func TestSelfHostDynStringRetIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
