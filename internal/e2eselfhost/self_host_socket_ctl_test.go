package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host twin of internal/e2e's TestSocketCtl*: tcp_listen_with,
// tcp_socket_ctl and the datagram sockets compiled by the production self-host driver on every
// target it serves here, with complete semantic lowering required. The
// native legs report exit 42; the wasm leg is a wasi:cli/run component,
// which reports only 0 or 1, so there the verdict is the probe's stdout.
func TestSelfHostSocketCtl(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostSocketCtl(t, []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"})
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
		{"std_net", e2eharness.NetSocketOptsProbe()},
		{"udp", e2eharness.UdpSocketProbe()},
		{"std_net_udp", e2eharness.NetUdpProbe()},
	}
	stdlib, err := filepath.Abs("../stdlib")
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
		for _, tool := range []string{"wasm-tools", "wasmtime"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Fatalf("%s not on PATH", tool)
			}
		}
		args = append([]string{"-emit", "asm"}, args...)
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
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
	report, err := compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	e2eharness.RequireCompleteSemanticLowering(t, report)
	if target == "wasm32-wasi" {
		got, err := composeSelfHostWat(t, out).CombinedOutput()
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

// composeSelfHostWat turns the self-host driver's WAT into a wasi:cli/run
// component the way the wasm-IR socket twins do (embed the vendored WIT
// world, adapt preview 1) and returns the networked wasmtime command for
// it. The preview-1 adapter comes from FERN_WASI_ADAPTER.
func composeSelfHostWat(t *testing.T, watPath string) *exec.Cmd {
	t.Helper()
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Fatal("FERN_WASI_ADAPTER unset")
	}
	witDir, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatal(err)
	}
	core := watPath + ".core.wasm"
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", core).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	embed := watPath + ".embed.wasm"
	if out, err := exec.Command("wasm-tools", "component", "embed", witDir, "-w", "fern", core, "-o", embed).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
	}
	comp := watPath + ".component.wasm"
	if out, err := exec.Command("wasm-tools", "component", "new", embed, "--adapt", "wasi_snapshot_preview1="+adapter, "-o", comp).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools component new: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "validate", comp).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	return exec.Command("wasmtime", "run", "-S", "inherit-network", comp)
}
