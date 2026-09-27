package e2eselfhost

import (
	"testing"
)

// TestSelfHostLambdaStructFieldWasmIR is the wasm sibling of
// TestSelfHostLambdaStructFieldX86IR: the struct-field lambda hoisting lives in
// the target-independent lift_expr_walk, so the wasm IR backend gets it for
// free. Each case asserts the oracle exit code from the IR-emitted module
// (<= 125 for WASI proc_exit).
func TestSelfHostLambdaStructFieldWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range lambdaStructFieldIRCases {
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
