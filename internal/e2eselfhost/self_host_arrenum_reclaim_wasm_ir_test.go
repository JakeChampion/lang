package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrEnumReclaimWasmIR is the wasm port of
// TestSelfHostArrEnumReclaimIRX86_64. The ARRENUM element walk is shared IR, and on wasm
// __fern_rc_dec maps to $__fern_arr_dec (wasm_helper_symbol) while emit_enum_variant_drops
// lowers to the same runtime variant_is dispatch it emits for a scalar enum local — so the
// class resolves with no dedicated runtime helper on this backend either. Case table
// shared with the x86-64 leg.
func TestSelfHostArrEnumReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrEnumReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = enum-array elements leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
