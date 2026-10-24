package e2ecompiler

import (
	"testing"
)

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
