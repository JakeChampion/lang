package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptAarrReclaimIRArm64 is the arm64 port of
// TestSelfHostOptAarrReclaimIRX86_64: the "OPTAARR:" crediting lives in shared
// irlower.fern and the arm64 __fn___fern_optarrarr_free body mirrors the
// x86-64 one (uniqueness-gated payload dec + rc-guarded box dec + buffer
// free). Case table shared with the x86-64 leg.
func TestSelfHostOptAarrReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optAarrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = structure leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
