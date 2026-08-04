package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArrArrCallRetReclaimIRX86_64 pins #4355 slice 10: an arr-of-arr
// local initialised from a CALL (`var g: T[][] = mk(..)`) earns the same
// ARRARR:/ARRARRS: credits the literal it returns would — opt_fresh_ret_fns_of
// registers "AAC:<name>|<flag>" for FREE functions whose every return is a
// fresh arr-of-arr literal (arrarr_lit_is_fresh; flag "s" when every return is
// also strings-fresh), and collect_fresh_arrarr_names admits the call init off
// that registry. The self-host IR path has no return-transfer inc, so the call
// result is rc=1 and solely owned by the caller.
//
// Also pins the slice-9 double-sweep fix: an arrarr slot is ALSO is_arr, and
// the exit sweep used to free it twice (a shallow arr_dec zeroed the rc, then
// the separate arrarr loop's helper saw rc==0, ticked the underflow detector,
// and skipped its element walk — inner strings leaked at function scope). The
// whole-structure helper now runs INSIDE the is_arr sweep (one slot, one
// free); the fn-scope-sweep case would exit 99 (detector) or 98 (leak) on the
// old code.
func TestSelfHostArrArrCallRetReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = structure leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// string[][] CALL-INIT churn — the slice target: mk's every return is a
	// strings-fresh arrarr literal, so g2 earns the strict credit and the
	// whole structure is freed per rebind. Flat at detector zero.
	run(t, `function mk(i: i32): string[][] {
    return [["a" + "b"], ["c" + "d", "e" + "f"]];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: string[][] = mk(i);
        acc = acc + g.len() + g[0][0].len();
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: string[][] = mk(j);
        acc = acc + g2.len() + g2[1][1].len();
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-callret-str-flat", 0)

	// i32[][] CALL-INIT churn with expression rows — lax credit suffices
	// (scalar inners value-copy). Flat.
	run(t, `function mks(i: i32): i32[][] {
    return [[i, i + 1], [i + 2]];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: i32[][] = mks(i);
        acc = acc + g.len() + g[0][0];
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: i32[][] = mks(j);
        acc = acc + g2.len() + g2[1][0];
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-callret-scalar-flat", 0)

	// PARAM-EMBEDDING exclusion: mk2 stores its string param as a row element
	// (`[[s]]`), so its "AAC:" entry carries flag "p" only — the string-kind
	// consumer slot gets no strict credit, nothing is freed, and the caller's
	// live s1 survives at detector zero.
	run(t, `function mk2(s: string): string[][] {
    return [[s], ["c" + "d"]];
}
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s1: string = "aa" + "bb";
        var g: string[][] = mk2(s1);
        if (g[0][0].len() != 4) { bad = 1; }
        if (s1.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-callret-param-embed-safe", 0)

	// LOCAL-RETURN exclusion: mk3 returns a local (`return t;`), not a
	// literal — it never registers, the call init earns no credit, and the
	// structure keeps its prior sound leak (correct reads, detector zero).
	run(t, `function mk3(i: i32): string[][] {
    var t: string[][] = [["a" + "b"], ["c" + "d"]];
    return t;
}
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        var g: string[][] = mk3(i);
        if (g[0][0].len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-callret-local-ret-safe", 0)

	// FN-SCOPE exit sweep, one free per slot (the slice-9 double-sweep fix):
	// each work() call sweeps its fn-scope literal arrarr on return — the
	// whole-structure helper must run exactly once (old code: shallow arr_dec
	// then a second helper call → 2200 underflow ticks → 99, inner strings
	// leaked → 98).
	run(t, `function work(n: i32): i32 {
    var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
    return g.len() + g[0][0].len() + n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + work(j); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-fnscope-sweep-flat", 0)
}
