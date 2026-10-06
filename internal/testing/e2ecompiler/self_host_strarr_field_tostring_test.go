package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A record's string[] field built from a number's `.to_string()` was refused
// by the element freshness proof, so the field was marked program-wide and
// every Rec lost its deep drop: the AST lowering left the buffer and the
// decimal text behind on every round (#10319).
const strarrFieldToStringSrc = `import "std/i32";
struct Rec { n: i32, xs: string[] }
function round(i: i32): i32 {
    let r: Rec = Rec { n: i, xs: [i.to_string(), "x", (i * 3).to_string()] };
    return r.n + r.xs.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    return acc % 83;
}
`

// Interpreter-confirmed.
const strarrFieldToStringWant = 21

func writeStrarrFieldToStringSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "strarr_field_tostring.fern")
	if err := os.WriteFile(path, []byte(strarrFieldToStringSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostStrarrFieldToStringX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStrarrFieldToStringSrc(t)
	bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != strarrFieldToStringWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, strarrFieldToStringWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostStrarrFieldToStringSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeStrarrFieldToStringSrc(t)
	bin := cli.x86Binary(t, src, "FERN_SANITIZE=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != strarrFieldToStringWant || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, strarrFieldToStringWant, stderr)
	}
}

func TestSelfHostStrarrFieldToStringArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeStrarrFieldToStringSrc(t)
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "strarr_field_tostring", string(asm)))
	var eb strings.Builder
	cmd.Stderr = &eb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != strarrFieldToStringWant {
		t.Fatalf("exit = %d, want %d\n%s", code, strarrFieldToStringWant, eb.String())
	}
	assertBalancedCensus(t, eb.String())
}

func TestSelfHostStrarrFieldToStringWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm string[]-field release")
	}
	cli := buildSelfHostCLI(t)
	src := writeStrarrFieldToStringSrc(t)
	wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1")
	stderr, exit := runWasmCensus(t, wat)
	if exit != strarrFieldToStringWant {
		t.Fatalf("exit = %d, want %d\n%s", exit, strarrFieldToStringWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
