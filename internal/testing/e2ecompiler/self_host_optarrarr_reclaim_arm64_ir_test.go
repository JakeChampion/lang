package e2ecompiler

import (
	"testing"
)

// TestSelfHostOptArrArrReclaimIRArm64 is the arm64 port of
// TestSelfHostOptArrArrReclaimIRX86_64: the OPTARRARR class (admission + inline
// tag-check/__fern_arrarr_free/box-free + the arr-of-arr escape checker) lives in shared
// lowering and lowers through op_opt_tag / op_opt_payload / __fern_arrarr_free /
// __fern_rc_dec, all backend-complete. Case table shared with the x86-64 leg.
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
