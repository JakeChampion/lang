package e2ecompiler

import (
	"testing"
)

// TestSelfHostOptAarrReclaimWasmIR is the wasm port of
// TestSelfHostOptAarrReclaimIRX86_64: the "OPTAARR:" crediting lives in shared
// lowering; the wasm release is the dedicated $__fern_optarrarr_free WAT
// body (wasm.optarrarr_free_func — per element, a uniquely-owned [tag@0,
// payload@4] option box decs its Some payload then itself, then the outer
// buffer), emitted only when the module lowers the call
// (module_calls_optarrarr_free). Case table shared with the x86-64 leg.
func TestSelfHostOptAarrReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optAarrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = structure leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
