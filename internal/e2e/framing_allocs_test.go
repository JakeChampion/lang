package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// #9853's framing-path allocation gate through the Go compiler. The
// self-host twin is TestSelfHostFramingAllocs, whose pins are the ones
// docs/NET-P0-MESSAGE-LAYER-PLAN.md takes to zero; these move with them
// where the stdlib change reaches both compilers.
func TestFramingAllocs(t *testing.T) {
	compiler := buildFernCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.FramingAllocsSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		want   e2eharness.FramingAllocs
	}{
		{"x86-64-linux", e2eharness.FramingAllocs{Parse: 68, Serialize: 13}},
		{"arm64-linux", e2eharness.FramingAllocs{Parse: 82, Serialize: 15}},
		{"wasm32-wasi", e2eharness.FramingAllocs{Parse: 74, Serialize: 13}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "probe")
			if tc.target == "wasm32-wasi" {
				bin += ".wasm"
			}
			if out, err := exec.Command(compiler, "-target", tc.target, "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("build for %s: %v\n%s", tc.target, err, out)
			}
			var cmd *exec.Cmd
			if tc.target == "wasm32-wasi" {
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", bin)
			} else {
				cmd = nativeServerRunner(t, tc.target)(bin)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("framing probe on %s: %v\n%s", tc.target, err, stderr.String())
			}
			e2eharness.CheckFramingAllocs(t, stderr.String(), tc.want)
		})
	}
}
