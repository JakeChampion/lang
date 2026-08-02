package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// iterPrelude mirrors internal/stdlib/core/iter.fern (the numeric Iterator
// trait + integer Range + eager drivers), inlined as a self-contained program.
// The self-host IR tests inline it (the self-host core tests inline the stdlib
// source rather than importing — see self_host_core_int_parse_ir_test.go); the
// native side ALSO runs this inlined form so both backends exercise byte-for-
// byte the same trait shape; a separate native test below exercises the real
// `import "core/iter"` module.
const iterPrelude = `pub trait Iterator { function next(self: Self): Option[(i32, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function sum[I: Iterator](it: I): i32 {
    var total = 0; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { total = total + t.0; cur = t.1; }, None => { go = false; }, } }
    return total;
}
pub function count[I: Iterator](it: I): i32 {
    var n = 0; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { n = n + 1; cur = t.1; }, None => { go = false; }, } }
    return n;
}
pub function to_array[I: Iterator](it: I): i32[] {
    var out: i32[] = []; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { out = out.append(t.0); cur = t.1; }, None => { go = false; }, } }
    return out;
}
`

var iterTraitCases = []struct {
	name string
	main string
	want int
}{
	// sum over [0,5) = 0+1+2+3+4 = 10
	{"sum-range", `function main(): i32 { return sum(range(0, 5)); }`, 10},
	// count of [2,9) = 7
	{"count-range", `function main(): i32 { return count(range(2, 9)); }`, 7},
	// to_array([0,4)) = [0,1,2,3]; sum-via-for 6 + len 4 = 10
	{"to-array", `function main(): i32 { var xs = to_array(range(0, 4)); var t = 0; for x in xs { t = t + x; } return t + xs.len(); }`, 10},
	// empty ranges yield nothing: sum 0 + count 0 + 9 = 9
	{"empty", `function main(): i32 { return sum(range(7, 7)) + count(range(3, 3)) + 9; }`, 9},
	// two drivers over two fresh ranges in one program (two monomorphic call sites)
	{"compose", `function main(): i32 { return sum(range(0, 5)) + count(range(0, 7)); }`, 17},
}

func iterProg(mainBody string) string { return iterPrelude + mainBody + "\n" }

// writeIterProg writes a program to a temp .fern file and returns its path —
// the fixture helpers (runFixture*) take a path and run the FULL pre-codegen
// pipeline (modload + constfold + checker + monomorph), which the bounded-
// generic drivers require (the lighter compileAndRunX86_64 skips monomorph).
func writeIterProg(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	return p
}

