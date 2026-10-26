package e2ecompiler

import "testing"

func TestSelfHostBoundedValueBlockNumeric(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	const prefix = `import "core/cmp";
import "std/f64";
function show[T: cmp.Display](x: T): string { return x.to_string(); }
`
	for _, tc := range []struct {
		name, setup, value, want string
	}{
		{"direct-integer", "", `if (true) { 3 } else { 2.5 }`, "3"},
		{"direct-float", "", `if (false) { 3 } else { 2.5 }`, "2.5"},
		{"local", `let v = if (true) { 3 } else { 2.5 };`, "v", "3"},
		{"reverse-arms", "", `if (false) { 2.5 } else { 3 }`, "3"},
		{"declared-control", `let v: f64 = if (true) { 3 } else { 2.5 };`, "v", "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + `function main(): i32 { ` + tc.setup + ` if (show(` + tc.value + `) == "` + tc.want + `") { return 0; } return 1; }`
			if got := interpExit(t, oracle, source); got != 0 {
				t.Fatalf("interpreter exit %d, want zero", got)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOf(t, source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d, want zero\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}
