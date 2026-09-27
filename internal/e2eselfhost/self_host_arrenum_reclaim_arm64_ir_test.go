package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrEnumReclaimIRArm64 is the arm64 port of
// TestSelfHostArrEnumReclaimIRX86_64. The ARRENUM class lives entirely in shared
// irlower.fern — the element walk is IR (op_arr_get on the 8-byte pointer element slot
// plus emit_enum_variant_drops' runtime variant dispatch), so it needs no arm64 helper.
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
