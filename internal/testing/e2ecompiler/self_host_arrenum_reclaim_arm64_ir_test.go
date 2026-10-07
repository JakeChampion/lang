package e2ecompiler

import (
	"testing"
)

// TestSelfHostArrEnumReclaimIRArm64 is the arm64 port of
// TestSelfHostArrEnumReclaimIRX86_64: the element walk and its runtime variant
// dispatch are target-independent IR, so arm64 needs no helper of its own.
// Case table shared with the x86-64 leg.
func TestSelfHostArrEnumReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrEnumReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = enum-array elements leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
