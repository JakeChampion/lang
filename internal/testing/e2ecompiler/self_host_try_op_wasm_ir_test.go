package e2ecompiler

import (
	"testing"
)

func TestSelfHostTryOpWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tryOpIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
