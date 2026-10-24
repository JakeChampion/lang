package e2ecompiler

import (
	"testing"
)

func TestSelfHostArrTupReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrtupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
