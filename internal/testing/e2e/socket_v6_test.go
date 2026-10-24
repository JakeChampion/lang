package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var socketV6Probes = []struct {
	name string
	src  func() string
}{
	{"raw", e2eharness.SocketV6Probe},
	{"std_net", e2eharness.NetV6Probe},
}

func checkV6Verdict(t *testing.T, leg, out string, got int) {
	t.Helper()
	if e2eharness.HostHasIPv6() {
		if got != 42 {
			t.Fatalf("%s got %d, want 42; first failing check: %s", leg, got, out)
		}
		return
	}
	if got != 43 || !strings.Contains(out, "nov6") {
		t.Fatalf("%s: Go cannot listen on ::1 here, so the probe must answer nov6 (exit 43); got %d: %s", leg, got, out)
	}
	t.Skip("this host has no IPv6: Go cannot listen on ::1")
}

func TestSocketV6Interp(t *testing.T) {
	for _, p := range socketV6Probes {
		t.Run(p.name, func(t *testing.T) {
			out, got := runInterpExitCode(t, p.src())
			checkV6Verdict(t, "interp", out, got)
		})
	}
}

func TestSocketV6X86_64(t *testing.T) {
	for _, p := range socketV6Probes {
		t.Run(p.name, func(t *testing.T) {
			out, got := compileAndRunX86_64(t, p.src())
			checkV6Verdict(t, "x86-64", out, got)
		})
	}
}

func TestSocketV6Arm64(t *testing.T) {
	for _, p := range socketV6Probes {
		t.Run(p.name, func(t *testing.T) {
			out, got := compileAndRunArm64(t, p.src())
			checkV6Verdict(t, "arm64", out, got)
		})
	}
}

func TestArm64DarwinSocketV6(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	for _, p := range socketV6Probes {
		t.Run(p.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			if err := os.WriteFile(src, []byte(p.src()), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "probe")
			if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			got := 0
			if ee, ok := err.(*exec.ExitError); ok {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			checkV6Verdict(t, "arm64-darwin", string(out), got)
		})
	}
}

func TestSocketV6Wasm(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not on PATH", tool)
		}
	}
	fern := buildFernCLI(t)
	for _, p := range socketV6Probes {
		t.Run(p.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			if err := os.WriteFile(src, []byte(p.src()), 0o644); err != nil {
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
			got := strings.TrimSpace(string(out))
			if e2eharness.HostHasIPv6() {
				if got != "ok" {
					t.Fatalf("wasm: want \"ok\" on stdout; first failing check: %s", got)
				}
				return
			}
			if got != "nov6" {
				t.Fatalf("wasm: Go cannot listen on ::1 here, so the probe must answer nov6; got: %s", got)
			}
			t.Skip("this host has no IPv6: Go cannot listen on ::1")
		})
	}
}
