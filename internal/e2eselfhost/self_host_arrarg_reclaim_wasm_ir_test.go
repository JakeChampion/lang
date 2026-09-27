package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArgReclaimWasmIR is the wasm port of
// TestSelfHostArrArgReclaimIRX86_64: the call-arg array-temp stash + post-call
// __fern_rc_dec and the consumed-param borrow-verdict fix live in shared
// irlower.fern; on wasm the release maps to $__fern_arr_dec (wasm_helper_symbol),
// the size-class freelist release the exit sweep already uses. Case table
// shared with the x86-64 leg.
func TestSelfHostArrArgReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArgReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = arg temp leaked; 99 = over-release/underflow; 94-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
