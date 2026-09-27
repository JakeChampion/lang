package e2eselfhost

import (
	"testing"
)

// TestSelfHostStrTupleReclaimWasmIR is the wasm port of
// TestSelfHostStrTupleReclaimIRX86_64: the string-element tuple admission and
// deep-drop live in shared irlower.fern; on wasm __fern_str_free maps to
// $__fern_arr_dec (wasm_helper_symbol — wasm strings are arr-boxed, so the
// rc-guarded box dec is the whole free) and op_tuple_get reads the 4-byte
// pointer slots. Case table shared with the x86-64 leg.
func TestSelfHostStrTupleReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range strTupleReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = string-tuple leaked; 99 = over-release/underflow; 97/96 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
