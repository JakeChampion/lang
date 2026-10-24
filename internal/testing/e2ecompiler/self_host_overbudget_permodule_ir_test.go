package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostOverBudgetPerModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	const src = `import "std/array";
import "std/string";
function work(ss: string[]): i32 {
    let a: i32 = ss.len();
    let j: string = ss.join(",");
    let r: string[] = ss.reverse();
    let c: string[] = ss.concat(r);
    let acc: i32 = a + j.len() + r.len() + c.len();
    let i: i32 = 0;
    while (i < ss.len()) {
        acc = acc + ss[i].len() + ss[i].trim().len();
        if (ss[i].starts_with("x")) { acc = acc + 1; }
        if (ss[i].contains("y")) { acc = acc + 2; }
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let ss: string[] = ["ab", "cd", "ef"];
    let v: i32 = work(ss);
    if (v > 0) { return 0; }
    return 1;
}`
	prog := filepath.Join(dir, "overbudget_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}

	cntOut, err := exec.Command(mmc, prog, stdlibRoot, "-per-module-count").Output()
	if err == nil {
		if n, cerr := strconv.Atoi(strings.TrimSpace(string(cntOut))); cerr == nil && n < 2 {
			t.Fatalf("-per-module-count = %d, want >= 2 (multi-module program)", n)
		}
	}

	decideOut, err := exec.Command(mmc, prog, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("-decide failed: %v", err)
	}
	if got := strings.TrimSpace(string(decideOut)); got != "ir" {
		t.Fatalf("over-budget program routed %q, want \"ir\" (the per-module rescue must keep it IR-eligible)", got)
	}

	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget compile failed: %v (len=%d)", err, len(asm))
	}
	if !regexp.MustCompile(`(?m)^\.S[0-9]+:`).Match(asm) {
		t.Errorf("expected whole-program string labels (.S<idx>) — the merged IR path; " +
			"if this now routes the per-module concat, see #3457 on cross-unit shapes")
	}
	bin := buildBin(t, gcc, dir, "overbudget_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("over-budget program exited %d, want 0", code)
	}
}
