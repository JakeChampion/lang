package e2ecompiler

import (
	"testing"
)

// TestSelfHostTryOpWasmIR is the wasm sibling of TestSelfHostTryOpX86IR: the
// try-operator (`inner?`) lowers in the target-independent lowering, so the wasm
// IR backend gets it for free (op_opt_tag / op_opt_payload / op_opt_none /
// return — the same ops the match-on-Option path already uses). Each case
// asserts the hardcoded oracle exit code from the IR-emitted module. Exit codes
// are kept <= 125 (the WASI proc_exit constraint).
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
