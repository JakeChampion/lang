package e2ecompiler

import (
	"testing"
)

// TestSelfHostArrStructReclaimIRArm64 is the arm64 leg of
// TestSelfHostArrStructReclaimIRX86_64, sharing its case table: an
// array-of-structs local is released with its elements' fields, without
// over-releasing an element that escapes.
func TestSelfHostArrStructReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = array-of-structs leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
