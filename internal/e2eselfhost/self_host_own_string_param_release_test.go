package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An `own` parameter's reference is the callee's to release, and the AST
// lowering released one only when it was a struct, so every `own` string
// leaked a box per call (#8805). The semantic lowering, which the CLI takes by
// default, already balanced; FERN_SEM_IR= selects the AST lowering, which still
// lowers any function the semantic path refuses. Each shape runs 100 times:
// consumed in place, handed straight back, and forwarded to another `own`
// position, beside an `own` array.
const ownStringParamSrc = `import "std/i32";
function takes(own s: string): i32 { return s.len(); }
function back(own s: string): string { return s; }
function fwd(own s: string): i32 { return takes(s); }
function takea(own a: i32[]): i32 { return a.len(); }
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        n = n + takes("a" + i.to_string());
        var b: string = back("b" + i.to_string());
        n = n + b.len();
        n = n + fwd("c" + i.to_string());
        n = n + takea([1, 2, i]);
        i = i + 1;
    }
    return n % 101;
}
`

// 3 x (10 x 2 + 90 x 3) + 100 x 3 = 1170.
const ownStringParamWant = 1170 % 101

var ownStringParamLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
}

func writeOwnStringParamSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "own_string_param.fern")
	if err := os.WriteFile(path, []byte(ownStringParamSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostOwnStringParamReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeOwnStringParamSrc(t)
	for _, lw := range ownStringParamLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != ownStringParamWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, ownStringParamWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostOwnStringParamReleaseSanitizeX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeOwnStringParamSrc(t)
	for _, lw := range ownStringParamLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != ownStringParamWant || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, ownStringParamWant, stderr)
			}
		})
	}
}

func TestSelfHostOwnStringParamReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm own-param release")
	}
	cli := buildSelfHostCLI(t)
	src := writeOwnStringParamSrc(t)
	for _, lw := range ownStringParamLowerings {
		t.Run(lw.name, func(t *testing.T) {
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env)
			stderr, exit := runWasmCensus(t, wat)
			if exit != ownStringParamWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, ownStringParamWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
