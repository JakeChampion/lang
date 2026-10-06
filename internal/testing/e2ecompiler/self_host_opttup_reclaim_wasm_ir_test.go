package e2ecompiler

import (
	"testing"
)

// TestSelfHostOptTupReclaimWasmIR is the wasm port of
// TestSelfHostOptTupReclaimIRX86_64: the OPTTUP class lives in shared
// lowering; on wasm the option box is [tag@0, payload@4] and __fern_rc_dec
// maps to $__fern_arr_dec (wasm_helper_symbol), so the inline tag-check +
// type-driven tuple deep-drop resolves without any dedicated runtime helper. Case
// table shared with the x86-64 leg.
func TestSelfHostOptTupReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optTupReclaimCases {
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
