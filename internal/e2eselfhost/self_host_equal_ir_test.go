package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// equalIRCases exercise the Eq-driven `equal` (structural array equality)
// and `index_of_last` verbs through the self-host IR path on x86-64 +
// wasm. As with the other Eq verbs the bare driver resolves no imports,
// so the surface is inlined: an `Eq` trait + i32 / string primitive
// impls (standing in for core/cmp) and the `[T: Eq]` bodies. Both verbs
// compare with the `==` operator (which lowers to the scalar compare /
// `str_eq`, not a method call), so they monomorphise and lower cleanly
// per element type. Each case returns a small deterministic int
// (<= 125, wasm exit-code safe), oracle-checked against the interpreter.
// #2689.
const equalIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function equal[T: Eq](a: T[], b: T[]): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) {
        if (a[i] != b[i]) { return false; }
        i = i + 1;
    }
    return true;
}
pub function index_of_last[T: Eq](xs: T[], target: T): Option[i32] {
    var i: i32 = xs.len() - 1;
    while (i >= 0) {
        if (xs[i] == target) { return Some(i); }
        i = i - 1;
    }
    return None;
}
function uw(o: Option[i32], d: i32): i32 {
    match (o) {
        Some(v) => { return v; },
        None => { return d; },
    }
    return d;
}
`

var equalIRCases = []struct {
	name string
	main string
	want int
}{
	// i32 equality: equal, value mismatch, length mismatch -> 1 + 2 + 4 = 7.
	{"equal-i32", `var a: i32[] = [1, 2, 3]; var b: i32[] = [1, 2, 3]; var c: i32[] = [1, 2, 4]; var r: i32 = 0; if (equal(a, b)) { r = r + 1; } if (!equal(a, c)) { r = r + 2; } if (!equal(a, [1, 2])) { r = r + 4; } return r;`, 7},
	// string equality via str_eq: equal + mismatch -> 8 + 4 = 12.
	{"equal-string", `var ss: string[] = ["x", "y", "x"]; var r: i32 = 0; if (equal(ss, ["x", "y", "x"])) { r = r + 8; } if (!equal(ss, ["x", "y", "z"])) { r = r + 4; } return r;`, 12},
	// index_of_last i32: last 5 at index 4 -> 40, plus miss -> +1 -> 41.
	{"last-i32", `var a: i32[] = [5, 1, 5, 2, 5]; var r: i32 = uw(index_of_last(a, 5), 0 - 1) * 10; if (uw(index_of_last(a, 9), 0 - 1) == 0 - 1) { r = r + 1; } return r;`, 41},
	// index_of_last string: last "a" at index 2 -> 2 (str compare on the reverse scan).
	{"last-string", `var ss: string[] = ["a", "b", "a", "c"]; return uw(index_of_last(ss, "a"), 0 - 1);`, 2},
}

func equalIRSrc(mainBody string) string {
	return equalIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostEqualIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostEqualIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range equalIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(equalIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostEqualIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostEqualIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host equal wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range equalIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(equalIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "equal_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("equal wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
