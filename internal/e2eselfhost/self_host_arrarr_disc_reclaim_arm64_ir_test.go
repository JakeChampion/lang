package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArrDiscReclaimIRArm64 is the arm64 port of
// TestSelfHostArrArrDiscReclaimIRX86_64: the discardable_scalar_arrarr_lit
// admission + __fern_arrarr_free routing live in shared irlower.fern; the arm64
// leg differs only in the release-helper body (__fn___fern_arrarr_free, need-
// seeded from the op-scan). Case table shared with the x86-64 leg.
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
