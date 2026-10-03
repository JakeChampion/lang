package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptStructReclaimWasmIR is the wasm port of
// TestSelfHostOptStructReclaimIRX86_64: the OPTSTRUCT class lives in shared lowering;
// on wasm the option box is [tag@0, payload@4], __fern_rc_dec maps to $__fern_arr_dec
// (wasm_helper_symbol), and emit_struct_field_drops emits $__struct_drop_<P>
// (backend-complete), so the inline tag-check + struct-field deep-drop resolves without any
// dedicated runtime helper. Case table shared with the x86-64 leg.
func TestSelfHostOptStructReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
