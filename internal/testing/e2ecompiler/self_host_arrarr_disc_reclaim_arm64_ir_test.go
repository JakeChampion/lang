package e2ecompiler

import (
	"testing"
)

// TestSelfHostArrArrDiscReclaimIRArm64 runs the shared discarded nested-array reclaim
// cases on ARM64, exercising the typed release pipeline.
func TestSelfHostArrArrDiscReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = arrarr temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
