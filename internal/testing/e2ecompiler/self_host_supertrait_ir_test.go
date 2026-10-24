package e2ecompiler

import "testing"

var supertraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"bounded-generic",
		`trait Eq { function eq(self: Self, other: Self): boolean; } trait Ord: Eq { function lt(self: Self, other: Self): boolean; } struct P { x: i32 } impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } } impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } } function rank[T: Ord](a: T, b: T): i32 { if (a.eq(b)) { return 5; } if (a.lt(b)) { return 1; } return 9; } function main(): i32 { let p: P = P { x: 3 }; let q: P = P { x: 5 }; return rank(p, q) + rank(q, p) + rank(p, p); }`, 15},
	{"direct",
		`trait Eq { function eq(self: Self, other: Self): boolean; } trait Ord: Eq { function lt(self: Self, other: Self): boolean; } struct P { x: i32 } impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } } impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } } function main(): i32 { let p: P = P { x: 3 }; let q: P = P { x: 5 }; let r: i32 = 8; if (p.eq(q)) { r = r + 100; } if (p.lt(q)) { r = r + 4; } return r; }`, 12},
}

func TestSelfHostSupertraitIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range supertraitIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
