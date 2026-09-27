package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrStructReclaimWasmIR is the wasm port of
// TestSelfHostArrStructReclaimIRX86_64: the ARRSTRUCT class lives in shared irlower.fern;
// on wasm __fern_rc_dec maps to $__fern_arr_dec (wasm_helper_symbol), op_arr_get reads the
// 4-byte pointer element slots (a struct-box element is a pointer, same width as a scalar),
// and emit_struct_field_drops emits $__struct_drop_<P> (backend-complete), so the
// element-walk deep-free resolves without any dedicated runtime helper. Case table shared
// with the x86-64 leg.
func TestSelfHostArrStructReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-structs leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
