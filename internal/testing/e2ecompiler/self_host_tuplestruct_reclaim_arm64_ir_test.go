package e2ecompiler

import (
	"testing"
)

// TestSelfHostTupleStructReclaimIRArm64 is the arm64 port of
// TestSelfHostTupleStructReclaimIRX86_64: the tuple-with-struct-element TUPRC path
// (struct-literal admission + emit_tuple_child_drops' struct arm + the
// rctuple_payload_escapes gate) lives in shared lowering and lowers through
// op_tuple_get / __struct_drop_<P> / __fern_rc_dec, all backend-complete. Case table
// shared with the x86-64 leg.
func TestSelfHostTupleStructReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = tuple/struct leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
