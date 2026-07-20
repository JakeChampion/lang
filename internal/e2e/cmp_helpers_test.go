package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Generic Ord helpers in core/cmp — `min` / `max` / `clamp` / `lt` … over any
// `T: Ord`, derived from the single `cmp` primitive. A bounded generic whose
// body calls a trait method on the bound parameter monomorphises to a direct
// call, so these lower on the native backends AND the self-host IR path
// (#2691 / #3558). The inline cases below pin the self-host IR routing; a
// native test exercises the shipped module (incl. the primitive `impl Ord for
// i32` and a user `Ord` struct).
var cmpHelperCases = []struct {
	name string
	main string
	want int
}{
	// min/max over a user Ord struct (P ordered by .v). min(5,2).v + max(5,2).v = 2+5 = 7.
	{"min-max", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function min[T: Ord](a: T, b: T): T { if (b.cmp(a) < 0) { return b; } return a; }
function max[T: Ord](a: T, b: T): T { if (a.cmp(b) < 0) { return b; } return a; }
function main(): i32 { var lo = min(P { v: 5 }, P { v: 2 }); var hi = max(P { v: 5 }, P { v: 2 }); return lo.v + hi.v; }`, 7},
	// clamp(9, 1, 6).v = 6.
	{"clamp", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function clamp[T: Ord](x: T, lo: T, hi: T): T { if (x.cmp(lo) < 0) { return lo; } if (x.cmp(hi) > 0) { return hi; } return x; }
function main(): i32 { var c = clamp(P { v: 9 }, P { v: 1 }, P { v: 6 }); return c.v; }`, 6},
	// relational helpers return boolean (direct bounded-generic). lt(2,9)=true → 5.
	{"lt-gte", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function lt[T: Ord](a: T, b: T): boolean { return a.cmp(b) < 0; }
function gte[T: Ord](a: T, b: T): boolean { return a.cmp(b) >= 0; }
function main(): i32 { var r = 0; if (lt(P { v: 2 }, P { v: 9 })) { r = r + 5; } if (gte(P { v: 9 }, P { v: 9 })) { r = r + 2; } return r; }`, 7},
	// generic sort over a user Ord struct array → [1,2,3]; 1*100+2*10+3 = 123.
	{"sort", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function sort[T: Ord](arr: T[]): T[] { var out = arr; var n = out.len(); var i = 1; while (i < n) { var j = i; while (j > 0 && out[j].cmp(out[j - 1]) < 0) { var tmp = out[j]; out = out.with(j, out[j - 1]); out = out.with(j - 1, tmp); j = j - 1; } i = i + 1; } return out; }
function main(): i32 { var xs: P[] = [P { v: 3 }, P { v: 1 }, P { v: 2 }]; var s = sort(xs); return s[0].v * 100 + s[1].v * 10 + s[2].v; }`, 123},
	// is_sorted over a user Ord struct array. sorted→+5, unsorted→+2 → 7.
	{"is-sorted", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function is_sorted[T: Ord](arr: T[]): boolean { var i = 1; var n = arr.len(); while (i < n) { if (arr[i].cmp(arr[i - 1]) < 0) { return false; } i = i + 1; } return true; }
function main(): i32 { var a: P[] = [P { v: 1 }, P { v: 2 }, P { v: 3 }]; var b: P[] = [P { v: 3 }, P { v: 1 }]; var r = 0; if (is_sorted(a)) { r = r + 5; } if (!is_sorted(b)) { r = r + 2; } return r; }`, 7},
	// eq_arrays over a user Eq struct array. equal→+5, unequal→+2 → 7.
	{"eq-arrays", `pub trait Eq { function eq(self: Self, other: Self): boolean; }
struct P { v: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.v == other.v; } }
function eq_arrays[T: Eq](a: T[], b: T[]): boolean { var n = a.len(); if (n != b.len()) { return false; } var i = 0; while (i < n) { if (!a[i].eq(b[i])) { return false; } i = i + 1; } return true; }
function main(): i32 { var xs: P[] = [P { v: 1 }, P { v: 2 }]; var ys: P[] = [P { v: 1 }, P { v: 2 }]; var zs: P[] = [P { v: 1 }]; var r = 0; if (eq_arrays(xs, ys)) { r = r + 5; } if (!eq_arrays(xs, zs)) { r = r + 2; } return r; }`, 7},
}

// TestNativeCmpHelpers runs the inline Ord-helper programs on the native
// interp / x86-64 / wasm backends, oracle-checked.
func TestNativeCmpHelpers(t *testing.T) {
	for _, tc := range cmpHelperCases {
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

// TestNativeCmpHelpersArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeCmpHelpersArm64(t *testing.T) {
	for _, tc := range cmpHelperCases {
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

// TestNativeCmpModule exercises the shipped `import "core/cmp"` module: the
// generic helpers over the primitive `impl Ord for i32` AND a user Ord struct.
func TestNativeCmpModule(t *testing.T) {
	src := `import "core/cmp" as cmp;
struct P { v: i32 }
impl cmp.Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function main(): i32 {
    var a = cmp.min(8, 3);               // 3   (primitive impl Ord for i32)
    var b = cmp.max(8, 3);               // 8
    var c = cmp.clamp(15, 0, 10);        // 10
    var p = cmp.min(P { v: 5 }, P { v: 2 }); // P{v:2}
    var d = 0;
    if (cmp.lt(2, 9)) { d = 1; }         // 1
    var arr = cmp.sort([3, 1, 2]);       // [1,2,3]  (primitive impl Ord for i32)
    var srt = 0;
    if (cmp.is_sorted(arr)) { srt = 1; } // 1
    var ix = 0;
    match (cmp.index_of([10, 20, 30], 20)) { Some(v) => { ix = v; }, None => { ix = 0 - 1; } } // 1  (index_of -> Option[i32], #5348)
    var has = 0;
    if (cmp.contains([10, 20, 30], 30)) { has = 1; }                              // 1
    var eqa = 0;
    if (cmp.eq_arrays([1, 2], [1, 2])) { eqa = 1; }                               // 1
    return a + b + c + p.v + d + arr[0] * 100 + arr[2] + srt + ix + has + eqa;    // 128 + 1+1+1 = 131
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 131 {
		t.Errorf("cmp module interp = %d, want 131", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 131 {
		t.Errorf("cmp module x86-64 = %d, want 131", code)
	}
	if code := runWasm(t, src); code != 131 {
		t.Errorf("cmp module wasm = %d, want 131", code)
	}
}

// TestSelfHostCmpHelpersIRX86_64 routes each inline Ord-helper case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostCmpHelpersIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range cmpHelperCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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

// TestSelfHostCmpHelpersIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCmpHelpersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host cmp-helper wasm IR e2e")
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

	for _, tc := range cmpHelperCases {
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
			watFile := filepath.Join(dir, "cmp_helper_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("cmp-helper wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
