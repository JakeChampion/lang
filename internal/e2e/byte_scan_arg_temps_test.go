package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestByteScanArgTempsAreReleased compiles ByteScanArgTempsProgram, which
// hands every byte-scan builtin a fresh temporary, with the native compiler
// on each native backend, and requires a balanced leak census and the right
// sum. Before the fix each call stranded its temporary.
func TestByteScanArgTempsAreReleased(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "scan.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ByteScanArgTempsProgram), 0o644); err != nil {
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
			bin := filepath.Join(t.TempDir(), "scan")
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
				t.Errorf("byte-scan argument temporaries leak: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			}
		})
	}
}
