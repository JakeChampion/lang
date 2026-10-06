package e2ecompiler

import (
	"testing"
)

// TestSelfHostArrTupReclaimWasmIR is the wasm port of
// TestSelfHostArrTupReclaimIRX86_64: the ARRTUP class lives in shared lowering;
// on wasm __fern_rc_dec maps to $__fern_arr_dec (wasm_helper_symbol) and op_arr_get
// reads the 4-byte pointer element slots (a tuple-box element is a pointer, same
// width as a scalar, so the counted arr_get walk resolves correctly), so the
// element-walk deep-free resolves without any dedicated runtime helper. Case table
// shared with the x86-64 leg.
func TestSelfHostArrTupReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrtupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
