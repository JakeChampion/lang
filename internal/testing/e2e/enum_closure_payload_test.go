package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestEnumClosurePayloadIsReleased compiles EnumClosurePayloadProgram with
// the Go compiler for x86-64, arm64 and wasm and requires a balanced leak
// census. Before the fix the generated enum drop skipped a closure payload,
// so every enum-held closure stranded its pair and env (#11349); the
// program's overwrite-inside-the-arm case segfaults on x86-64 unless the
// match holds the box its arm reassigns.
func TestEnumClosurePayloadIsReleased(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "enumclo.fern")
	if err := os.WriteFile(src, []byte(e2eharness.EnumClosurePayloadProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var runner []string
			switch target {
			case "x86-64-linux":
				_, runner = x86_64Tooling(t)
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("requires wasmtime")
				}
				runner = []string{"wasmtime", "run"}
			}
			bin := filepath.Join(t.TempDir(), "enumclo")
			compile := exec.Command(fern, "-target", target, "-o", bin, src)
			compile.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append(append([]string{}, runner...), bin)
			out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			allocs, frees, live := leakSummaryIn(t, string(out))
			if allocs == 0 || allocs != frees || live != 0 {
				t.Errorf("enum-held closures leak: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
		})
	}
}
