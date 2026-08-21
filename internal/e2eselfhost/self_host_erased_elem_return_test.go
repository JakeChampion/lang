package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `first_of[T](xs: T[]): T` — an erased ARRAY param with a BARE erased return —
// silently miscompiled on the self-host x86-64 backend (#6273). The element
// stride is not the problem there; every register slot is 8 bytes. The problem
// is that the CALL SITE does not know what came back: an erased `T` result
// reaches a `.len()` as an ARRAY receiver, so `first_of(strs).len()` loaded the
// array length at offset 0 instead of the string length at offset 8 and
// returned 0. f64 came back 255 and i64 came back 0 for the mirror-image
// reason; only i32 was accidentally right.
//
// This is why the cases assert VALUES rather than routing. Every one of them
// routed `ir`, exited 0, and reported nothing under FERN_STRICT_IR=1 — the
// path probe cannot see this class at all. The struct case is here because
// promotion is what makes it lower and run rather than being refused.
//
// Neither half alone is broken (`count_of[T](xs: T[]): i32` and the #5586
// pass-through `id_of[T](x: T): T` were both always correct), so the two
// controls are what pin the fault to the combination.
//
// The tuple-return rows are the same defect in a different return spelling —
// `(T, T)` and `(T, i32)`. They are here rather than in a file of their own
// because a fix that only handles the spellings it was shown is how this bug
// got fixed twice already; the promotion now triggers on any mention of the
// var in the return, and these are what hold it to that.
var erasedElemReturnCases = []struct {
	name string
	src  string
}{
	{"first_f64", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var xs: f64[] = [4.5, 1.5];
    return (first_of(xs) * 10.0) as i32;
}`}, // 45; was 255
	{"first_i64", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var xs: i64[] = [9000000000, 1];
    return (first_of(xs) / 1000000000) as i32 + 36;
}`}, // 45; was 36 (the element read back as 0)
	{"first_string", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var xs: string[] = ["abcde"];
    return first_of(xs).len() + 40;
}`}, // 45; was 40 — len() on a string box read the ARRAY length slot
	{"first_i32", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var xs: i32[] = [4, 1];
    return first_of(xs) * 10 + 5;
}`}, // 45; the one width that was already correct — keep it correct
	{"first_struct", `struct P { a: i32 }
function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var xs: P[] = [P { a: 4 }, P { a: 1 }];
    return first_of(xs).a * 10 + 5;
}`}, // 45; used to be refused on both self-host backends
	{"pair_of_f64", `function pair_of[T](xs: T[]): (T, T) { return (xs[0], xs[1]); }
function main(): i32 {
    var xs: f64[] = [4.5, 1.5];
    var (a, b) = pair_of(xs);
    return ((a + b) * 10.0) as i32;
}`}, // 60; was 0 — a bare TUPLE of the typevar is the same defect in another return spelling
	{"pair_of_string", `function pair_of[T](xs: T[]): (T, T) { return (xs[0], xs[1]); }
function main(): i32 {
    var xs: string[] = ["ab", "cde"];
    var (a, b) = pair_of(xs);
    return a.len() + b.len() + 40;
}`}, // 45; was 42
	{"mixed_tuple_return", `function head_and_one[T](xs: T[]): (T, i32) { return (xs[0], 1); }
function main(): i32 {
    var xs: f64[] = [4.4];
    var (a, b) = head_and_one(xs);
    return (a * 10.0) as i32 + b;
}`}, // 45; was 0
	{"receiver_typevar_control", `struct Pair { a: i32 }
function (xs: T[]) firstpair[U](other: U[]): (T, U) { return (xs[0], other[0]); }
function main(): i32 {
    var a: f64[] = [4.5];
    var b: i32[] = [1];
    var (x, y) = a.firstpair(b);
    return (x * 10.0) as i32 + y - 1;
}`}, // 45 — a METHOD must NOT be promoted: its receiver's `T` is not in
	// type_params, so `all_tp_count` reads 1 while `T` is right there in the
	// return, and promoting `U` alone would strand `T` erased in the clone.
	// `array.zip`'s method form is exactly this and fell off the IR path.
	{"count_of_control", `function count_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    var xs: f64[] = [4.5, 1.5];
    return count_of(xs) * 20 + 5;
}`}, // 45 — erased array param, NON-erased return: always was correct
	{"id_of_control", `function id_of[T](x: T): T { return x; }
function main(): i32 {
    var xs: f64[] = [4.5, 1.5];
    return (id_of(xs[0]) * 10.0) as i32;
}`}, // 45 — bare T param and return, no array: the #5586 pass-through
}

// TestSelfHostErasedElemReturnX86_64 runs each case through the self-host
// x86-64 backend against the interp oracle. This is the leg the bug was on.
func TestSelfHostErasedElemReturnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedElemReturnCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			var rcmd *exec.Cmd
			if len(runner) == 0 {
				rcmd = exec.Command(binPath)
			} else {
				rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
			}
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — an erased T[] param with a bare T return must monomorphise so the call site knows the result type", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostErasedElemReturnWasm is the wasm leg. Before the promotion the
// wide-element cases were REFUSED here by the erased-wide gate; now they lower
// and must produce the oracle's value. `count_of_control` was refused for a
// separate reason (the gate did not ask whether the element was ever read) and
// is covered from that angle by TestSelfHostErasedWideArrayGateBlindWasm.
func TestSelfHostErasedElemReturnWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-element-return wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedElemReturnCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
