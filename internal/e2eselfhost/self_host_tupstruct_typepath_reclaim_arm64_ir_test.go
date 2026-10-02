package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupStructTypePathReclaimIRArm64 is the arm64 port of
// TestSelfHostTupStructTypePathReclaimIRX86_64: the TYPE-driven struct-element drop
// (emit_tuple_type_child_drops' struct arm + the structs-threaded admission predicates)
// lives in shared lowering and lowers through op_tuple_get / __struct_drop_<P> /
// __fern_rc_dec, all backend-complete. Case table shared with the x86-64 leg.
func TestSelfHostTupStructTypePathReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupStructTypePathCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
