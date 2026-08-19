package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArrayMethodTreeshakeIR pins that a stdlib-importing program using
// an array-method call (`ss.join(sep)`, dispatched to std/array's
// auto-discovered `__method_Array_join` helper) routes the IR path rather than
// bailing (#3457).
//
// The treeshaker prunes the merged module to functions reachable from main
// before codegen so a stdlib-importing program fits asm_ir's IR budget. It
// over-approximates reachability BY NAME, but the method-call syntax
// `arr.<m>(...)` names only `<m>` — never the `__method_Array_<m>` helper it
// dispatches to (nor that helper's `<mod>__`-mangled name). So the helper looked
// unreachable, got pruned, and `find_arr_method` then resolved nothing — the IR
// lowering bailed `i32.join` and dropped the WHOLE module to AST. treeshake now
// appends the helper's canonical `__method_Array_<m>` token at every `.<m>`
// field access and matches it by suffix (ts_kept_name), so the helper survives
// the prune and the module stays on the IR path.
//
// Asserts both halves: `-decide` reports `ir` (routing), and the compiled
// program runs to exit 0 (correctness — `["1","2","3"].join("-") == "1-2-3"`).
//
// Native only: the file-loading driver reads stdlib modules by host path from
// argv, so a qemu runner can't resolve them (mirrors TestSelfHostStdTestE2E).
func TestSelfHostArrayMethodTreeshakeIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t) // util, parser, irlower, asm_ir, treeshake, …
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	const src = `import "std/array";
function joined(): string {
    var ss: string[] = ["1", "2", "3"];
    return ss.join("-");
}
function main(): i32 {
    if (joined() == "1-2-3") { return 0; }
    return 1;
}`
	prog := filepath.Join(dir, "join_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}

	// Routing: the merged module must decide `ir`, not `ast`. Before the
	// treeshake fix this printed `ast` (helper pruned -> i32.join bail).
	decideOut, err := exec.Command(mmc, prog, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("-decide failed: %v", err)
	}
	if got := strings.TrimSpace(string(decideOut)); got != "ir" {
		t.Fatalf("array-method program routed %q, want \"ir\" (treeshake pruned __method_Array_join)", got)
	}

	// Correctness: compile -> assemble -> link -> run, expect exit 0.
	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("self-host compile failed: %v (len=%d)", err, len(asm))
	}
	bin := buildBin(t, gcc, dir, "join_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Errorf("join IR program exited %d, want 0 (\"1-2-3\")", code)
	}
}
