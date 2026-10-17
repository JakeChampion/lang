package e2ecompiler

import (
	"testing"
)

// TestSelfHostOptArrArrReclaimIRArm64 runs the shared optional nested-array reclaim
// cases on ARM64, exercising the typed release pipeline.
func TestSelfHostOptArrArrReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optArrArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
