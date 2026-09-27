package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An `own` record's pointer-element field appended in place: when the push
// grows the unique buffer into a fresh box, the elements change hands with it,
// so the field must be moved out then too. The AST lowering moved it only when
// the result was the same buffer, so on growth the record's release freed
// every element the grown array still held. The NLam arm keeps `step` off the
// reuse path that would otherwise take the append.
const fieldAppendGrowthSrc = `struct Occ { name: string, line: i32 }
struct Facts { occ: Occ[], esc: string[] }
enum Node { NIdent(Occ), NLam(i32), NOther(i32) }
function step(e: Node, own f: Facts): Facts {
    match (e) {
        NIdent(id) => {
            return Facts { ...f, occ: f.occ.append(Occ { name: id.name, line: id.line }) };
        },
        NLam(_) => {
            var esc: string[] = f.esc;
            return Facts { ...f, esc: esc };
        },
        _ => { return f; }
    }
    return f;
}
function sum_lines(fx: Facts): i32 {
    var d: i32 = 0;
    for o in fx.occ { d = d + o.line + o.name.len(); }
    return d;
}
function run(k: i32): i32 {
    var facts: Facts = Facts { occ: [], esc: [] };
    var i: i32 = 0;
    while (i < 9) {
        facts = step(NIdent(Occ { name: "n", line: i + k }), facts);
        i = i + 1;
    }
    return sum_lines(facts) + sum_lines(facts);
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 3) { t = t + run(k); k = k + 1; }
    return t % 256;
}
`

// Interpreter-confirmed.
const fieldAppendGrowthWant = 68

// balanced: the lowering releases everything this program builds. The legs
// that AST-lower `run` keep its rebound `facts` past the exit, which is the
// AST lowering's reclaim gap and not this shape's.
var fieldAppendGrowthLowerings = []struct {
	name, env string
	balanced  bool
}{
	{"semantic", "FERN_SEM_IR=1", true},
	{"ast", "FERN_SEM_IR=", false},
	{"ast_step", "FERN_SEM_IR_SKIP=step", false},
	{"ast_main", "FERN_SEM_IR_SKIP=main", true},
}

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
	for _, lw := range fieldAppendGrowthLowerings {
		t.Run(lw.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env)
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != fieldAppendGrowthWant || forArrStructSanitizerFault(stderr, lw.balanced) {
				t.Fatalf("exit = %d, want %d, and no sanitizer report but a leak\n%s", exit, fieldAppendGrowthWant, stderr)
			}
		})
	}
}

func TestSelfHostFieldAppendGrowthArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeFieldAppendGrowthSrc(t)
	for _, lw := range fieldAppendGrowthLowerings {
		t.Run(lw.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", lw.env))
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
			if lw.balanced {
				assertBalancedCensus(t, eb.String())
			}
		})
	}
}

func TestSelfHostFieldAppendGrowthWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm field-append growth")
	}
	cli := buildSelfHostCLI(t)
	src := writeFieldAppendGrowthSrc(t)
	for _, lw := range fieldAppendGrowthLowerings {
		t.Run(lw.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env))
			if exit != fieldAppendGrowthWant {
				t.Fatalf("exit = %d, want %d\n%s", exit, fieldAppendGrowthWant, stderr)
			}
			if lw.balanced {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}
