package e2ecompiler

import (
	"testing"
)

// TestSelfHostStrOnlyStructExitDropIRArm64 is the arm64 port of
// TestSelfHostStrOnlyStructExitDropIRX86_64. Case table shared with the x86-64
// leg.
func TestSelfHostStrOnlyStructExitDropIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range strOnlyStructExitDropCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = string field leaked; 99 = over-release/underflow; 97 = value corrupted; 1/2 = live value lost)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
