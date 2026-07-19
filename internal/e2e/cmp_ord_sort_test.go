package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Ord-bounded `sort` in std/array — the no-comparator companion to the shipped
// `sort_by(xs, cmp)`, the last remaining verb under #2689. The `T: cmp.Ord`
// bound supplies the ordering (`a.cmp(b)`) and, exactly like the Eq verbs
// (#3872), is what monomorphises it per element type so it lowers on the native
// backends AND the self-host IR path: an `i32` instance lowers `.cmp` to the
// scalar three-way compare, a `string` instance to the lexicographic byte
// compare, where an unbounded `[T]` would erase to one pointer-compare clone and
// mis-order. The inline cases pin the routing over a primitive `impl Ord for
// i32` (the i32[] return genuinely lowers on the IR path) and a user `Ord`
// struct (genuine genericity, struct[] return rides the AST fallback — both
// produce the right answer); a native module test exercises the shipped
// `std/array` body over i32 / string / a user struct.
var ordSortCases = []struct {
	name string
	main string
	want int
}{
	// sort i32 ascending: [3,1,2] -> [1,2,3]; encode s[0]*100+s[1]*10+s[2] = 123.
	{"i32-ascending", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { var out: T[] = []; for x in xs { out = out.append(x); var i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { var tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { var s = sort([3, 1, 2]); return s[0] * 100 + s[1] * 10 + s[2]; }`, 123},
	// sort i32 reverse-ordered five: [5,4,3,2,1] -> [1,2,3,4,5];
	// s[0]*100 + s[2]*5 + s[4] = 1*100 + 3*5 + 5 = 120. (Kept < 126 so the wasm
	// leg's raw WASI exit code is a valid status — wasmtime rejects codes >= 126.)
	{"i32-five", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { var out: T[] = []; for x in xs { out = out.append(x); var i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { var tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { var s = sort([5, 4, 3, 2, 1]); return s[0] * 100 + s[2] * 5 + s[4]; }`, 120},
	// sort over a user Ord struct (compared by .v): [P2,P1,P3] -> [P1,P2,P3];
	// s[0].v*100 + s[1].v*10 + s[2].v = 123.
	{"struct", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { var out: T[] = []; for x in xs { out = out.append(x); var i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { var tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { var xs: P[] = [P { v: 2 }, P { v: 1 }, P { v: 3 }]; var s = sort(xs); return s[0].v * 100 + s[1].v * 10 + s[2].v; }`, 123},
	// stability: keys [1,1,0] with tags [2,1,0]. A STABLE sort by key keeps the
	// two key=1 elements in input order (tag 2 before tag 1) -> tags [0,2,1];
	// s[0].tag*100 + s[1].tag*10 + s[2].tag = 21 (a non-stable sort would give 12).
	{"stable", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { key: i32, tag: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.key < other.key) { return 0 - 1; } if (self.key > other.key) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { var out: T[] = []; for x in xs { out = out.append(x); var i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { var tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { var xs: P[] = [P { key: 1, tag: 2 }, P { key: 1, tag: 1 }, P { key: 0, tag: 0 }]; var s = sort(xs); return s[0].tag * 100 + s[1].tag * 10 + s[2].tag; }`, 21},
}

// TestNativeOrdSort runs the inline Ord-sort programs on the native interp /
// x86-64 / wasm backends, oracle-checked.
func TestNativeOrdSort(t *testing.T) {
	for _, tc := range ordSortCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeOrdSortArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeOrdSortArm64(t *testing.T) {
	for _, tc := range ordSortCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeOrdSortModule exercises the shipped `import "core/cmp"` body: the
// `sort` verb over the primitive `impl Ord for i32` / `string` AND a user Ord
// struct. (The generic `sort[T: Ord]` verb's single home is core/cmp — #5348.)
func TestNativeOrdSortModule(t *testing.T) {
	src := `import "core/cmp" as cmp;
struct P { v: i32 }
impl cmp.Ord for P {
    function cmp(self: Self, other: Self): i32 {
        if (self.v < other.v) { return 0 - 1; }
        if (self.v > other.v) { return 1; }
        return 0;
    }
}
function main(): i32 {
    var r = 0;
    var s = cmp.sort([3, 1, 2]);
    if (s[0] == 1 && s[1] == 2 && s[2] == 3) { r = r + 1; }
    var ss = cmp.sort(["banana", "apple", "cherry"]);
    if (ss[0] == "apple" && ss[1] == "banana" && ss[2] == "cherry") { r = r + 2; }
    var ps = cmp.sort([P { v: 2 }, P { v: 1 }, P { v: 3 }]);
    if (ps[0].v == 1 && ps[1].v == 2 && ps[2].v == 3) { r = r + 4; }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 7 {
		t.Errorf("ord-sort module interp = %d, want 7", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 7 {
		t.Errorf("ord-sort module x86-64 = %d, want 7", code)
	}
	if code := runWasm(t, src); code != 7 {
		t.Errorf("ord-sort module wasm = %d, want 7", code)
	}
}

// TestSelfHostOrdSortIRX86_64 drives each inline Ord-sort case through the
// self-hosted x86-64 compiler and oracle-checks the exit code. The i32[] cases
// lower on the IR path; the struct[] cases ride the AST fallback (the generic
// struct[] return) — both produce the right answer, so this asserts behaviour
// (as the Eq-verb `distinct` case does). The native legs above pin cross-backend
// correctness.
func TestSelfHostOrdSortIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range ordSortCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.main+"\n"))
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

// TestSelfHostOrdSortIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostOrdSortIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host ord-sort wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range ordSortCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
			watFile := filepath.Join(dir, "ord_sort_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("ord-sort wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
