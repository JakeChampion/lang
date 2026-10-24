package e2ecompiler

import (
	"testing"
)

func TestSelfHostArrArrDiscReclaimWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = arrarr temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
