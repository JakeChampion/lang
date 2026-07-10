package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// core/cmp's generic array reductions `max_of[T: Ord](xs) -> Option[T]` and
// `min_of` — the any-`Ord`-type replacement for std/array's per-width
// `max_i64` / `min_i64` / … zoo (#4387 item 2). Like the shipped `sort` /
// `is_sorted`, the `T: Ord` bound monomorphises `.cmp` to a direct call per
// element type, so the body lowers on the native backends AND the self-host IR
// path. Empty -> None; ties keep the first extremum (strict `> 0` / `< 0`).
//
// Each inline case bundles a minimal `Ord` trait + `impl Ord for i32` + the
// two reducers so the single-program self-host driver (which resolves no
// imports) can compile it, then encodes the result as a small exit code.
var maxOfCases = []struct {
	name string
	main string
	want int
}{
	// max of [3,9,1,7,4] is 9; min is 1; 9*10 + 1 = 91.
	{"i32-max-min", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function max_of[T: Ord](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (xs[i].cmp(best) > 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function min_of[T: Ord](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (xs[i].cmp(best) < 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function unwrap(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0 - 1; } } }
function main(): i32 { var a: i32[] = [3, 9, 1, 7, 4]; return unwrap(max_of(a)) * 10 + unwrap(min_of(a)); }`, 91},
	// empty array -> None for both; encode None as 5 (2 for max + 3 for min).
	{"empty-none", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function max_of[T: Ord](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (xs[i].cmp(best) > 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function min_of[T: Ord](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (xs[i].cmp(best) < 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function main(): i32 { var e: i32[] = []; var r = 0; match (max_of(e)) { Some(v) => {}, None => { r = r + 2; } } match (min_of(e)) { Some(v) => {}, None => { r = r + 3; } } return r; }`, 5},
	// singleton -> that element is both max and min; 42 via max.
	{"singleton", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function max_of[T: Ord](xs: T[]): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var i: i32 = 1; while (i < xs.len()) { if (xs[i].cmp(best) > 0) { best = xs[i]; } i = i + 1; } return Some(best); }
function unwrap(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0 - 1; } } }
function main(): i32 { var s: i32[] = [42]; return unwrap(max_of(s)); }`, 42},
}

// TestNativeCmpMaxOf runs the inline max_of/min_of programs on interp / x86-64 /
// wasm, oracle-checked.
func TestNativeCmpMaxOf(t *testing.T) {
	for _, tc := range maxOfCases {
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

// TestNativeCmpMaxOfModule exercises the shipped `import "core/cmp"` bodies over
// the primitive `impl Ord` (i32 / string) and a user `Ord` struct.
func TestNativeCmpMaxOfModule(t *testing.T) {
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
    var a: i32[] = [3, 9, 1, 7, 4];
    match (cmp.max_of(a)) { Some(v) => { if (v == 9) { r = r + 1; } }, None => {} }
    match (cmp.min_of(a)) { Some(v) => { if (v == 1) { r = r + 2; } }, None => {} }
    var ss: string[] = ["pear", "apple", "cherry"];
    match (cmp.max_of(ss)) { Some(v) => { if (v == "pear") { r = r + 4; } }, None => {} }
    match (cmp.min_of(ss)) { Some(v) => { if (v == "apple") { r = r + 8; } }, None => {} }
    var ps: P[] = [P { v: 2 }, P { v: 5 }, P { v: 1 }];
    match (cmp.max_of(ps)) { Some(v) => { if (v.v == 5) { r = r + 16; } }, None => {} }
    match (cmp.min_of(ps)) { Some(v) => { if (v.v == 1) { r = r + 32; } }, None => {} }
    var e: i32[] = [];
    match (cmp.max_of(e)) { Some(v) => {}, None => { r = r + 64; } }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 127 {
		t.Errorf("max_of module interp = %d, want 127", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 127 {
		t.Errorf("max_of module x86-64 = %d, want 127", code)
	}
	if code := runWasm(t, src); code != 127 {
		t.Errorf("max_of module wasm = %d, want 127", code)
	}
}

// TestSelfHostCmpMaxOfIRX86_64 drives the inline cases through the self-hosted
// x86-64 compiler (asm_run), oracle-checking the exit code — pinning that the
// generic Ord-bounded reducers lower + run on the self-host IR path.
func TestSelfHostCmpMaxOfIRX86_64(t *testing.T) {
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

	for _, tc := range maxOfCases {
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
				t.Errorf("%s self-host x86-64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
