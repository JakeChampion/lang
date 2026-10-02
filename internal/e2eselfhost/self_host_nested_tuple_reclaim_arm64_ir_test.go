package e2eselfhost

import (
	"testing"
)

// TestSelfHostNestedTupleReclaimIRArm64 is the arm64 port of
// TestSelfHostNestedTupleReclaimIRX86_64: the recursive tuple deep-drop
// (emit_tuple_child_drops) and the widened TUPRC: admission live in shared
// lowering and lower through op_tuple_get + __fern_rc_dec, both backend-
// complete. Case table shared with the x86-64 leg.
func TestSelfHostNestedTupleReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range nestedTupleReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = nested tuple leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
