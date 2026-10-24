package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostReactorFloor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostReactorFloor(t, []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"})
}

func TestSelfHostArm64DarwinReactorFloor(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostReactorFloor(t, []string{"arm64-darwin"})
}

func TestSelfHostReactorSignal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostReactorSignal(t, []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"})
}

func TestSelfHostArm64DarwinReactorSignal(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostReactorSignal(t, []string{"arm64-darwin"})
}

func TestSelfHostArm64DarwinReactorCompiles(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for name, src := range map[string]string{"floor": e2eharness.ReactorProbe(), "signal": e2eharness.ReactorSignalProbe("native")} {
		path := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(cli.emit(t, path, "arm64-darwin")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func checkSelfHostReactorSignal(t *testing.T, targets []string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		mode := "native"
		if target == "wasm32-wasi" {
			mode = "wasm"
		}
		t.Run(target, func(t *testing.T) {
			checkSelfHostSocketProbe(t, driver, stdlib, target, e2eharness.ReactorSignalProbe(mode))
		})
	}
}

func checkSelfHostReactorFloor(t *testing.T, targets []string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			checkSelfHostSocketProbe(t, driver, stdlib, target, e2eharness.ReactorProbe())
		})
	}
}
