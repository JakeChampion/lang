package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A struct local with an array field keeps its release when a callee hands it
// back, rebuilds it through a field read, or takes its field as an argument
// (#9203). The last leaked on the AST lowering: a field read at a call
// argument counted as moving the field out, so the local lost its deep drop,
// although the callee's construction retains that argument.
const structFieldArgSrc = `struct Big { neg: boolean, mag: u64[] }
@noinline
function make(neg: boolean, mag: u64[]): Big { return Big { neg: neg, mag: mag }; }
@noinline
function (a: Big) id_or_make(k: i32): Big {
    if (k <= 0) { return a; }
    var mag: u64[] = a.mag;
    return make(a.neg, mag);
}
@noinline
function handed_back(i: i32): i32 {
    var b: Big = Big { neg: false, mag: [1 as u64, 2 as u64, 3 as u64] };
    var c: Big = b.id_or_make(0);
    return c.mag.len() + i;
}
@noinline
function remade(i: i32): i32 {
    var b: Big = Big { neg: false, mag: [1 as u64, 2 as u64] };
    var c: Big = b.id_or_make(1);
    return c.mag.len() + i;
}
@noinline
function field_args(i: i32): i32 {
    var b: Big = Big { neg: true, mag: [4 as u64] };
    var c: Big = make(b.neg, b.mag);
    return c.mag.len() + b.mag.len() + i;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        t = t + handed_back(i) + remade(i) + field_args(i);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}
`

// Interpreter-confirmed.
const structFieldArgWant = 29

var structFieldArgLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
}

func writeStructFieldArgSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "struct_field_arg.fern")
	if err := os.WriteFile(path, []byte(structFieldArgSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostStructFieldArgReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	for _, lw := range structFieldArgLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != structFieldArgWant {
				t.Fatalf("exit = %d, want %d (99 = rc underflow)\n%s", exit, structFieldArgWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostStructFieldArgReleaseSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	for _, lw := range structFieldArgLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != structFieldArgWant || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, structFieldArgWant, stderr)
			}
		})
	}
}

func TestSelfHostStructFieldArgReleaseArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	for _, lw := range structFieldArgLowerings {
		t.Run(lw.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", lw.env))
			if err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "struct_field_arg", string(asm)))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != structFieldArgWant {
				t.Fatalf("exit = %d, want %d\n%s", code, structFieldArgWant, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostStructFieldArgReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct field-argument release")
	}
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	for _, lw := range structFieldArgLowerings {
		t.Run(lw.name, func(t *testing.T) {
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWasmCensus(t, wat)
			if exit != structFieldArgWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, structFieldArgWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
