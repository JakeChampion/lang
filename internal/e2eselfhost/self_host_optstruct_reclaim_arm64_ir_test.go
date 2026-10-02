package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptStructReclaimIRArm64 is the arm64 port of
// TestSelfHostOptStructReclaimIRX86_64: the OPTSTRUCT class (admission + inline
// tag-check/struct-field-deep-drop/box-free + the struct-payload escape checker) lives in
// shared lowering and lowers through op_opt_tag / op_opt_payload / emit_struct_field_
// drops (-> __struct_drop_<P>) / __fern_rc_dec, all backend-complete. Case table shared
// with the x86-64 leg.
func TestSelfHostOptStructReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optStructReclaimCases {
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
