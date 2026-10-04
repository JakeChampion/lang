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

func xattrBytesSource(t *testing.T) (string, e2eharness.XattrBytesFixture) {
	t.Helper()
	fixture := e2eharness.MakeXattrBytesFixture(t)
	src := filepath.Join(t.TempDir(), "xattr.fern")
	if err := os.WriteFile(src, []byte(fixture.Source), 0o644); err != nil {
		t.Fatal(err)
	}
	return src, fixture
}

func TestSelfHostXattrBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux attributes require a Linux host")
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"interp", "x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			src, fixture := xattrBytesSource(t)
			if target == "interp" {
				out, diagnostic, code := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib), "")
				if code != 0 || out != "" {
					t.Fatalf("interpreter: exit %d, stdout %q, stderr %q", code, out, diagnostic)
				}
			} else {
				stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit = %d\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			}
			fixture.CheckWrites(t)
		})
	}
}

// The interpreter is also part of the WASM playground. Its native xattr
// dispatch must not prevent that build, and interpreted calls must report
// Unsupported instead of attempting a native operation.
func TestSelfHostWasmInterpXattrUnsupported(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")
	driver := cli.emit(t, filepath.Join(dir, "interp_run.fern"), "wasm32-wasi", "FERN_STRICT_IR=1")
	for _, call := range []string{
		`getxattr("a", "user.x")`, `lgetxattr("a", "user.x")`,
		`getxattr_bytes("a", "user.x")`, `lgetxattr_bytes("a", "user.x")`,
		`setxattr("a", "user.x", "text")`, `lsetxattr("a", "user.x", "text")`,
		`setxattr_bytes("a", "user.x", [255 as u8])`, `lsetxattr_bytes("a", "user.x", [])`,
	} {
		t.Run(call, func(t *testing.T) {
			cmd := exec.Command("wasmtime", "run", driver)
			cmd.Stdin = strings.NewReader("function main(): i32 { match (" + call + ") { Ok(_) => { return 1; }, Err(e) => { match (e) { Unsupported => { return 0; }, _ => { return 2; } } } } }")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("WASM interpreter: %v\n%s", err, out)
			}
		})
	}
}

func TestSelfHostArm64DarwinXattrBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, target := range []string{"native", "interp"} {
		t.Run(target, func(t *testing.T) {
			src, fixture := xattrBytesSource(t)
			if target == "interp" {
				if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
					t.Fatalf("interpreter: %v\n%s", err, out)
				}
			} else {
				bin := filepath.Join(t.TempDir(), "xattr")
				compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
				compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if out, err := compile.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				out, err := exec.Command(bin).CombinedOutput()
				if err != nil {
					t.Fatalf("run: %v\n%s", err, out)
				}
				assertBalancedCensus(t, string(out))
			}
			fixture.CheckWrites(t)
		})
	}
	t.Run("WASI refusal", func(t *testing.T) {
		for _, call := range []string{`getxattr_bytes("a", "user.x")`, `lgetxattr_bytes("a", "user.x")`, `setxattr_bytes("a", "user.x", [255 as u8])`, `lsetxattr_bytes("a", "user.x", [])`} {
			src := filepath.Join(t.TempDir(), "refused.fern")
			body := "function main(): i32 { match (" + call + ") { Ok(_) => { return 0; }, Err(_) => { return 1; } } }"
			if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(cli, "-target", "wasm32-wasi", "-o", filepath.Join(t.TempDir(), "refused.wasm"), src, stdlib).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "E066") || !strings.Contains(string(out), "xattr") {
				t.Fatalf("expected xattr E066 for %s: %v\n%s", call, err, out)
			}
		}
	})
}
