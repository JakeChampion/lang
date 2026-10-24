package e2ecompiler

import (
	"testing"
)

var dynStringRetIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"chained-len",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; return d.name().len(); }`, 5},
	{"chained-index",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; if (d.name()[0] == 104) { return 30; } return 3; }`, 30},
	{"chained-concat",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; return (d.name() + "!").len() + 40; }`, 46},
	{"via-var",
		`trait Named { function name(self: Self): string; } struct P { tag: string } impl Named for P { function name(self: Self): string { return self.tag; } } function main(): i32 { let p: P = P { tag: "hello" }; let d: dyn Named = p; let s: string = d.name(); return s.len() + 50; }`, 55},
	{"i32-ret-regression",
		`trait V { function v(self: Self): i32; } struct Q { n: i32 } impl V for Q { function v(self: Self): i32 { return self.n; } } function main(): i32 { let q: Q = Q { n: 60 }; let d: dyn V = q; return d.v() + 1; }`, 61},
	{"arr-ret-regression",
		`trait M { function make(self: Self): i32[]; } struct R { n: i32 } impl M for R { function make(self: Self): i32[] { return [self.n, self.n]; } } function main(): i32 { let r: R = R { n: 5 }; let d: dyn M = r; return d.make().len() + 70; }`, 72},
}

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