// TestNativeIteratorTrait runs the inlined numeric-Iterator-trait programs
// through the native interpreter + x86-64 + wasm backends, oracle-checked.
func TestNativeIteratorTrait(t *testing.T) {
	for _, tc := range iterTraitCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIteratorTraitArm64 is the arm64 leg (CI-gated; runs under qemu).
func TestNativeIteratorTraitArm64(t *testing.T) {
	for _, tc := range iterTraitCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIteratorTraitModule exercises the REAL `import "core/iter"` module
// (not the inlined form) so the shipped stdlib module is validated end-to-end.
func TestNativeIteratorTraitModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var s = iter.sum(iter.range(0, 5));            // 10
    var c = iter.count(iter.range(2, 9));          // 7
    var xs = iter.to_array(iter.range(0, 4));      // [0,1,2,3]
    var t = 0;
    for x in xs { t = t + x; }                     // 6
    return s + c + t + xs.len();                   // 10+7+6+4 = 27
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 27 {
		t.Errorf("module interp = %d, want 27", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 27 {
		t.Errorf("module x86-64 = %d, want 27", code)
	}
	if code := runWasm(t, src); code != 27 {
		t.Errorf("module wasm = %d, want 27", code)
	}
}

// TestNativeIteratorTraitModuleAdapters exercises the shipped module's
// closure-free adapters (nth / min / max / product / last / position /
// contains / count_value) end-to-end.
func TestNativeIteratorTraitModuleAdapters(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var a = 0;
    match (iter.nth(iter.range(0, 9), 4)) { Some(v) => { a = v; }, None => {} }   // 4
    var c = 0;
    match (iter.min(iter.range(3, 7))) { Some(v) => { c = v; }, None => {} }       // 3
    var d = 0;
    match (iter.max(iter.range(3, 7))) { Some(v) => { d = v; }, None => {} }       // 6
    var e = iter.product(iter.range(1, 5));                                        // 24
    var f = 0;
    match (iter.last(iter.range(0, 5))) { Some(v) => { f = v; }, None => {} }       // 4
    var g = 0;
    match (iter.position(iter.range(0, 9), 7)) { Some(v) => { g = v; }, None => {} } // 7
    var h = 0;
    if (iter.contains(iter.range(0, 5), 3)) { h = 1; }                             // 1
    var k = iter.count_value(iter.range(0, 5), 2);                                 // 1
    return a + c + d + e + f + g + h + k;                                         // 48+1+1 = 50
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 50 {
		t.Errorf("adapters interp = %d, want 50", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 50 {
		t.Errorf("adapters x86-64 = %d, want 50", code)
	}
	if code := runWasm(t, src); code != 50 {
		t.Errorf("adapters wasm = %d, want 50", code)
	}
}

// TestNativeIteratorTraitModuleGeneric exercises the GENERIC face of the
// shipped `core/iter` module: a user type that implements `iter.Iterator[T]`
// for a non-i32 element type (`boolean`) and drives the module's generic
// `count` / `to_array` over it. This proves the stdlib trait is genuinely
// generic (#2691), not the old i32-only shape — `count`/`to_array` infer the
// element type from the impl (bound-driven inference, #3596).
func TestNativeIteratorTraitModuleGeneric(t *testing.T) {
	src := `import "core/iter" as iter;
struct BoolSeq { n: i32 }
impl iter.Iterator[boolean] for BoolSeq { function next(self: Self): Option[(boolean, Self)] { if (self.n <= 0) { return None; } return Some((true, BoolSeq { n: self.n - 1 })); } }
function main(): i32 {
    var c = iter.count(BoolSeq { n: 3 });           // 3
    var bs = iter.to_array(BoolSeq { n: 2 });        // [true, true]
    var k = 0;
    for b in bs { if (b) { k = k + 1; } }            // 2
    var f = iter.fold(iter.range(1, 5), 1, function (a: i32, x: i32): i32 { return a * x; });  // 1*1*2*3*4 = 24
    return c + k + bs.len() + f - 24;                // 3+2+2+24-24 = 7
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 7 {
		t.Errorf("generic module interp = %d, want 7", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 7 {
		t.Errorf("generic module x86-64 = %d, want 7", code)
	}
	if code := runWasm(t, src); code != 7 {
		t.Errorf("generic module wasm = %d, want 7", code)
	}
}

// TestNativeIteratorTraitModulePredicates exercises the shipped module's
// closure-taking predicate adapters (any / all / find) over the real
// `import "core/iter"` module. These take a `(T) => boolean` through a
// function-typed parameter; the self-host IR lowering of the inline forms is
// pinned by generic_iterator_predicate_test.go (the #2686 condition-lift fix),
// so this leg validates the shipped module on the native backends.
func TestNativeIteratorTraitModulePredicates(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var a = 0;
    if (iter.any(iter.range(0, 5), function (x: i32): boolean { return x == 3; })) { a = 1; }      // 1
    var b = 0;
    if (!iter.any(iter.range(0, 5), function (x: i32): boolean { return x > 9; })) { b = 2; }       // 2
    var c = 0;
    if (iter.all(iter.range(0, 5), function (x: i32): boolean { return x < 10; })) { c = 4; }       // 4
    var d = 0;
    if (!iter.all(iter.range(0, 5), function (x: i32): boolean { return x % 2 == 0; })) { d = 8; }   // 8
    var e = 0;
    match (iter.find(iter.range(0, 9), function (x: i32): boolean { return x >= 2 && x % 2 == 0; })) { Some(v) => { e = v; }, None => {} }  // 2
    var f = 0;
    match (iter.find(iter.range(0, 3), function (x: i32): boolean { return x > 100; })) { Some(v) => { f = v; }, None => { f = 16; } }      // 16
    return a + b + c + d + e + f;                                                                   // 1+2+4+8+2+16 = 33
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 33 {
		t.Errorf("predicate adapters module interp = %d, want 33", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 33 {
		t.Errorf("predicate adapters module x86-64 = %d, want 33", code)
	}
	if code := runWasm(t, src); code != 33 {
		t.Errorf("predicate adapters module wasm = %d, want 33", code)
	}
}

// TestSelfHostIteratorTraitIRX86_64 routes each inlined case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks the
// exit code.
func TestSelfHostIteratorTraitIRX86_64(t *testing.T) {
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

	for _, tc := range iterTraitCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterProg(tc.main))
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

// TestSelfHostIteratorTraitIRWasm runs the same inlined cases through the wasm
// IR backend.
func TestSelfHostIteratorTraitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iterator-trait wasm IR e2e")
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

	for _, tc := range iterTraitCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterProg(tc.main))
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
			watFile := filepath.Join(dir, "iter_trait_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iterator-trait wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
