package e2ecompiler

import (
	"testing"
)

// TestSelfHostStrTupleReclaimIRArm64 is the arm64 port of
// TestSelfHostStrTupleReclaimIRX86_64: the string-element tuple release is
// shared lowering, through op_tuple_get and the rc-aware __fern_str_free. Case
// table shared with the x86-64 leg.
func TestSelfHostStrTupleReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range strTupleReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = string-tuple leaked; 99 = over-release/underflow; 97/96 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
