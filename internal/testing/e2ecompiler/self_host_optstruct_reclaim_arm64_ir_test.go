package e2ecompiler

import (
	"testing"
)

// TestSelfHostOptStructReclaimIRArm64 is the arm64 leg of
// TestSelfHostOptStructReclaimIRX86_64, over the same case table: the
// `Option[<struct-with-rc-field>]` reclaim must hold on arm64 too.
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
