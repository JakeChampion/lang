package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostLaneRoundBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "tests", "lane_round.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, string(src), target); code != 0 {
				t.Fatalf("SIMD rounding differs from the scalar reference: exit %d\n%s", code, stderr)
			}
		})
	}
}
