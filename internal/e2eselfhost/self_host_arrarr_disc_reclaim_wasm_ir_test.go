package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArrDiscReclaimWasmIR is the wasm port of
// TestSelfHostArrArrDiscReclaimIRX86_64: the discarded scalar-inner arrarr
// reclaim lives in shared irlower.fern; on wasm __fern_arrarr_free maps to
// $__fern_arr_dec_ptr (wasm_helper_symbol), the pointer-element walk always
// emitted with the heap runtime, so no wasm-specific gating is needed. Case
// table shared with the x86-64 leg.
func TestSelfHostArrArrDiscReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = arrarr temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
