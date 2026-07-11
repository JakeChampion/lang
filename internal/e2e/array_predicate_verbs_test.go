package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/array's predicate-driven verbs `position[T](xs, pred) -> Option[i32]`
// (index of first match — the predicate sibling of `index_of`), and
// `take_while[T]` / `drop_while[T]` (the predicate siblings of the count-based
// `take` / `drop`), from #4416's stdlib-ergonomics grab-bag. Each is a closure
// verb like `find` / `filter`, so it lowers on every native backend and the
// self-host IR path. Empty / no-match / all-match edges are pinned.
//
// Each inline case bundles the minimal bodies (the single-program self-host
// driver resolves no imports) and encodes the result as a small exit code.
var predVerbCases = []struct {
	name string
	main string
	want int
}{
	// position: first even in [1,3,4,6,7] is at index 2.
	{"position", `function position[T](xs: T[], pred: (T) => boolean): Option[i32] { var i: i32 = 0; while (i < xs.len()) { if (pred(xs[i])) { return Some(i); } i = i + 1; } return None; }
function is_even(x: i32): boolean { return x % 2 == 0; }
function main(): i32 { var a: i32[] = [1, 3, 4, 6, 7]; match (position(a, is_even)) { Some(i) => { return i; }, None => { return 0 - 1; } } }`, 2},
	// position with no match -> None; encode None as 9.
	{"position-none", `function position[T](xs: T[], pred: (T) => boolean): Option[i32] { var i: i32 = 0; while (i < xs.len()) { if (pred(xs[i])) { return Some(i); } i = i + 1; } return None; }
function is_even(x: i32): boolean { return x % 2 == 0; }
function main(): i32 { var a: i32[] = [1, 3, 5]; match (position(a, is_even)) { Some(i) => { return i; }, None => { return 9; } } }`, 9},
	// take_while lt5 on [2,4,1,6,3] -> [2,4,1]; len 3, first+last of prefix = 2+1 = 3; 3*10+3 = 33.
	{"take-while", `function take_while[T](xs: T[], pred: (T) => boolean): T[] { var out: T[] = []; var i: i32 = 0; while (i < xs.len() && pred(xs[i])) { out = out.append(xs[i]); i = i + 1; } return out; }
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 { var b: i32[] = [2, 4, 1, 6, 3]; var tw = take_while(b, lt5); return tw.len() * 10 + tw[0] + tw[2]; }`, 33},
	// drop_while lt5 on [2,4,1,6,3] -> [6,3]; len 2, elems 6+3 = 9; 2*10+9 = 29.
	{"drop-while", `function drop_while[T](xs: T[], pred: (T) => boolean): T[] { var i: i32 = 0; while (i < xs.len() && pred(xs[i])) { i = i + 1; } var out: T[] = []; while (i < xs.len()) { out = out.append(xs[i]); i = i + 1; } return out; }
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 { var b: i32[] = [2, 4, 1, 6, 3]; var dw = drop_while(b, lt5); return dw.len() * 10 + dw[0] + dw[1]; }`, 29},
	// The natural inline chain `take_while(b, p).len()` — miscompiled to a
	// signal crash on the self-host IR path until #4767 (the lift pass never
	// reached a fn-arg call in method-receiver position); these edge and chain
	// cases were native-only until that fix and now run everywhere.
	{"take-while-chained-len", `function take_while[T](xs: T[], pred: (T) => boolean): T[] { var out: T[] = []; var i: i32 = 0; while (i < xs.len() && pred(xs[i])) { out = out.append(xs[i]); i = i + 1; } return out; }
function drop_while[T](xs: T[], pred: (T) => boolean): T[] { var i: i32 = 0; while (i < xs.len() && pred(xs[i])) { i = i + 1; } var out: T[] = []; while (i < xs.len()) { out = out.append(xs[i]); i = i + 1; } return out; }
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 { var b: i32[] = [2, 4, 1, 6, 3]; return take_while(b, lt5).len() * 10 + drop_while(b, lt5).len(); }`, 32},
	// take_while: first element fails -> empty (0); all pass -> full (3).
	{"take-while-edges", `function take_while[T](xs: T[], pred: (T) => boolean): T[] { var out: T[] = []; var i: i32 = 0; while (i < xs.len() && pred(xs[i])) { out = out.append(xs[i]); i = i + 1; } return out; }
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 { var c: i32[] = [9, 1, 2]; var d: i32[] = [1, 2, 3]; var e = take_while(c, lt5); var f = take_while(d, lt5); var r = 0; if (e.len() == 0) { r = r + 1; } if (f.len() == 3) { r = r + 2; } return r; }`, 3},
	// drop_while: first element fails -> full (3); all pass -> empty (0).
	{"drop-while-edges", `function drop_while[T](xs: T[], pred: (T) => boolean): T[] { var i: i32 = 0; while (i < xs.len() && pred(xs[i])) { i = i + 1; } var out: T[] = []; while (i < xs.len()) { out = out.append(xs[i]); i = i + 1; } return out; }
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 { var c: i32[] = [9, 1, 2]; var d: i32[] = [1, 2, 3]; var e = drop_while(c, lt5); var f = drop_while(d, lt5); var r = 0; if (e.len() == 3) { r = r + 1; } if (f.len() == 0) { r = r + 2; } return r; }`, 3},
}

// TestNativeArrayPredicateVerbs runs the inline programs on interp / x86-64 / wasm.
func TestNativeArrayPredicateVerbs(t *testing.T) {
	for _, tc := range predVerbCases {
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

// TestNativeArrayPredicateVerbsModule exercises the shipped `import "std/array"`
// bodies, incl. the take_while ++ drop_while == original complement.
func TestNativeArrayPredicateVerbsModule(t *testing.T) {
	src := `import "std/array" as arr;
function is_even(x: i32): boolean { return x % 2 == 0; }
function lt5(x: i32): boolean { return x < 5; }
function main(): i32 {
    var r = 0;
    var a: i32[] = [1, 3, 4, 6, 7];
    match (arr.position(a, is_even)) { Some(i) => { if (i == 2) { r = r + 1; } }, None => {} }
    match (arr.position([1, 3, 5], is_even)) { Some(i) => {}, None => { r = r + 2; } }
    var b: i32[] = [2, 4, 1, 6, 3];
    var tw = arr.take_while(b, lt5);
    var dw = arr.drop_while(b, lt5);
    if (tw.len() == 3 && dw.len() == 2 && tw.len() + dw.len() == b.len()) { r = r + 4; }
    if (arr.take_while(b, lt5)[0] == 2 && arr.drop_while(b, lt5)[0] == 6) { r = r + 8; }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 15 {
		t.Errorf("pred-verbs module interp = %d, want 15", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 15 {
		t.Errorf("pred-verbs module x86-64 = %d, want 15", code)
	}
	if code := runWasm(t, src); code != 15 {
		t.Errorf("pred-verbs module wasm = %d, want 15", code)
	}
}

// TestSelfHostArrayPredicateVerbsIRX86_64 drives the inline cases through the
// self-hosted x86-64 compiler (asm_run), oracle-checking the exit code.
func TestSelfHostArrayPredicateVerbsIRX86_64(t *testing.T) {
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

	for _, tc := range predVerbCases {
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
