package e2ecompiler

import (
	"testing"
)

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
