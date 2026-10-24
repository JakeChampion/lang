package e2ecompiler

import (
	"testing"
)

func TestSelfHostTupleStrArrReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleStrArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = string[] element positions leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
