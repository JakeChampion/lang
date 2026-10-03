package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupDiscReclaimWasmIR is the wasm port of
// TestSelfHostTupDiscReclaimIRX86_64: the discarded-tuple deep-drop lives in
// shared lowering; on wasm __fern_rc_dec maps to $__fern_arr_dec
// (wasm_helper_symbol) and op_tuple_get reads the 4-byte element slots. The
// i64-element decline in tuple_ret_arrfree_flags exists exactly for this
// backend's slot width. Case table shared with the x86-64 leg.
func TestSelfHostTupDiscReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = tuple temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
