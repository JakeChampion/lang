package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArgReclaimIRArm64 is the arm64 port of
// TestSelfHostArrArgReclaimIRX86_64: the call-arg array-temp stash + post-call
// __fern_rc_dec and the consumed-param borrow-verdict fix both live in shared
// irlower.fern, so the arm64 leg only differs in the release helper body
// (__fn___fern_arr_dec). Case table shared with the x86-64 leg.
func TestSelfHostArrArgReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArgReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = arg temp leaked; 99 = over-release/underflow; 94-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
