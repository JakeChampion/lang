package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostOverBudgetPerModuleIR pins that a program importing a large slice
// of stdlib — enough that the RAW merged module is far past the 512-function IR
// budget, which used to drop the WHOLE program to the legacy AST emitter — still
// reaches the IR path (#3457).
//
// MEASURED, and not what the name suggests: this does NOT exercise
// asm_load_run.emit_per_module_concat. That driver treeshakes its merged module
// in place BEFORE the concat's size gate is consulted, and the live closure here
// is 37 functions (`asm_load_run <prog> <stdlib> -ir-probe | wc -l`) — far under
// 512 — so the ordinary MERGED IR path compiles it and the concat is skipped.
// The `.S<idx>` assertion below is what states that: whole-program string labels,
// not the per-unit `.S<ns>_<idx>` pools a per-module unit emits.
//
// So this is a genuine regression test for the treeshake-then-merged-IR route,
// and NOT coverage of the per-module concat, which remains unexercised: entering
// it needs a program with a >512-function LIVE closure, and nothing in the suite
// has one. Do not rely on this as proof the concat works — it has a known
// cross-unit shape defect (units emit `.weak __fern_shp_*` for the LINKER to
// merge across object files, which a single-file concat cannot carry). See #3457.
//
// Native only: the file-loading driver reads stdlib by host path from argv.
func TestSelfHostOverBudgetPerModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// Many distinct std/array + std/string methods, so the RAW merged module is
	// comfortably past 512 functions while every reachable helper stays
	// IR-eligible. (The treeshaked closure is only 37 — see the note above.)
	const src = `import "std/array";
import "std/string";
function work(ss: string[]): i32 {
    var a: i32 = ss.len();
    var j: string = ss.join(",");
    var r: string[] = ss.reversed();
    var c: string[] = ss.concat(r);
    var acc: i32 = a + j.len() + r.len() + c.len();
    var i: i32 = 0;
    while (i < ss.len()) {
        acc = acc + ss[i].len() + ss[i].trim().len();
        if (ss[i].starts_with("x")) { acc = acc + 1; }
        if (ss[i].contains("y")) { acc = acc + 2; }
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var ss: string[] = ["ab", "cd", "ef"];
    var v: i32 = work(ss);
    if (v > 0) { return 0; }
    return 1;
}`
	prog := filepath.Join(dir, "overbudget_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}

	// Multi-module (stdlib pulled in).
	cntOut, err := exec.Command(mmc, prog, stdlibRoot, "-per-module-count").Output()
	if err == nil {
		if n, cerr := strconv.Atoi(strings.TrimSpace(string(cntOut))); cerr == nil && n < 2 {
			t.Fatalf("-per-module-count = %d, want >= 2 (multi-module program)", n)
		}
	}

	// Routing: the reachable closure is IR-eligible ⇒ "ir". Note -decide reports
	// per-function eligibility on the merged module, so it does NOT distinguish
	// the merged path from the concat; the `.S<idx>` check below does that.
	decideOut, err := exec.Command(mmc, prog, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("-decide failed: %v", err)
	}
	if got := strings.TrimSpace(string(decideOut)); got != "ir" {
		t.Fatalf("over-budget program routed %q, want \"ir\" (IR path over the AST emitter)", got)
	}

	// Correctness: the emitted asm links + runs to exit 0.
	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget compile failed: %v (len=%d)", err, len(asm))
	}
	// WHICH path produced it. Whole-program `.S<idx>` string labels mean the
	// merged IR emit; per-unit `.S<ns>_<idx>` pools would mean the per-module
	// concat. Pinning this keeps the test honest about what it covers, and turns a
	// future change that DOES route the concat into a visible failure here rather
	// than a silent change of meaning — which matters, because the concat has an
	// open cross-unit shape defect (see #3457).
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
