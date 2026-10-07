package e2ecompiler

import (
	"testing"
)

// TestSelfHostArrTupReclaimIRArm64 is the arm64 leg of
// TestSelfHostArrTupReclaimIRX86_64: the array-of-tuples element reclaim is
// decided in the target-independent lowering, so arm64 must give the same
// answers. Case table shared with the x86-64 leg.
func TestSelfHostArrTupReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrtupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
