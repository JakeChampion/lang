package e2eselfhost

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host twin of internal/e2e's TestSocketV6*: the IPv6 probes
// (#9853) compiled by the production self-host driver on every target it
// serves here. A host without IPv6 is one where Go cannot listen on ::1;
// there the probe must answer "nov6" and the test skips.
func TestSelfHostSocketV6(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostSocketV6(t, []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"})
}

func TestSelfHostArm64DarwinSocketV6(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostSocketV6(t, []string{"arm64-darwin"})
}

func selfHostHostHasIPv6() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func checkSelfHostSocketV6(t *testing.T, targets []string) {
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
		{"raw", e2eharness.SocketV6Probe()},
		{"std_net", e2eharness.NetV6Probe()},
	}
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		for _, p := range probes {
			t.Run(target+"/"+p.name, func(t *testing.T) {
				checkSelfHostSocketV6Probe(t, driver, stdlib, target, p.src)
			})
		}
	}
}

// checkSelfHostSocketV6Probe is checkSelfHostSocketProbe with the "nov6"
// answer accepted where the host has no IPv6.
func checkSelfHostSocketV6Probe(t *testing.T, driver, stdlib, target, probe string) {
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
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_REPORT=1")
	report, err := compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	e2eharness.RequireCompleteSemanticLowering(t, report)
	has := selfHostHostHasIPv6()
	if target == "wasm32-wasi" {
		got, err := composeSelfHostWat(t, out).CombinedOutput()
		if _, exited := err.(*exec.ExitError); err != nil && !exited {
			t.Fatalf("wasmtime run: %v\n%s", err, got)
		}
		s := strings.TrimSpace(string(got))
		if has {
			if s != "ok" {
				t.Fatalf("run: want \"ok\" on stdout; first failing check: %s", s)
			}
			return
		}
		if s != "nov6" {
			t.Fatalf("run: Go cannot listen on ::1 here, so the probe must answer nov6; got: %s", s)
		}
		t.Skip("this host has no IPv6: Go cannot listen on ::1")
	}
	got, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if has {
		if code != 42 {
			t.Fatalf("run: exit %d, want 42; first failing check: %s", code, got)
		}
		return
	}
	if code != 43 || !strings.Contains(string(got), "nov6") {
		t.Fatalf("run: Go cannot listen on ::1 here, so the probe must answer nov6 (exit 43); got %d: %s", code, got)
	}
	t.Skip("this host has no IPv6: Go cannot listen on ::1")
}
