package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The reactor floor (#9853) on every leg of the Go compiler: one probe,
// exit 42 iff every check holds. The wasm leg reads the verdict from
// stdout, as the socket tests do.

func TestReactorFloorInterp(t *testing.T) {
	if out, got := runInterpExitCode(t, e2eharness.ReactorProbe()); got != 42 {
		t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
	}
}

func TestReactorFloorX86_64(t *testing.T) {
	if out, got := compileAndRunX86_64(t, e2eharness.ReactorProbe()); got != 42 {
		t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
	}
}

func TestReactorFloorArm64(t *testing.T) {
	if out, got := compileAndRunArm64(t, e2eharness.ReactorProbe()); got != 42 {
		t.Fatalf("arm64 got %d, want 42; first failing check: %s", got, out)
	}
}

// The Darwin leg: kqueue behind the same floor.
func TestArm64DarwinReactorFloor(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ReactorProbe()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
		t.Fatalf("arm64-darwin: %v, want exit 42; first failing check: %s", err, out)
	}
}

func TestReactorFloorWasm(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not on PATH", tool)
		}
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ReactorProbe()), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "probe.component.wasm")
	if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", component, src).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run", "-S", "inherit-network", component).CombinedOutput()
	if _, exited := err.(*exec.ExitError); err != nil && !exited {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "ok" {
		t.Fatalf("wasm: want \"ok\" on stdout; first failing check: %s", got)
	}
}
