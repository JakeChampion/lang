package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fieldAppendGrowthSrc = `struct Occ { name: string, line: i32 }
struct Facts { occ: Occ[], esc: string[] }
enum Node { NIdent(Occ), NLam(i32), NOther(i32) }
function step(e: Node, own f: Facts): Facts {
    match (e) {
        NIdent(id) => {
            return Facts { ...f, occ: f.occ.append(Occ { name: id.name, line: id.line }) };
        },
        NLam(_) => {
            let esc: string[] = f.esc;
            return Facts { ...f, esc: esc };
        },
        _ => { return f; }
    }
    return f;
}
function sum_lines(fx: Facts): i32 {
    let d: i32 = 0;
    for o in fx.occ { d = d + o.line + o.name.len(); }
    return d;
}
function run(k: i32): i32 {
    let facts: Facts = Facts { occ: [], esc: [] };
    let i: i32 = 0;
    while (i < 9) {
        facts = step(NIdent(Occ { name: "n", line: i + k }), facts);
        i = i + 1;
    }
    return sum_lines(facts) + sum_lines(facts);
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 3) { t = t + run(k); k = k + 1; }
    return t % 256;
}
`

const fieldAppendGrowthWant = 68

func writeFieldAppendGrowthSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "field_append_growth.fern")
	if err := os.WriteFile(path, []byte(fieldAppendGrowthSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostFieldAppendGrowthSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeFieldAppendGrowthSrc(t)
	bin := cli.x86Binary(t, src, "FERN_SANITIZE=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != fieldAppendGrowthWant || forArrStructSanitizerFault(stderr, true) {
		t.Fatalf("exit = %d, want %d, and no sanitizer report but a leak\n%s", exit, fieldAppendGrowthWant, stderr)
	}
}

func TestSelfHostFieldAppendGrowthArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeFieldAppendGrowthSrc(t)
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "field_append_growth", string(asm)))
	var eb strings.Builder
	cmd.Stderr = &eb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != fieldAppendGrowthWant {
		t.Fatalf("exit = %d, want %d\n%s", code, fieldAppendGrowthWant, eb.String())
	}
	assertBalancedCensus(t, eb.String())
}

func TestSelfHostFieldAppendGrowthWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm field-append growth")
	}
	cli := buildSelfHostCLI(t)
	src := writeFieldAppendGrowthSrc(t)
	stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
	if exit != fieldAppendGrowthWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, fieldAppendGrowthWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
