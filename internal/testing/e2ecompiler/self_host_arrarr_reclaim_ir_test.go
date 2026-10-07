package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArrArrReclaimIRX86_64 pins #4355 slice 9: an arr-of-arr local
// (`let g = [[..], [..]]`) is released whole per iteration — the outer buffer,
// every inner buffer and every string element (__fern_arrarr_free for scalar
// rows, __fern_strarrarr_free for string rows) — so the churn loops stay flat.
// A row bound out of it (`let row = g[i]`) and a live string local stored as an
// element must survive intact, with no underflow.
func TestSelfHostArrArrReclaimIRX86_64(t *testing.T) {
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        acc = acc + g.len() + g[0][0].len();
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        acc = acc + g2.len() + g2[1][1].len();
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-str-flat", 0)

	// i32[][] churn with EXPRESSION inner elements (idents / binaries — value-
	// copied scalars, admitted by the lax rows-are-literals credit), flat.
	run(t, `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let g: i32[][] = [[i, i + 1], [i + 2]];
        acc = acc + g.len() + g[0][0];
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2: i32[][] = [[j, j + 1], [j + 2]];
        acc = acc + g2.len() + g2[1][0];
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-scalar-flat", 0)

	// ROW-ALIAS exclusion: `let row = g[1]` binds an inner buffer pointer, so
	// the candidate is rejected — row stays readable at detector zero (the
	// structure keeps its prior sound leak).
	run(t, `function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
        let row: string[] = g[1];
        if (row.len() != 2) { bad = 1; }
        if (row[0].len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-row-alias-safe", 0)

	// STRING-VAR inner element: `[[s1]]` stores a live local's pointer — the
	// strict "ARRARRS:" credit is withheld (string-kind slot, non-fresh
	// element), so nothing is freed and s1 survives at detector zero.
	run(t, `function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let s1: string = "aa" + "bb";
        let g: string[][] = [[s1], ["c" + "d"]];
        if (g[0][0].len() != 4) { bad = 1; }
        if (s1.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-ident-elem-safe", 0)
}
