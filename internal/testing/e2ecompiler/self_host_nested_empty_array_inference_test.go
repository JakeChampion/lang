package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostNestedEmptyArrayInference(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, declarations, body string }{
		{"empty-first", "", `let n = 0; for xs in [[], [1]] { n = n + xs.len(); } return n - 1;`},
		{"empty-last-control", "", `let n = 0; for xs in [[1], []] { n = n + xs.len(); } return n - 1;`},
		{"two-level", "", `let n = 0; for xs in [[[]], [[1]]] { for ys in xs { n = n + ys.len(); } } return n - 1;`},
		{"three-level", "", `let n = 0; for xs in [[[[]]], [[[1]]]] { for ys in xs { for zs in ys { n = n + zs.len(); } } } return n - 1;`},
		{"owned-string", "", `let n = 0; for xs in [[], [seed]] { for text in xs { if (text != seed) { return 2; } n = n + 1; } } return n - 1;`},
		{"generic-record", `struct Box[T] { value: T }`, `let n = 0; for xs in [[], [Box { value: seed }]] { for item in xs { if (item.value != seed) { return 2; } n = n + 1; } } return n - 1;`},
		{"generic-numeric-literal", `struct Box[T] { value: T }`, `let sum = 0i64; for xs in [[], [Box { value: 1 }], [Box { value: 7i64 }]] { for item in xs { sum = sum + item.value; } } return (sum - 8i64) as i32;`},
		{"typed-tail", "", `let sum = 0i64; for xs in [[], [7i64]] { for x in xs { sum = sum + x; } } return (sum - 7i64) as i32;`},
		{"explicit-control", "", `let arrays: i32[][] = [[], [1]]; let n = 0; for xs in arrays { n = n + xs.len(); } return n - 1;`},
		{"argument-control", `function count(arrays: [i32[]]): i32 { let n = 0; for xs in arrays { n = n + xs.len(); } return n; }`, `return count([[], [1]]) - 1;`},
		{"side-effect-order", `function make(state: Cell[i32], value: i32): i32[] { state.set(state.get() * 10 + value); return [value]; }`, `let state = cell_new(0); let sum = 0; for xs in [[], make(state, 1), make(state, 2)] { for x in xs { sum = sum + x; } } return if (sum == 3 && state.get() == 12) { 0 } else { 2 };`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			source := tc.declarations + "\nfunction main(): i32 { let seed = args()[0] + \"-nested-array-owned-value\"; if (seed.len() < 24) { return 90; } " + tc.body + " }\n"
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"-check", "-interp"} {
				if out, err := exec.Command(oracle, mode, path).CombinedOutput(); err != nil {
					t.Fatalf("Go %s: %v\n%s", mode, err, out)
				}
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("nested array exit %d\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

func TestSelfHostNestedEmptyArrayRejectsMixedElements(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	codes := regexp.MustCompile(`error\[E[0-9]+\]`)
	for _, tc := range []struct{ name, declarations, setup, elements string }{
		{"integer-string", "", "", `[[], [1], ["text"]]`},
		{"string-integer", "", "", `[[], ["text"], [1]]`},
		{"nested-depth", "", "", `[[[]], [[1]], [["text"]]]`},
		{"nominal-records", `struct A { n: i32 } struct B { n: i32 }`, "", `[[], [A { n: 1 }], [B { n: 1 }]]`},
		{"generic-records", `struct Box[T] { value: T }`, "", `[[], [Box { value: 1 }], [Box { value: "text" }]]`},
		{"array-depth", "", "", `[[], [1], [[1]]]`},
		{"owned-view", "", `let owned = [1]; let borrowed: [i32] = owned[:];`, `[[], [owned], [borrowed]]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			source := tc.declarations + "\nfunction main(): i32 { " + tc.setup + " for xs in " + tc.elements + " { let count = xs.len(); } return 0; }\n"
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, goErr := exec.Command(oracle, "-check", path).CombinedOutput()
			if goErr == nil || !strings.Contains(string(goOut), "error[E034]") {
				t.Fatalf("Go expected E034: %v\n%s", goErr, goOut)
			}
			wantCodes := strings.Join(codes.FindAllString(string(goOut), -1), ",")
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path).CombinedOutput()
					gotCodes := strings.Join(codes.FindAllString(string(out), -1), ",")
					if err == nil || gotCodes != wantCodes {
						t.Fatalf("expected diagnostic sequence %s, got %s: %v\n%s", wantCodes, gotCodes, err, out)
					}
				})
			}
		})
	}
}
