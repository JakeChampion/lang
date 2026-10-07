package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A struct local with an array field keeps its release when a callee hands it
// back, rebuilds it through a field read, or takes its field as an argument
// (#9203). A field read at a call argument does not move the field out — the
// callee's construction retains that argument — so the local keeps its deep
// drop.
const structFieldArgSrc = `struct Big { neg: boolean, mag: u64[] }
@noinline
function make(neg: boolean, mag: u64[]): Big { return Big { neg: neg, mag: mag }; }
@noinline
function (a: Big) id_or_make(k: i32): Big {
    if (k <= 0) { return a; }
    let mag: u64[] = a.mag;
    return make(a.neg, mag);
}
@noinline
function handed_back(i: i32): i32 {
    let b: Big = Big { neg: false, mag: [1 as u64, 2 as u64, 3 as u64] };
    let c: Big = b.id_or_make(0);
    return c.mag.len() + i;
}
@noinline
function remade(i: i32): i32 {
    let b: Big = Big { neg: false, mag: [1 as u64, 2 as u64] };
    let c: Big = b.id_or_make(1);
    return c.mag.len() + i;
}
@noinline
function field_args(i: i32): i32 {
    let b: Big = Big { neg: true, mag: [4 as u64] };
    let c: Big = make(b.neg, b.mag);
    return c.mag.len() + b.mag.len() + i;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
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
	bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != structFieldArgWant {
		t.Fatalf("exit = %d, want %d (99 = rc underflow)\n%s", exit, structFieldArgWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostStructFieldArgReleaseSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	bin := cli.x86Binary(t, src, "FERN_SANITIZE=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != structFieldArgWant || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, structFieldArgWant, stderr)
	}
}

func TestSelfHostStructFieldArgReleaseArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
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
}

func TestSelfHostStructFieldArgReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm struct field-argument release")
	}
	cli := buildSelfHostCLI(t)
	src := writeStructFieldArgSrc(t)
	wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1")
	stderr, exit := runWasmCensus(t, wat)
	if exit != structFieldArgWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, structFieldArgWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
