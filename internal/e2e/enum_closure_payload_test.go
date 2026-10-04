package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestEnumClosurePayloadIsReleased compiles EnumClosurePayloadProgram with
// the native compiler on each native backend and requires a balanced leak
// census. Before the fix the generated enum drop skipped a closure payload,
// so every enum-held closure stranded its pair and env (#11349).
func TestEnumClosurePayloadIsReleased(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "enumclo.fern")
	if err := os.WriteFile(src, []byte(e2eharness.EnumClosurePayloadProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "x86-64-linux-ssa", "arm64-linux", "arm64-linux-ssa", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var runner []string
			actual := strings.TrimSuffix(target, "-ssa")
			switch actual {
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
			args := []string{"-target", actual, "-o", bin, src}
			if strings.HasSuffix(target, "-ssa") {
				args = append([]string{"-backend", "ssa"}, args...)
			}
			compile := exec.Command(fern, args...)
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
