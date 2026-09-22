package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestNativeSocketSendSuppressesSIGPIPE(t *testing.T) {
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
			checkNativeSocketSend(t, compiler, tc.target, tc.backend, run)
		})
	}
}

func TestArm64DarwinSocketSendSuppressesSIGPIPE(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkNativeSocketSend(t, buildFernCLI(t), "arm64-darwin", "", func(p string) *exec.Cmd { return exec.Command(p) })
}

func checkNativeSocketSend(t *testing.T, compiler, target, backend string, run func(string) *exec.Cmd) {
	t.Helper()
	for _, payload := range []string{"", "x", "abcdefgh", strings.Repeat("x", 4097)} {
		for _, shut := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes%d/shutdown%t", len(payload), shut), func(t *testing.T) {
				dir := t.TempDir()
				src, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main")
				if err := os.WriteFile(src, []byte(e2eharness.NativeSocketSendProbe(payload, shut)), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-target", target, "-o", bin, src}
				if backend != "" {
					args = append([]string{"-backend", backend}, args...)
				}
				if out, err := exec.Command(compiler, args...).CombinedOutput(); err != nil {
					t.Fatalf("build: %v\n%s", err, out)
				}
				e2eharness.CheckNativeSocketSend(t, run(bin), payload, shut)
			})
		}
	}
}
