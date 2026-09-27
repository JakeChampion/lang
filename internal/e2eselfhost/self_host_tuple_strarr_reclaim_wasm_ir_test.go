package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupleStrArrReclaimWasmIR is the wasm port of
// TestSelfHostTupleStrArrReclaimIRX86_64. On wasm __fern_str_arr_free routes to
// $__fern_arr_dec_ptr (wasm_helper_symbol) — wasm strings are single inline
// rc-headered blocks, so the per-element pointer dec IS the string free. Case
// table shared with the x86-64 leg.
func TestSelfHostTupleStrArrReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleStrArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = string[] element positions leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
