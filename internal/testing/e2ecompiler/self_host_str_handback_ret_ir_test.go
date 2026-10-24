package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStrHandbackRetIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (99 = double-free/over-release; 98 = leak; 97 = result corrupted; 88 = arg source freed)", name, code, want)
		}
	}

	method := `function (s: string) maybe(k: i32): string { if (k == 0) { return s; } return s + "!"; }
`

	run(t, method+`function idret(x: string): string { return x.maybe(0); }
function main(): i32 {
    let base: string = "payload";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let got: string = idret(base + "-x");
        if (got.len() != 9) { bad = 1; }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2: string = idret(base + "-x");
        if (g2.len() != 9) { bad = 1; }
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 97; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "identity-return-arg-flat", 0)

	run(t, method+`function idret(x: string): string { return x.maybe(0); }
function main(): i32 {
    let base: string = "payload";
    let i: i32 = 0;
    while (i < 200) { idret(base + "-x"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { idret(base + "-x"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "identity-return-discarded-flat", 0)

	run(t, method+`function freshret(x: string): string { return x.maybe(1); }
function main(): i32 {
    let base: string = "payload";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let got: string = freshret(base + "-x");
        if (got.len() != 10) { bad = 1; }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2: string = freshret(base + "-x");
        if (g2.len() != 10) { bad = 1; }
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 97; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "fresh-return-arg-flat", 0)

	run(t, method+`function idret(x: string): string { return x.maybe(0); }
function wrap(y: string): string { return idret(y); }
function main(): i32 {
    let base: string = "payload";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let got: string = wrap(base + "-x");
        if (got.len() != 9) { bad = 1; }
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) {
        let g2: string = wrap(base + "-x");
        if (g2.len() != 9) { bad = 1; }
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 97; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "transitive-handback-arg-flat", 0)
}
