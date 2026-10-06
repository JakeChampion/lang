package e2ecompiler

import (
	"testing"
)

// TestSelfHostTupStructFieldReclaimIRArm64 is the arm64 port of
// TestSelfHostTupStructFieldReclaimIRX86_64: the field-read lowering
// (expr_tuple_elem_tag's ExprIndex arm) and the struct-aware ARRTUP / OPTTUP escape
// checkers live in shared lowering and lower through op_tuple_get / op_struct_get /
// __struct_drop_<P> / __fern_rc_dec, all backend-complete. Case table shared with the
// x86-64 leg.
func TestSelfHostTupStructFieldReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupStructFieldReclaimCases {
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
