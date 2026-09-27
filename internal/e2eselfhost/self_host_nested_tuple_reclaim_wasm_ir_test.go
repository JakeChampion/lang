package e2eselfhost

import (
	"testing"
)

// TestSelfHostNestedTupleReclaimWasmIR is the wasm port of
// TestSelfHostNestedTupleReclaimIRX86_64: the recursive tuple deep-drop lives in
// shared irlower.fern; on wasm __fern_rc_dec maps to $__fern_arr_dec
// (wasm_helper_symbol) and op_tuple_get reads the 4-byte pointer slots (a nested
// tuple element is a pointer, same width as a scalar, so the chained tuple_get
// resolves correctly). Case table shared with the x86-64 leg.
func TestSelfHostNestedTupleReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range nestedTupleReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = nested tuple leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
