package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrTupReclaimIRArm64 is the arm64 port of
// TestSelfHostArrTupReclaimIRX86_64: the ARRTUP class (admission + the counted
// element-walk deep-free + the element-payload escape checker) lives in shared
// lowering and lowers through backend-common IR ops (block / loop / arr_len /
// arr_get / __fern_rc_dec + emit_tuple_type_child_drops), all backend-complete.
// Case table shared with the x86-64 leg.
func TestSelfHostArrTupReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrtupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
