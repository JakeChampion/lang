package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostSocketCtl(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostSocketCtl(t, []string{"x86-64-linux", "arm64-linux"})
}

func TestSelfHostWasmSocketCtl(t *testing.T) {
	checkSelfHostSocketCtl(t, []string{"wasm32-wasi"})
}

func TestSelfHostArm64DarwinSocketCtl(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostSocketCtl(t, []string{"arm64-darwin"})
}

func checkSelfHostSocketCtl(t *testing.T, targets []string) {
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
	probes := []struct {
		name string
		src  string
	}{
		{"raw", e2eharness.SocketCtlProbe()},
		{"send_queue", e2eharness.SendQueueProbe()},
		{"peer_key", e2eharness.PeerKeyProbe()},
		{"std_net", e2eharness.NetSocketOptsProbe()},
		{"udp", e2eharness.UdpSocketProbe()},
		{"udp_reuse", e2eharness.UdpReuseProbe()},
		{"std_net_udp", e2eharness.NetUdpProbe()},
		{"connect", e2eharness.ConnectProbe()},
		{"std_net_connect", e2eharness.NetConnectProbe()},
		{"relisten", e2eharness.RelistenProbe()},
		{"read_reset", e2eharness.NetReadResetProbe()},
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		for _, p := range probes {
			t.Run(target+"/"+p.name, func(t *testing.T) {
				checkSelfHostSocketProbe(t, driver, stdlib, target, p.src)
			})
		}
	}
}

func checkSelfHostSocketProbe(t *testing.T, driver, stdlib, target, probe string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "probe")
	args := []string{"-target", target, "-o", out, src, stdlib}
	var cmd *exec.Cmd
	switch target {
	case "wasm32-wasi":
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Fatal("wasmtime not on PATH")
		}
		cmd = exec.Command("wasmtime", "run", "-S", "inherit-network", out)
	case "arm64-darwin":
		cmd = exec.Command(out)
	case "arm64-linux":
		_, q := arm64Tooling(t)
		cmd = runArm64Bin(q, out)
	default:
		_, q := x86_64Tooling(t)
		cmd = runX86_64Bin(q, out)
	}
	compile := exec.Command(driver, args...)
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_REPORT=1")
	report, err := compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	e2eharness.RequireCompleteSemanticLowering(t, report)
	if target == "wasm32-wasi" {
		got, err := cmd.CombinedOutput()
		if _, exited := err.(*exec.ExitError); err != nil && !exited {
			t.Fatalf("wasmtime run: %v\n%s", err, got)
		}
		if s := strings.TrimSpace(string(got)); s != "ok" {
			t.Fatalf("run: want \"ok\" on stdout; first failing check: %s", s)
		}
		return
	}
	got, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
		t.Fatalf("run: %v, want exit 42; first failing check: %s", err, got)
	}
}

func TestSelfHostUnixSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostUnixSocket(t, []string{"x86-64-linux", "arm64-linux"})
}

func TestSelfHostArm64DarwinUnixSocket(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostUnixSocket(t, []string{"arm64-darwin"})
}

func checkSelfHostUnixSocket(t *testing.T, targets []string) {
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
	probes := []struct {
		name string
		src  string
	}{
		{"raw", e2eharness.UnixSocketProbe()},
		{"std_net", e2eharness.NetUnixProbe()},
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		for _, p := range probes {
			t.Run(target+"/"+p.name, func(t *testing.T) {
				checkSelfHostSocketProbe(t, driver, stdlib, target, p.src)
			})
		}
	}
}
