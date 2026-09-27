package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrTupProducerReclaimWasmIR is the wasm port of
// TestSelfHostArrTupProducerReclaimIRX86_64. The element walk lowers through
// backend-common IR ops (block / loop / arr_len / arr_get / rc_dec), so wasm needs
// no dedicated helper. Case table shared with the x86-64 leg.
func TestSelfHostArrTupProducerReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrtupProducerReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = call-bound array-of-boxes leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
