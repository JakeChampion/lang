package e2ecompiler

import (
	"testing"
)

// TestSelfHostTupStructTypePathReclaimWasmIR is the wasm leg of
// TestSelfHostTupStructTypePathReclaimIRX86_64: the TYPE-driven struct-element
// drop is decided in the target-independent lowering, so wasm must give the
// same answers. Case table shared with the x86-64 leg.
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
