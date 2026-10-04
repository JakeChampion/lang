package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func readerTextSource(t *testing.T) (string, e2eharness.ReaderTextFixture) {
	t.Helper()
	f := e2eharness.MakeReaderTextFixture()
	src := filepath.Join(t.TempDir(), "reader.fern")
	if err := os.WriteFile(src, []byte(f.Source), 0o644); err != nil {
		t.Fatal(err)
	}
	return src, f
}

func checkReaderTextComponent(t *testing.T, compiler string, runner []string, stdlib, src string, f e2eharness.ReaderTextFixture) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	bin := filepath.Join(t.TempDir(), "reader.wasm")
	cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("component compile: %v\n%s", err, out)
	}
	// Components return through wasi:cli/run without the command module's
	// exit-time census. Native/core legs below verify allocation balance.
	f.Check(t, exec.Command("wasmtime", "run", bin))
}

// Also compile the interpreter with the current backend. The direct CLI
// checks below cover the interpreter built by the pin's older runtime.
func checkReaderTextInterpreter(t *testing.T, compiler string, runner []string, target, stdlib string, f e2eharness.ReaderTextFixture) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")
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
`, f.Source)
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "reader-interp")
	cmd := runX86_64Bin(runner, compiler, "-target", target, "-o", bin, entry, stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile current interpreter: %v\n%s", err, out)
	}
	f.Check(t, runX86_64Bin(runner, bin))
}

func TestSelfHostReaderText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, f := readerTextSource(t)
	t.Run("pin-built-interp", func(t *testing.T) { f.Check(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib)) })
	t.Run("interp", func(t *testing.T) { checkReaderTextInterpreter(t, cli.bin, cli.runner, "x86-64-linux", cli.stdlib, f) })
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, f.Input, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	t.Run("component", func(t *testing.T) { checkReaderTextComponent(t, cli.bin, cli.runner, cli.stdlib, src, f) })
}

func TestSelfHostArm64DarwinReaderText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src, f := readerTextSource(t)
	t.Run("pin-built-interp", func(t *testing.T) { f.Check(t, exec.Command(cli, "-interp", src, stdlib)) })
	t.Run("interp", func(t *testing.T) { checkReaderTextInterpreter(t, cli, nil, "arm64-darwin", stdlib, f) })
	bin := filepath.Join(t.TempDir(), "reader")
	cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	assertBalancedCensus(t, f.Check(t, exec.Command(bin)))
	t.Run("component", func(t *testing.T) { checkReaderTextComponent(t, cli, nil, stdlib, src, f) })
}

func TestSelfHostPerModuleReaderText(t *testing.T) {
	checkSelfHostPerModuleByteSink(t, `pub function save(): i32 {
  match (write_file_bytes("text.bin", [195 as u8, 169 as u8, 255 as u8])) {
    Err(_) => { return 1; }, Ok(_) => {}
  }
  match (open_reader("text.bin")) {
    Err(_) => { return 2; },
    Ok(r) => {
      match (r.read_chunk(2)) { Ok(s) => { if (s != "é") { return 3; } }, Err(_) => { return 4; } }
      match (r.read_chunk(1)) {
        Ok(_) => { return 5; },
        Err(e) => { match (e) { InvalidUtf8(p) => { if (p != "") { return 6; } }, _ => { return 7; } } }
      }
      match (r.close()) { Some(_) => { return 8; }, None => {} }
    }
  }
  return 0;
}
`, nil)
}
