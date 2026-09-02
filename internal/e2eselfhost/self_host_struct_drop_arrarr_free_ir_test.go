package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The struct/enum-ARRAY field release in __struct_drop_<T> and __field_reclaim_<T>
// is __fn___fern_arrarr_free, not an open-coded walk (#2649). Both emitters used to
// write the same ~24-instruction sequence inline — sole-owner gate, one
// __fern_arr_dec per element, then the buffer — which is exactly what that helper
// already is, so each backend carried two hand-written copies of a body it also
// exported as a symbol.
//
// These pin BOTH halves, because only the pair is a contract: the call must be
// there, AND the inline walk must not have come back beside it. The walk is
// recognisable by its loop label (.Lstd_/.Lfr_ on x86-64, .Lasd_/.Lafr_ on arm64),
// so its absence for the type under test is the erasure assertion.
//
// The behavioural half is not redundant with the shape half. The two forms differ
// in which guard answers a non-sole-owner: the walk asked __fern_rc_is_unique and
// then let a trailing __fern_arr_dec decrement, where the helper reads rc once and
// branches. A regression there frees a shared buffer's elements, which shows up as
// a corrupt read-back or an rc underflow rather than as a missing symbol.
var structDropArrArrFreeProg = `struct P { x: i32, y: i32 }
struct Bag { es: P[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
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
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
        var c: Bag = b;
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
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantCalls int) {
		t.Helper()
		asm := string(runCapture(t, gcc, runner, driverBin, []byte(prog)))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if got := strings.Count(asm, "call __fn___fern_arrarr_free"); got != wantCalls {
			t.Errorf("%s: %d `call __fn___fern_arrarr_free`, want %d — the struct-array field release is not going through the shared helper", name, got, wantCalls)
		}
		for _, walk := range []string{".Lstd_Bag_loop", ".Lfr_Bag_loop"} {
			if strings.Contains(asm, walk) {
				t.Errorf("%s: emitted asm still has %s — the open-coded element walk came back beside the helper call", name, walk)
			}
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
	run(t, structDropArrArrFreeProg, "struct_drop_arrarr_free", 0, 2)
	// A live second owner across the drop: the helper's rc>1 arm must decrement
	// without touching the elements `c` still reads.
	run(t, structDropArrArrSharedProg, "struct_drop_arrarr_free_shared", 0, 1)
}

func TestSelfHostStructDropArrArrFreeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantCalls int) {
		t.Helper()
		asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		if got := strings.Count(asm, "bl __fn___fern_arrarr_free"); got != wantCalls {
			t.Errorf("%s: %d `bl __fn___fern_arrarr_free`, want %d — the struct-array field release is not going through the shared helper", name, got, wantCalls)
		}
		for _, walk := range []string{".Lasd_Bag_loop", ".Lafr_Bag_loop"} {
			if strings.Contains(asm, walk) {
				t.Errorf("%s: emitted arm64 asm still has %s — the open-coded element walk came back beside the helper call", name, walk)
			}
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, asm)
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, structDropArrArrFreeProg, "struct_drop_arrarr_free_arm64", 0, 2)
	run(t, structDropArrArrSharedProg, "struct_drop_arrarr_free_shared_arm64", 0, 1)
}
