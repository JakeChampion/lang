package e2ecompiler

import (
	"testing"
)

func TestSelfHostAppendBorrowedParamWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range selfHostAppendBorrowedCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
