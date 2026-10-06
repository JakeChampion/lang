package e2ecompiler

import (
	"testing"
)

// TestSelfHostChainedFnArgCallIRArm64 is the arm64 port of
// TestSelfHostChainedFnArgCallIRX86_64 (#4767): the chained fn-arg-call
// miscompile lived in the shared lift pass (irlower.fern's
// lift_inline_closures_expr), so the arm64 IR path crashed on the same
// shapes. The case table is shared with the x86-64 leg.
func TestSelfHostChainedFnArgCallIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range chainedFnArgCallCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (-1 = signal crash, the #4767 shape)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
