package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostModuleQualifiedVariant(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostModuleQualifiedVariant(t, []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"})
}

func TestSelfHostArm64DarwinModuleQualifiedVariant(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostModuleQualifiedVariant(t, []string{"arm64-darwin"})
}

func checkSelfHostModuleQualifiedVariant(t *testing.T, targets []string) {
	t.Helper()
	dir := t.TempDir()
	driver := selfHostCLIForHost(t).cli
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ModuleQualifiedVariantProbe()), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "probe")
			args := []string{"-target", target, "-o", bin, src, stdlib}
			var cmd *exec.Cmd
			switch target {
			case "wasm32-wasi":
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("wasmtime not on PATH")
				}
				args = append([]string{"-emit", "asm"}, args...)
				cmd = exec.Command("wasmtime", "run", bin)
			case "arm64-darwin":
				cmd = exec.Command(bin)
			case "arm64-linux":
				_, q := arm64Tooling(t)
				cmd = runArm64Bin(q, bin)
			default:
				_, q := x86_64Tooling(t)
				cmd = runX86_64Bin(q, bin)
			}
			compile := exec.Command(driver, args...)
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_REPORT=1")
			report, err := compile.CombinedOutput()
			if err != nil {
				t.Fatalf("compile: %v\n%s", err, report)
			}
			e2eharness.RequireCompleteSemanticLowering(t, report)
			out, err := cmd.CombinedOutput()
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
				t.Fatalf("run: %v, want exit 42; first failing check: %s", err, out)
			}
		})
	}
}
