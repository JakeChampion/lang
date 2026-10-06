package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The struct/enum-ARRAY field release in __struct_drop_<T> and __field_reclaim_<T>
// is __fn___fern_arrarr_free, not an open-coded walk (#2649). Both emitters used to
// write the same ~24-instruction sequence inline — sole-owner gate, one
// __fern_arr_dec per element, then the buffer — which is exactly what that helper
// already is, so each backend carried two hand-written copies of a body it also
// exported as a symbol.
//
// The x86-64 leg pins BOTH halves, because only the pair is a contract: the call
// must be there, AND the inline walk must not have come back beside it. The walk
// is recognisable by its loop label (.Lstd_/.Lfr_), so its absence for the type
// under test is the erasure assertion.
//
// The behavioural half is not redundant with the shape half. The two forms differ
// in which guard answers a non-sole-owner: the walk asked __fern_rc_is_unique and
// then let a trailing __fern_arr_dec decrement, where the helper reads rc once and
// branches. A regression there frees a shared buffer's elements, which shows up as
// a corrupt read-back or an rc underflow rather than as a missing symbol.
var structDropArrArrFreeProg = `struct P { x: i32, y: i32 }
struct Bag { es: P[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
        acc = (acc + b.n + b.es.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 220) { return 98; }
    return 0;
}`

// A live second owner across the drop, and ELEMENT reads rather than a bare
// `.len()`. The reads are what keep __field_reclaim_Bag on its shallow arm — the
// "sarr:" admission wants .len()-only reads — so this program has the scope-exit
// call alone, and its subject is the helper's rc>1 arm: `c` still reads the
// elements a wrongly-taken sole-owner walk would free.
//
// `acc` is pinned exactly rather than bounded. A freed element box is rewritten
// by the next iteration's fresh literal, so a wrong release reads back plausible
// data and lands somewhere in range; only the exact value separates that from a
// correct run.
var structDropArrArrSharedProg = `struct P { x: i32, y: i32 }
struct Bag { es: P[], n: i32 }
function take(b: Bag): i32 { return b.es[0].x + b.es.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
        let c: Bag = b;
        acc = (acc + take(b) + take(c)) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 249) { return 97; }
    return 0;
}`

func TestSelfHostStructDropArrArrFreeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := string(runCapture(t, gcc, runner, driverBin, []byte(prog)))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// Scope-exit drop + rebind reclaim: __struct_drop_Bag and __field_reclaim_Bag
	// each release `es`, so both call sites are in one program.
	run(t, structDropArrArrFreeProg, "struct_drop_arrarr_free", 0)
	// A live second owner across the drop: the helper's rc>1 arm must decrement
	// without touching the elements `c` still reads.
	run(t, structDropArrArrSharedProg, "struct_drop_arrarr_free_shared", 0)
}

// TestSelfHostStructDropArrArrFreeIRArm64 runs both programs on the arm64
// census: the typed lowering releases the struct-array field through its own
// drop helper, so the proof here is the balance and the exact read-back.
func TestSelfHostStructDropArrArrFreeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, prog string }{
		{"struct_drop_arrarr_free_arm64", structDropArrArrFreeProg},
		{"struct_drop_arrarr_free_shared_arm64", structDropArrArrSharedProg},
	} {
		if code := arm64CensusRun(t, x86runner, driverBin, arm64gcc, qemu, tc.name, tc.prog); code != 0 {
			t.Errorf("%s exited %d, want 0", tc.name, code)
		}
	}
}
