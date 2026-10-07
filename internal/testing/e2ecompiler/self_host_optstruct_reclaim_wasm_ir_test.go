package e2ecompiler

import (
	"testing"
)

// TestSelfHostOptStructReclaimWasmIR is the wasm port of
// TestSelfHostOptStructReclaimIRX86_64: the option release is shared lowering, and
// on wasm the option box is [tag@0, payload@4], so the inline tag-check and the
// struct-field deep drop need no dedicated runtime helper. Case table shared with
// the x86-64 leg.
func TestSelfHostOptStructReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range optStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
