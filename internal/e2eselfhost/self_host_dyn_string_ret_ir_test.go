package e2eselfhost

import (
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
// was validated native-first (`fern -interp` + `-target x86-64-linux`).
var dynStringRetIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// The issue reproducer: `.len()` chained on the dyn string result.
	{"chained-len",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; return d.name().len(); }`, 5},
	// Byte-index chained on the dyn string result ('h' == 104).
	{"chained-index",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; if (d.name()[0] == 104) { return 30; } return 3; }`, 30},
	// Concat chained on the dyn string result, then .len() of the sum.
	{"chained-concat",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; return (d.name() + "!").len() + 40; }`, 46},
	// Materialised via a `let` first — the shape that already worked
	// (the slot carries the type); regression guard.
	{"via-var",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; let s: string = d.name(); return s.len() + 50; }`, 55},
	// i32-returning dyn method — must stay correct (no over-tracking).
	{"i32-ret-regression",
		`trait V { function v(self: Self): i32; } struct Q { n: i32 } impl V for Q { function v(self: Self): i32 { return self.n; } } function main(): i32 { let q: Q = Q { n: 60 }; let d: dyn V = q; return d.v() + 1; }`, 61},
	// i32[]-returning dyn method with chained .len() — must stay
	// correct (reaches the array path, not the string one).
	{"arr-ret-regression",
		`trait M { function make(self: Self): i32[]; } struct R { n: i32 } impl M for R { function make(self: Self): i32[] { return [self.n, self.n]; } } function main(): i32 { let r: R = R { n: 5 }; let d: dyn M = r; return d.make().len() + 70; }`, 72},
}

// TestSelfHostDynStringRetIRX86_64 runs each case through the self-host CLI
// on x86-64-linux and asserts the exit code.
func TestSelfHostDynStringRetIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range dynStringRetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"x86-64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
