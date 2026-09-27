package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupDiscReclaimIRArm64 is the arm64 port of
// TestSelfHostTupDiscReclaimIRX86_64: the discarded-tuple deep-drop (literal +
// TUPRET call arms) lives in shared irlower.fern and lowers through
// op_tuple_get + __fern_rc_dec, both already backend-complete. Case table
// shared with the x86-64 leg.
func TestSelfHostTupDiscReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = tuple temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
