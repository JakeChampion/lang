package e2ecompiler

import (
	"testing"
)

// TestSelfHostChainedFnArgCallWasmIR is the wasm port of
// TestSelfHostChainedFnArgCallIRX86_64 (#4767): the chained fn-arg-call
// miscompile lived in the shared lift pass (irlower.fern's
// lift_inline_closures_expr), so the wasm IR path trapped on the same
// shapes (pre-fix: wasmtime unreachable/abort instead of SIGSEGV). The
// case table is shared with the x86-64 leg.
func TestSelfHostChainedFnArgCallWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range chainedFnArgCallCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (134 = trap, the #4767 shape)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
