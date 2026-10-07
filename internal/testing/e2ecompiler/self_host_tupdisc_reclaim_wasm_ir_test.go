package e2ecompiler

import (
	"testing"
)

// TestSelfHostTupDiscReclaimWasmIR is the wasm leg of
// TestSelfHostTupDiscReclaimIRX86_64: the discarded-tuple deep-drop is decided
// in the target-independent lowering, so wasm must give the same answers. Case
// table shared with the x86-64 leg.
func TestSelfHostTupDiscReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = tuple temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
