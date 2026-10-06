package e2ecompiler

import "testing"

// TestSelfHostFormerBailsRun runs programs the AST lowering refused (#8590) —
// a loop over the innermost level of a 4-deep nested array, and a
// value-position match binding an 8-byte-element array payload — which the
// typed lowering produces. Each must answer what the interpreter does, with a
// balanced census, on both register targets. The nested array's rows go through
// id so it is built on the heap rather than placed as a constant.
func TestSelfHostFormerBailsRun(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"nested-for", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let hyper: i32[][][][] = [[[id([1])], [id([2, 3])]]];
    let sum = 0;
    for cube in hyper { for plane in cube { for row in plane { for v in row { sum = sum + v; } } } }
    return sum;
}
`, 6},
		{"iife-value-block", `enum W { Wide(i64[]), Empty }
function main(): i32 {
    let w: W = Wide([5i64, 6i64]);
    let u: i64 = (match (w) { Wide(xs) => xs[0], Empty => 9i64 });
    return (u as i32) & 255i32;
}
`, 5},
	} {
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
				if exit != tc.want {
					t.Fatalf("exit %d, want %d\n%s", exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
