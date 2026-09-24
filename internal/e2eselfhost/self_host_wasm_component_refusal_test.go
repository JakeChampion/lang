package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A handle-carrying tuple literal, `(stdout(), true)`, lowers wherever the same
// tuple built from a named handle does (#9238). And a module the lowering takes
// whole but a wasm COMPONENT cannot import for is refused by naming the op: the
// component framing has no preview2 body for opening a file, and the refusal
// used to be the generic "not IR-eligible", with FERN_STRICT_IR=1 naming nothing.
const handleTupleSrc = `function handle_lit(): (Writer, boolean) {
    return (stdout(), true);
}
function handle_var(): (Writer, boolean) {
    var w: Writer = stdout();
    return (w, true);
}
function main(): i32 {
    var a: (Writer, boolean) = handle_lit();
    var b: (Writer, boolean) = handle_var();
    match (a.0.write("ok\n")) { Some(_) => { return 1; }, None => {} }
    if (a.1 && b.1) { return 7; }
    return 0;
}
`

const openFileSrc = `function main(): i32 {
    match (open_reader("x.txt")) { Ok(_) => { return 1; }, Err(_) => { return 2; } }
    return 0;
}
`

func writeHandleTupleSrc(t *testing.T) string {
	t.Helper()
	return writeSrc(t, "handle_tuple.fern", handleTupleSrc)
}

func writeSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostHandleTupleLiteralX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeHandleTupleSrc(t)
	for _, env := range []string{"FERN_SEM_IR=1", "FERN_SEM_IR="} {
		t.Run(env, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1", env)
			stdout, exit := runStdout(t, cli.runner, bin)
			if exit != 7 || stdout != "ok\n" {
				t.Fatalf("exit = %d, stdout %q; want 7 and \"ok\\n\"", exit, stdout)
			}
		})
	}
}

func TestSelfHostHandleTupleLiteralWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm handle tuple")
	}
	cli := buildSelfHostCLI(t)
	src := writeHandleTupleSrc(t)
	dir := t.TempDir()

	// A component's run export reports any non-zero main as an error, so the
	// host exits 1, as native's component does.
	t.Run("component-runs", func(t *testing.T) {
		comp := filepath.Join(dir, "comp.wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", src, cli.stdlib, "-o", comp)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("component build: %v\n%s", err, out)
		}
		run := exec.Command("wasmtime", "run", comp)
		got, _ := run.Output()
		if code := run.ProcessState.ExitCode(); code != 1 || string(got) != "ok\n" {
			t.Fatalf("exit = %d, stdout %q; want 1 and \"ok\\n\"", code, got)
		}
	})

	t.Run("component-names-the-op", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", writeSrc(t, "open_file.fern", openFileSrc), cli.stdlib, "-o", filepath.Join(dir, "open.wasm"))
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("component build succeeded; want a refusal naming open_file\n%s", out)
		}
		if !strings.Contains(string(out), "open_file is not supported in a wasm component") {
			t.Fatalf("refusal does not name the op:\n%s", out)
		}
	})

	t.Run("core-module-runs", func(t *testing.T) {
		wasm := filepath.Join(dir, "core.wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "core-module", src, cli.stdlib, "-o", wasm)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("core-module build: %v\n%s", err, out)
		}
		run := exec.Command("wasmtime", "run", wasm)
		got, _ := run.Output()
		if code := run.ProcessState.ExitCode(); code != 7 || string(got) != "ok\n" {
			t.Fatalf("exit = %d, stdout %q; want 7 and \"ok\\n\"", code, got)
		}
	})
}

func runStdout(t *testing.T, runner []string, bin string) (string, int) {
	t.Helper()
	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.Output()
	return string(out), cmd.ProcessState.ExitCode()
}
