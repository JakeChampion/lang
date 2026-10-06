package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A handle-carrying tuple literal, `(stdout(), true)`, lowers wherever the same
// tuple built from a named handle does (#9238). And a sleeping program is a
// component too: the framing used to refuse it by naming the op (#11411), and
// before that with the generic "not IR-eligible", FERN_STRICT_IR=1 naming
// nothing.
const handleTupleSrc = `function handle_lit(): (Writer, boolean) {
    return (stdout(), true);
}
function handle_var(): (Writer, boolean) {
    let w: Writer = stdout();
    return (w, true);
}
function main(): i32 {
    let a: (Writer, boolean) = handle_lit();
    let b: (Writer, boolean) = handle_var();
    match (a.0.write("ok\n")) { Some(_) => { return 1; }, None => {} }
    if (a.1 && b.1) { return 7; }
    return 0;
}
`

const sleepSrc = `function main(): i32 {
    sleep_ms(1 as i64);
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
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	stdout, exit := runStdout(t, cli.runner, bin)
	if exit != 7 || stdout != "ok\n" {
		t.Fatalf("exit = %d, stdout %q; want 7 and \"ok\\n\"", exit, stdout)
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
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, src, cli.stdlib)
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

	t.Run("component-sleeps", func(t *testing.T) {
		comp := filepath.Join(dir, "sleep.wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, writeSrc(t, "sleep.fern", sleepSrc), cli.stdlib)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("component build: %v\n%s", err, out)
		}
		if out, err := exec.Command("wasmtime", "run", comp).CombinedOutput(); err != nil {
			t.Fatalf("wasmtime run: %v\n%s", err, out)
		}
	})

	t.Run("core-module-runs", func(t *testing.T) {
		wasm := filepath.Join(dir, "core.wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src, cli.stdlib)
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
