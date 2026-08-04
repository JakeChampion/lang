package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArrArrReclaimIRX86_64 pins #4355 slice 9: an arr-of-arr local
// (`var g = [[..], [..]]`) had NO reclaim at all on the self-host IR path —
// the init marks is_arrarr but the slot is not is_arr, so neither the exit
// sweep nor any rebind dec touched it: the outer buffer, every inner buffer,
// and every string element leaked per iteration (native is flat on the same
// shapes). The reclaim frees the WHOLE two-level structure via new runtime
// helpers modeled on __fn___fern_str_arr_free: __fern_arrarr_free (scalar
// inners — one rc-guarded arr_dec each) and __fern_strarrarr_free (string
// inners — __fern_str_arr_free each), routed by the slot's type-aware
// arrarr_elem kind. Admission: rows must be array LITERALS ("ARRARR:"), and
// string-kind inners additionally need every element to be a fresh string
// ("ARRARRS:"); a bare row read (`var row = g[i]`) or `for row in g` rejects
// the candidate (the row pointer would dangle).
func TestSelfHostArrArrReclaimIRX86_64(t *testing.T) {
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

	// string[][] churn — the slice target: rows + string elements all fresh,
	// whole structure freed per rebind, flat at detector zero.
	run(t, `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        acc = acc + g.len() + g[0][0].len();
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        acc = acc + g2.len() + g2[1][1].len();
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-str-flat", 0)

	// i32[][] churn with EXPRESSION inner elements (idents / binaries — value-
	// copied scalars, admitted by the lax rows-are-literals credit), flat.
	run(t, `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: i32[][] = [[i, i + 1], [i + 2]];
        acc = acc + g.len() + g[0][0];
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: i32[][] = [[j, j + 1], [j + 2]];
        acc = acc + g2.len() + g2[1][0];
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-scalar-flat", 0)

	// ROW-ALIAS exclusion: `var row = g[1]` binds an inner buffer pointer, so
	// the candidate is rejected — row stays readable at detector zero (the
	// structure keeps its prior sound leak).
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        var row: string[] = g[1];
        if (row.len() != 2) { bad = 1; }
        if (row[0].len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-row-alias-safe", 0)

	// STRING-VAR inner element: `[[s1]]` stores a live local's pointer — the
	// strict "ARRARRS:" credit is withheld (string-kind slot, non-fresh
	// element), so nothing is freed and s1 survives at detector zero.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s1: string = "aa" + "bb";
        var g: string[][] = [[s1], ["c" + "d"]];
        if (g[0][0].len() != 4) { bad = 1; }
        if (s1.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-ident-elem-safe", 0)
}
