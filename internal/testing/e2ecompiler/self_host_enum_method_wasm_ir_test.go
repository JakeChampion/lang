package e2ecompiler

import (
	"testing"
)

// TestSelfHostEnumMethodWasmIR is the wasm sibling of
// TestSelfHostEnumMethodX86IR: the enum-receiver typing (expr_enum_type +
// the unannotated-enum-binding recording) lives in the target-independent
// lowering, so the wasm IR backend gets it for free. Each case asserts the
// oracle exit code from the IR-emitted module (<= 125 for WASI proc_exit).
func TestSelfHostEnumMethodWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range enumMethodIRCases {
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
