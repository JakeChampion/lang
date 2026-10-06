package e2ecompiler

import (
	"testing"
)

// TestSelfHostArrStructReclaimIRArm64 is the arm64 port of
// TestSelfHostArrStructReclaimIRX86_64: the ARRSTRUCT class (admission + the counted
// element-walk deep-free + the element-field escape checker) lives in shared lowering
// and lowers through backend-common IR ops (block / loop / arr_len / arr_get /
// __fern_rc_dec + emit_struct_field_drops -> __struct_drop_<P>), all backend-complete.
// Case table shared with the x86-64 leg.
func TestSelfHostArrStructReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-structs leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
