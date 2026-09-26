package e2eselfhost

import "testing"

// supertraitIRCases exercise supertraits (`trait Ord: Eq`) through the
// self-hosted compiler. The self-host dispatches by receiver type and
// doesn't validate conformance, so it carries no supertrait semantics —
// but it must PARSE the `: Eq` clause (parse_trait_decl skips it) and a
// bounded generic over `Ord` whose body calls the supertrait's `eq`
// method still runs: monomorphisation clones `rank` per concrete type and
// dispatches `a.eq(b)` / `a.lt(b)` to the type's methods. See docs/TRAITS.md.
var supertraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// rank(p,q)=1 (lt), rank(q,p)=9 (gt), rank(p,p)=5 (eq); sum 15.
	{"bounded-generic",
		`trait Eq { function eq(self: Self, other: Self): boolean; } trait Ord: Eq { function lt(self: Self, other: Self): boolean; } struct P { x: i32 } impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } } impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } } function rank[T: Ord](a: T, b: T): i32 { if (a.eq(b)) { return 5; } if (a.lt(b)) { return 1; } return 9; } function main(): i32 { var p: P = P { x: 3 }; var q: P = P { x: 5 }; return rank(p, q) + rank(q, p) + rank(p, p); }`, 15},
	// Direct dispatch of both the trait's and the supertrait's method.
	// p.eq(q) false → +0; p.lt(q) true → +4; 0 + 4 + 8 = 12.
	{"direct",
		`trait Eq { function eq(self: Self, other: Self): boolean; } trait Ord: Eq { function lt(self: Self, other: Self): boolean; } struct P { x: i32 } impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } } impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } } function main(): i32 { var p: P = P { x: 3 }; var q: P = P { x: 5 }; var r: i32 = 8; if (p.eq(q)) { r = r + 100; } if (p.lt(q)) { r = r + 4; } return r; }`, 12},
}

// TestSelfHostSupertraitIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
