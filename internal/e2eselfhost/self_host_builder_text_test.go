package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Also compile the interpreter with the current backend. Direct CLI tests
// below cover the installed stage1 interpreter built by the older pin.
func runBuilderTextInterpreter(t *testing.T, compiler string, runner []string, target, stdlib string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")
	bin := filepath.Join(dir, "interp")
	cmd := runX86_64Bin(runner, compiler, "-target", target, "-o", bin, filepath.Join(dir, "interp_run.fern"), stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile current interpreter: %v\n%s", err, out)
	}
	data, err := os.ReadFile(e2eharness.WriteBuilderTextFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// interp_run has no module loader. Exact expected strings already prove
	// validity; the native fixture additionally calls std/utf8's validator.
	source := strings.Replace(string(data), "import \"std/utf8\";\n", "", 1)
	source = strings.Replace(source, "  if (!utf8.is_valid_utf8(first) || !utf8.is_valid_utf8(second)) { return false; }\n", "", 1)
	cmd = runX86_64Bin(runner, bin)
	cmd.Stdin = bytes.NewBufferString(source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("current interpreter: %v\n%s", err, out)
	}
}

func runBuilderTextComponent(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	bin := filepath.Join(t.TempDir(), "builder-text.wasm")
	cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, e2eharness.WriteBuilderTextFixture(t), stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("component compile: %v\n%s", err, out)
	}
	if out, err := exec.Command("wasmtime", "run", bin).CombinedOutput(); err != nil {
		t.Fatalf("component run: %v\n%s", err, out)
	}
	// wasi:cli/run does not emit the command module's exit-time census.
	// Native/core tests below check allocation balance on this fixture.
}

func TestSelfHostBuilderText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("pin-built-interp", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", e2eharness.WriteBuilderTextFixture(t), cli.stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pin-built interpreter: %v\n%s", err, out)
		}
	})
	t.Run("raw-runtime", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "raw.fern")
		if err := os.WriteFile(src, []byte(`function main(): i32 {
  let h: usize = buf_new(1); buf_push_byte(h, 255);
  let raw: u8[] = buf_take_bytes(h); buf_free(h);
  if (raw.len() != 1 || raw[0] != 255 as u8) { return 1; }
  return 0;
}`), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			data, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "__fern_buf_text_") || strings.Contains(string(data), "__fern_buf_take_text") {
				t.Fatalf("%s includes text repair in a raw-only program", target)
			}
		}
	})
	t.Run("interp", func(t *testing.T) {
		runBuilderTextInterpreter(t, cli.bin, cli.runner, "x86-64-linux", cli.stdlib)
	})
	t.Run("component", func(t *testing.T) {
		runBuilderTextComponent(t, cli.bin, cli.runner, cli.stdlib)
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, e2eharness.WriteBuilderTextFixture(t), target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostArm64DarwinBuilderText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := e2eharness.WriteBuilderTextFixture(t)
	t.Run("pin-built-interp", func(t *testing.T) {
		if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("pin-built interpreter: %v\n%s", err, out)
		}
	})
	t.Run("native", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "builder-text")
		compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
		compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
		if out, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		if out, err := exec.Command(bin).CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		} else {
			assertBalancedCensus(t, string(out))
		}
	})
	t.Run("interp", func(t *testing.T) {
		runBuilderTextInterpreter(t, cli, nil, "arm64-darwin", stdlib)
	})
	t.Run("component", func(t *testing.T) {
		runBuilderTextComponent(t, cli, nil, stdlib)
	})
}
