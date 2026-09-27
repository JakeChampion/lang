package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptArrArrReclaimWasmIR is the wasm port of
// TestSelfHostOptArrArrReclaimIRX86_64: the OPTARRARR class lives in shared irlower.fern;
// on wasm the option box is [tag@0, payload@4], __fern_rc_dec maps to $__fern_arr_dec, and
// $__fern_arrarr_free (wasm arrarr free helper) frees the payload whole, so the inline
// tag-check + arr-of-arr free resolves without any dedicated new runtime helper. Case table
// shared with the x86-64 leg.
func TestSelfHostOptArrArrReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optArrArrReclaimCases {
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
