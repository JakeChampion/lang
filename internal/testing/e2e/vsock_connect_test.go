package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The host of a virtual machine is context ID 2. No test listens on the
// port, so the kernel answers with an errno, which the Go reference finds
// with the same two system calls.
const (
	vsockTestCID  = 2
	vsockTestPort = 45391
)

// The first line is the raw builtin: the descriptor or -errno. The second
// is std/net: "connected" or the errno of the NetError.
var vsockConnectSource = fmt.Sprintf(`import "std/net";

function main(): i32 {
    let s = vsock_connect(%d, %d);
    print(s.fd.to_string());
    match (net.connect_vsock(%d, %d)) {
        Ok(_) => {
            print("connected");
        },
        Err(e) => {
            print(e.errno().to_string());
        }
    }
    return 0;
}
`, vsockTestCID, vsockTestPort, vsockTestCID, vsockTestPort)

func vsockConnectWant(t *testing.T) string {
	t.Helper()
	errno, ok := e2eharness.VsockConnectErrno(vsockTestCID, vsockTestPort)
	if !ok {
		t.Fatalf("the VSOCK reference runs on Linux only, not %s", runtime.GOOS)
	}
	if errno == 0 {
		return "connected"
	}
	return fmt.Sprintf("-%d\n%d\n", errno, errno)
}

func checkVsockConnect(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	want := vsockConnectWant(t)
	out, err := cmd.CombinedOutput()
	if code := exitCodeOf(err); code != 0 {
		t.Fatalf("exit = %d, output %q", code, out)
	}
	if want == "connected" {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != 2 || strings.HasPrefix(lines[0], "-") || lines[1] != "connected" {
			t.Fatalf("the reference connected, the program printed %q", out)
		}
		return
	}
	if string(out) != want {
		t.Fatalf("output %q, want %q: the errno of the Go reference", out, want)
	}
}

func TestX86_64VsockConnect(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, vsockConnectSource, nil)
	checkVsockConnect(t, runX86_64Bin(runner, bin))
}

func TestArm64VsockConnect(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, vsockConnectSource, nil)
	checkVsockConnect(t, runArm64Bin(qemu, bin))
}

// Darwin has no VSOCK: the builtin is refused, and std/net gives
// EAFNOSUPPORT without it.
func TestArm64DarwinVsockConnect(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.fern")
	if err := os.WriteFile(raw, []byte("function main(): i32 { return vsock_connect(2, 1).fd; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetArm64Darwin, raw, filepath.Join(dir, "raw")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "required by `vsock_connect`") {
		t.Errorf("arm64-darwin built vsock_connect, or the refusal does not name it: %v\n%s", err, out)
	}
	src := `import "std/net";

function main(): i32 {
    match (net.connect_vsock(2, 1)) {
        Ok(_) => {
            return 1;
        },
        Err(e) => {
            return e.errno();
        }
    }
    return 2;
}
`
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, src, nil)
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	if code := exitCodeOf(exec.Command(bin).Run()); code != 97 {
		t.Errorf("std/net on arm64-darwin: exit %d, want EAFNOSUPPORT (97)", code)
	}
}

// The interpreters open no VSOCK socket: -ENOSYS.
func TestInterpVsockConnect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prog.fern")
	src := `function main(): i32 {
    if (vsock_connect(2, 1).fd >= 0) { return 2; }
    return 0;
}
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	checkStatuses(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
}

func TestWASMVsockConnectRefused(t *testing.T) {
	const src = `function main(): i32 { return vsock_connect(2, 1).fd; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{e2eharness.TargetWasm32Wasi, "wasm32-wasi-http", "arm64-darwin"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 || vs[0].Builtin != "vsock_connect" || vs[0].Capability != "vsock" {
			t.Errorf("platforms.Enforce on %s: %v, want vsock_connect refused on vsock", target, vs)
		}
	}
	if vs := platforms.Enforce(prog, "arm64-linux"); len(vs) != 0 {
		t.Errorf("platforms.Enforce on arm64-linux: %v, want none", vs)
	}
	out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, path, filepath.Join(dir, "prog.wasm")).CombinedOutput()
	if err == nil {
		t.Errorf("the compiler built vsock_connect for wasm32-wasi, which has no VSOCK")
	}
	if !strings.Contains(string(out), "required by `vsock_connect`; targets providing it") {
		t.Errorf("the wasm32-wasi refusal does not name the builtin:\n%s", out)
	}
}
