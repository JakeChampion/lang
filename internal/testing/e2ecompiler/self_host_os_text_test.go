package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostOSText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi", "component", "interp"} {
		t.Run(target, func(t *testing.T) {
			f := e2eharness.MakeOSTextFixture(t, target != "wasm32-wasi" && target != "component")
			checkSelfHostOSText(t, cli, target, f)
		})
	}
}

func checkSelfHostOSText(t *testing.T, cli *selfHostCLI, target string, f e2eharness.OSTextFixture) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "os-text.fern")
	if err := os.WriteFile(src, []byte(f.Source), 0o600); err != nil {
		t.Fatal(err)
	}
	actual := target
	if target == "component" {
		actual = "wasm32-wasi"
	}
	if target == "interp" {
		// Build the interpreter with the current runtime, rather than the pin's.
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "lexer.fern", "parser.fern", "interp.fern")
		src = filepath.Join(dir, "os-text-interp.fern")
		code := fmt.Sprintf(`import "./lexer"; import "./parser"; import "./interp";
function main(): i32 { let m: parser.Module = parser.parse_module(lexer.tokenize(%q));
 match (interp.eval_module(m)) { interp.VInt(v) => { return v.v; }, _ => { return 254; } } }
`, f.Source)
		if err := os.WriteFile(src, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		actual = "x86-64-linux"
	}
	bin := filepath.Join(t.TempDir(), "os-text")
	args := []string{"-target", actual, "-o", bin}
	if target == "wasm32-wasi" {
		args = append(args, "-emit", "command-module")
	}
	args = append(args, src, cli.stdlib)
	compile := runX86_64Bin(cli.runner, cli.bin, args...)
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	var run *exec.Cmd
	switch target {
	case "wasm32-wasi", "component":
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("requires wasmtime")
		}
		run = exec.Command("wasmtime", "run", "--dir", f.Dir+"::.", bin)
	case "arm64-linux":
		_, qemu := arm64Tooling(t)
		if qemu == "" {
			run = exec.Command(bin)
		} else {
			run = exec.Command(qemu, bin)
		}
	default:
		run = runX86_64Bin(cli.runner, bin)
	}
	run.Dir = f.Dir
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if target != "interp" && target != "component" {
		assertBalancedCensus(t, string(out))
	}
}

func TestSelfHostOSTextGetcwd(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "interp"} {
		t.Run(target, func(t *testing.T) { checkSelfHostOSText(t, cli, target, e2eharness.MakeInvalidCwdFixture(t)) })
	}
}

func TestSelfHostArm64DarwinOSText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := &selfHostCLI{bin: buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern"), stdlib: e2eharness.SelfHostStdlibRoot(t)}
	checkSelfHostOSText(t, cli, "arm64-darwin", e2eharness.MakeOSTextFixture(t, true))
	checkSelfHostOSText(t, cli, "arm64-darwin", e2eharness.MakeInvalidCwdFixture(t))
}
