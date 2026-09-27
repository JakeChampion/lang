package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupStructFieldReclaimWasmIR is the wasm port of
// TestSelfHostTupStructFieldReclaimIRX86_64: the field-read lowering and the struct-aware
// ARRTUP / OPTTUP escape checkers live in shared irlower.fern; on wasm __fern_rc_dec maps
// to $__fern_arr_dec and emit_struct_field_drops emits $__struct_drop_<P> (backend-
// complete), so the per-element struct-field deep-drop resolves without any dedicated
// runtime helper. Case table shared with the x86-64 leg.
func TestSelfHostTupStructFieldReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupStructFieldReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
