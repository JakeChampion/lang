package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostOverBudgetPerModuleIR pins the #3457 over-budget fix: a program
// whose merged module exceeds the 512-function IR budget — which used to drop
// the WHOLE program to the AST emitter — now routes the IR path per-module
// (asm_load_run.emit_per_module_concat: each module is under budget, so each
// lowers on the IR path, and the units concatenate into one linkable stream).
//
// Each module is pruned to the reachable-name set (the treeshaked merged
// module's funcs) before emit, so an UNUSED stdlib helper that the IR path can't
// lower yet doesn't drag the program onto the AST emitter — only the reachable
// closure must be IR-eligible, exactly as the merged treeshaked path requires.
//
// Forced over budget with -no-treeshake at the merged-decide level is NOT used
// here (that would leave unreachable ineligible funcs in the closure); instead
// the program uses enough of std/array + std/string that the TREESHAKED merged
// still clears 512. Asserts it is genuinely multi-module, routes "ir", and the
// concatenated units link + run correctly.
//
// Native only: the file-loading driver reads stdlib by host path from argv.
func TestSelfHostOverBudgetPerModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
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

	// Use many distinct std/array + std/string methods so the treeshaked reachable
	// closure stays over the 512-func budget (each method drags in its helper +
	// transitive deps), while every reachable helper is IR-eligible.
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

	// Routing: over budget + reachable closure eligible ⇒ "ir" (the concat path).
	decideOut, err := exec.Command(mmc, prog, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("-decide failed: %v", err)
	}
	if got := strings.TrimSpace(string(decideOut)); got != "ir" {
		t.Fatalf("over-budget program routed %q, want \"ir\" (per-module concat over the AST emitter)", got)
	}

	// Correctness: the per-module-concatenated units link + run to exit 0.
	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("over-budget compile failed: %v (len=%d)", err, len(asm))
	}
	bin := buildBin(t, gcc, dir, "overbudget_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("over-budget program exited %d, want 0", code)
	}
}
