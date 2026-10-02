package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupleRetIntermediateWasmIR is the wasm port of
// TestSelfHostTupleRetIntermediateIRX86_64: the tuple-fresh-ret registry and
// "TUP:" crediting live in shared lowering. Case table
// shared with the x86-64 leg.
func TestSelfHostTupleRetIntermediateWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleRetIntermediateCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = box leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
