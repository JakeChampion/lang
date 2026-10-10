package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFormerBailsRun runs shapes that once failed to lower (#8590):
// a loop over a 4-deep nested array and a value-position match binding an
// 8-byte-element array payload. Keep the original static match and a runtime
// counterpart, checking the result and allocation census on both native targets.
func TestSelfHostFormerBailsRun(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name string
		src  string
		want int
		body string
	}{
		{"nested-for", `@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let hyper: i32[][][][] = [[[id([1])], [id([2, 3])]]];
    let sum = 0;
    for cube in hyper { for plane in cube { for row in plane { for v in row { sum = sum + v; } } } }
    return sum;
}
`, 6, ""},
		{"iife-value-block", `enum W { Wide(i64[]), Empty }
function main(): i32 {
    let w: W = Wide([5i64, 6i64]);
    let u: i64 = (match (w) { Wide(xs) => xs[0], Empty => 9i64 });
    return (u as i32) & 255i32;
}
`, 5, "main"},
		// The program name supplies args length 1, retaining payloads 5 and 6.
		{"iife-runtime-value-block", `enum W { Wide(i64[]), Empty }
@noinline function probe(n: i64): i32 {
    let w: W = Wide([n + 4i64, n + 5i64]);
    let u: i64 = (match (w) { Wide(xs) => xs[0], Empty => 9i64 });
    return (u as i32) & 255i32;
}
function main(): i32 { return probe(args().len() as i64); }
`, 5, "probe"},
	} {
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				if tc.body != "" {
					src := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
						t.Fatal(err)
					}
					asm, err := os.ReadFile(cli.emit(t, src, target, "FERN_LEAKCHECK=1"))
					if err != nil {
						t.Fatal(err)
					}
					// Both native emitters delimit function bodies with CFI. Scope
					// these checks to exclude argv allocation/release in main.
					body := selfHostFnBody(t, asm, tc.body)
					heap := tc.body == "probe"
					for _, helper := range []string{"__fern_arr_box", "__fn___fern_arr_dec"} {
						if strings.Contains(body, helper) != heap {
							t.Errorf("%s in %s: want %t\n%s", helper, tc.body, heap, body)
						}
					}
					if !heap && !strings.Contains(body, ".K0") {
						t.Errorf("no static payload in main\n%s", body)
					}
				}
				stderr, exit := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
				if exit != tc.want {
					t.Fatalf("exit %d, want %d\n%s", exit, tc.want, stderr)
				}
				if tc.body == "main" {
					if summary := leakSummaryLine(stderr); summary != "leakcheck: allocs=0 frees=0 live_bytes=0" {
						t.Errorf("static payload census: %q", summary)
					}
				} else {
					assertBalancedCensus(t, stderr)
				}
			})
		}
	}
}
