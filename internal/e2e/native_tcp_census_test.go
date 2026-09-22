package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestNativeTCPLifecycleCensus(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, tc := range []struct{ target, backend string }{
		{"x86-64-linux", ""}, {"x86-64-linux", "ssa"}, {"arm64-linux", ""}, {"arm64-linux", "ssa"},
	} {
		t.Run(tc.target+"/"+tc.backend, func(t *testing.T) {
			var run func(string) *exec.Cmd
			if tc.target == "arm64-linux" {
				q := arm64QemuOrEmpty(t)
				run = func(p string) *exec.Cmd { return runArm64Bin(q, p) }
			} else {
				q := x86QemuOrEmpty(t)
				run = func(p string) *exec.Cmd {
					if q != "" {
						return exec.Command(q, p)
					}
					return exec.Command(p)
				}
			}
			checkNativeTCPCensus(t, compiler, tc.target, tc.backend, run)
		})
	}
}

func TestArm64DarwinTCPLifecycleCensus(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkNativeTCPCensus(t, buildFernCLI(t), "arm64-darwin", "", func(p string) *exec.Cmd { return exec.Command(p) })
}

func checkNativeTCPCensus(t *testing.T, compiler, target, backend string, run func(string) *exec.Cmd) {
	t.Helper()
	for _, tc := range e2eharness.NativeTCPCensusCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main")
			if err := os.WriteFile(src, []byte(tc.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"-target", target, "-o", bin, src}
			if backend != "" {
				args = append([]string{"-backend", backend}, args...)
			}
			cmd := exec.Command(compiler, args...)
			cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			e2eharness.CheckNativeTCPCensus(t, run(bin))
		})
	}
}
