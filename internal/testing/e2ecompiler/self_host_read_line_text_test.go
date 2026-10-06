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

func readLineTextSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "lines.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ReadLineTextProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// Embed source so the interpreted program retains stdin for its own reads.
// Building with the current backend also exercises the current runtime.
func checkReadLineTextInterpreter(t *testing.T, compiler string, runner []string, target, stdlib string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "lexer.fern", "parser.fern", "interp.fern")
	entry := filepath.Join(dir, "interp_run.fern")
	source := fmt.Sprintf(`import "./lexer";
import "./parser";
import "./interp";
function main(): i32 {
  let mod: parser.Module = parser.parse_module(lexer.tokenize(%q));
  match (interp.eval_module(mod)) {
    interp.VInt(v) => { return v.v; },
    _ => { return 254; }
  }
}
`, e2eharness.ReadLineTextProgram)
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "line-interp")
	cmd := runX86_64Bin(runner, compiler, "-target", target, "-o", bin, entry, stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile interpreter: %v\n%s", err, out)
	}
	e2eharness.CheckReadLineText(t, func() *exec.Cmd { return runX86_64Bin(runner, bin) }, nil)
}

func checkReadLineTextComponent(t *testing.T, compiler string, runner []string, stdlib, src string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	bin := filepath.Join(t.TempDir(), "lines.wasm")
	cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("component compile: %v\n%s", err, out)
	}
	// Components do not emit an exit-time census; native/core legs do.
	e2eharness.CheckReadLineText(t, func() *exec.Cmd { return exec.Command("wasmtime", "run", bin) }, nil)
}

func TestSelfHostReadLineText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := readLineTextSource(t)
	t.Run("pin-built-interp", func(t *testing.T) {
		e2eharness.CheckReadLineText(t, func() *exec.Cmd { return runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib) }, nil)
	})
	t.Run("interp", func(t *testing.T) { checkReadLineTextInterpreter(t, cli.bin, cli.runner, "x86-64-linux", cli.stdlib) })
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var bin string
			var runner []string
			env := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
			switch target {
			case "x86-64-linux":
				bin, runner = cli.x86Binary(t, src, env...), cli.runner
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, env...))
				if err != nil {
					t.Fatal(err)
				}
				bin = buildBinArm64(t, gcc, t.TempDir(), "lines", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin, runner = cli.emit(t, src, target, env...), []string{"wasmtime", "run"}
			}
			argv := append(append([]string{}, runner...), bin)
			e2eharness.CheckReadLineText(t, func() *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }, assertBalancedCensus)
		})
	}
	t.Run("component", func(t *testing.T) { checkReadLineTextComponent(t, cli.bin, cli.runner, cli.stdlib, src) })
}

func TestSelfHostArm64DarwinReadLineText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := readLineTextSource(t)
	t.Run("pin-built-interp", func(t *testing.T) {
		e2eharness.CheckReadLineText(t, func() *exec.Cmd { return exec.Command(cli, "-interp", src, stdlib) }, nil)
	})
	t.Run("interp", func(t *testing.T) { checkReadLineTextInterpreter(t, cli, nil, "arm64-darwin", stdlib) })
	bin := filepath.Join(t.TempDir(), "lines")
	cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.CheckReadLineText(t, func() *exec.Cmd { return exec.Command(bin) }, assertBalancedCensus)
	t.Run("component", func(t *testing.T) { checkReadLineTextComponent(t, cli, nil, stdlib, src) })
}

func TestSelfHostPerModuleReadLineText(t *testing.T) {
	checkSelfHostPerModuleByteSink(t, `pub function save(): i32 {
  let first: string = "a".repeat(255) + "🙂\n";
  let second: string = "b".repeat(4095) + "€\n";
  match (write_file("lines.txt", first + second)) { Err(_) => { return 1; }, Ok(_) => {} }
  match (open_reader("lines.txt")) {
    Err(_) => { return 2; },
    Ok(r) => {
      let held: string = "";
      match (r.read_line()) { None => { return 3; }, Some(s) => { held = s; } }
      match (r.read_line()) { None => { return 4; }, Some(s) => { if (s != second) { return 5; } } }
      if (held != first) { return 6; }
      match (r.read_line()) { None => {}, Some(_) => { return 7; } }
      match (r.close()) { None => {}, Some(_) => { return 8; } }
    }
  }
  return 0;
}
`, nil)
}
