package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setOpsIRCases exercise the Eq-driven set-algebra verbs
// (`count` / `union` / `intersection` / `difference`) through the
// self-host IR path on x86-64 + wasm. As with the eq-verbs cases, the
// bare single-program driver resolves no imports, so the surface is
// inlined from `internal/stdlib/std/array.fern`: the `[T: Eq]` bodies
// (plus the `contains` they build on), an inline `Eq` trait + the i32 /
// string primitive impls, all stand-ins for core/cmp.
//
// The `Eq` bound is what makes the verbs clone per element type on the
// self-host path — an unbounded `[T]` would erase to one scalar-`==`
// clone that miscompares strings (pointer identity). This pins, in one
// program: per-type monomorphisation of the set ops, the nested
// bounded-generic calls (`union`/`intersection`/`difference` ->
// `contains`), and the string-vs-scalar `==` dispatch. Each case returns
// a small deterministic int (<= 125 so wasmtime accepts the process exit
// code), oracle-checked against the interpreter. FEATURE-AUDIT std/array
// row (#2689).
const setOpsIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function contains[T: Eq](xs: T[], target: T): boolean {
    for x in xs {
        if (x == target) { return true; }
    }
    return false;
}
pub function count[T: Eq](xs: T[], target: T): i32 {
    var c: i32 = 0;
    for x in xs {
        if (x == target) { c = c + 1; }
    }
    return c;
}
pub function union[T: Eq](a: T[], b: T[]): T[] {
    var out: T[] = [];
    for x in a {
        if (!contains(out, x)) { out = out.append(x); }
    }
    for y in b {
        if (!contains(out, y)) { out = out.append(y); }
    }
    return out;
}
pub function intersection[T: Eq](a: T[], b: T[]): T[] {
    var out: T[] = [];
    for x in a {
        if (contains(b, x) && !contains(out, x)) { out = out.append(x); }
    }
    return out;
}
pub function difference[T: Eq](a: T[], b: T[]): T[] {
    var out: T[] = [];
    for x in a {
        if (!contains(b, x) && !contains(out, x)) { out = out.append(x); }
    }
    return out;
}
`

var setOpsIRCases = []struct {
	name string
	main string
	want int
}{
	// count over i32[] (3 twos) and string[] (2 x's): 3*10 + 2 = 32.
	{"count-mixed", `var a: i32[] = [1, 2, 2, 3, 2]; var ss: string[] = ["x", "y", "x"]; return count(a, 2) * 10 + count(ss, "x");`, 32},
	// union dedups across both: {1,2,3,4,5}; len*10 + first + last.
	{"union-i32", `var a: i32[] = [1, 2, 2, 3, 4]; var b: i32[] = [3, 4, 4, 5]; var u: i32[] = union(a, b); return u.len() * 10 + u[0] + u[4];`, 56},
	// intersection in a-order: {3,1} -> [3,1]; len*10 + x0*2 + x1.
	{"intersection-i32", `var a: i32[] = [4, 3, 2, 1]; var b: i32[] = [1, 3, 5]; var x: i32[] = intersection(a, b); return x.len() * 10 + x[0] * 2 + x[1];`, 27},
	// difference a\b over strings: ["a","b","c","b"] \ ["b"] = {a,c}; len*10 + (a==first?).
	{"difference-string", `var ss: string[] = ["a", "b", "c", "b"]; var tt: string[] = ["b"]; var d: string[] = difference(ss, tt); var r: i32 = d.len() * 10; if (d[0] == "a") { r = r + 1; } if (d[1] == "c") { r = r + 2; } return r;`, 23},
}

func setOpsIRSrc(mainBody string) string {
	return setOpsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostSetOpsIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostSetOpsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range setOpsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(setOpsIRSrc(tc.main))
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

// TestSelfHostSetOpsIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostSetOpsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host set-ops wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range setOpsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(setOpsIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "set_ops_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("set-ops wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
