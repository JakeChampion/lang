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

// tcp_listen_with and tcp_socket_ctl, then the datagram sockets (#9853),
// on every backend, raw and through std/net's typed faces: one probe each
// over loopback, exit 42 iff every check holds.
var socketProbes = []struct {
	name string
	src  func() string
}{
	{"raw", e2eharness.SocketCtlProbe},
	{"std_net", e2eharness.NetSocketOptsProbe},
	{"udp", e2eharness.UdpSocketProbe},
	{"std_net_udp", e2eharness.NetUdpProbe},
	{"connect", e2eharness.ConnectProbe},
	{"std_net_connect", e2eharness.NetConnectProbe},
}

func TestSocketCtlInterp(t *testing.T) {
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := runInterpExitCode(t, p.src()); got != 42 {
				t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestSocketCtlX86_64(t *testing.T) {
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := compileAndRunX86_64(t, p.src()); got != 42 {
				t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestSocketCtlArm64(t *testing.T) {
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := compileAndRunArm64(t, p.src()); got != 42 {
				t.Fatalf("arm64 got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

// The Darwin leg runs the probes natively on Apple Silicon: the Darwin
// socket leg of #9853, which macos.yml selects by this name.
func TestArm64DarwinSocketCtl(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	for _, p := range socketProbes {
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
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
				t.Fatalf("arm64-darwin: %v, want exit 42; first failing check: %s", err, out)
			}
		})
	}
}

// The wasm leg runs the component under wasmtime with the network
// inherited, which the plain wasm runner does not grant: the probe binds
// and dials loopback. A wasi:cli/run component reports only 0 or 1, so the
// verdict is the probe's stdout: "ok", or the first failing check.
func TestSocketCtlWasm(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not on PATH", tool)
		}
	}
	fern := buildFernCLI(t)
	for _, p := range socketProbes {
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
			if got := strings.TrimSpace(string(out)); got != "ok" {
				t.Fatalf("wasm: want \"ok\" on stdout; first failing check: %s", got)
			}
		})
	}
}
