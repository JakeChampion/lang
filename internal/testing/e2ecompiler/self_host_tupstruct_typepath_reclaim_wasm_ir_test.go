package e2ecompiler

import (
	"testing"
)

// TestSelfHostTupStructTypePathReclaimWasmIR is the wasm port of
// TestSelfHostTupStructTypePathReclaimIRX86_64: the TYPE-driven struct-element drop
// lives in shared lowering; on wasm __fern_rc_dec maps to $__fern_arr_dec and
// emit_struct_field_drops emits $__struct_drop_<P> (backend-complete), so the
// per-element struct-field deep-drop + box dec resolves without any dedicated runtime
// helper. Case table shared with the x86-64 leg.
func TestSelfHostTupStructTypePathReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupStructTypePathCases {
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
