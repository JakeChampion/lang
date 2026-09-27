package e2eselfhost

import (
	"testing"
)

// TestSelfHostNestedClosureWasmIR is the wasm sibling of
// TestSelfHostNestedClosureX86IR: the nested-lambda capture analysis and the
// lift worklist live in the target-independent astwalk / irlower, so the wasm IR
// backend gets nested closures for free. Each case asserts the oracle exit code
// from the IR-emitted module (<= 125 for WASI proc_exit).
func TestSelfHostNestedClosureWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range nestedClosureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
