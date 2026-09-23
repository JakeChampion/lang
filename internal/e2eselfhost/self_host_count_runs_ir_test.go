package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__count_runs(s, inside, set)` on the self-host IR path: how many runs of
// bytes whose entry in the u8[] `set` is nonzero begin in s, with `inside`
// nonzero meaning the byte before s was a member. wc's word count.
//
// The set's length lives in the array header and its bytes in the backend's
// element slots, eight bytes on the register backends and four on wasm, so
// the sweep holds a full set, a short one and an empty one, both values of
// `inside`, and every length up to 40.

// countRunsIRProg is SELF-CHECKING: it carries a Fern reference and compares
// the kernel against it. A failure returns a small distinct code saying which
// shape disagreed; 42 means every comparison matched.
const countRunsIRProg = `function ref(s: string, inside: i32, set: u8[]): i32 {
    var prev: boolean = inside != 0;
    var runs: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) {
        var b: i32 = s[i] as i32;
        var member: boolean = b < set.len() && set[b] as i32 != 0;
        if (member && !prev) { runs = runs + 1; }
        prev = member;
        i = i + 1;
    }
    return runs;
}
function space_set(): u8[] {
    var set: u8[] = __alloc_u8(256);
    set = set.with(32, 1 as u8);
    set = set.with(9, 1 as u8);
    set = set.with(10, 1 as u8);
    return set;
}
function main(): i32 {
    var space: u8[] = space_set();
    var short: u8[] = [0 as u8, 1 as u8, 0 as u8, 1 as u8];
    var none: u8[] = [];
    var n: i32 = 0;
    var s: string = "";
    while (n <= 40) {
        var inside: i32 = 0;
        while (inside <= 1) {
            if (__count_runs(s, inside, space) != ref(s, inside, space)) { return 1; }
            if (__count_runs(s, inside, short) != ref(s, inside, short)) { return 2; }
            if (__count_runs(s, inside, none) != ref(s, inside, none)) { return 3; }
            inside = inside + 1;
        }
        if (n % 3 == 2 || n % 7 == 0) { s = s + " "; } else if (n % 5 == 1) { s = s + "\x01"; } else { s = s + "w"; }
        n = n + 1;
    }
    if (__count_runs("the quick\tbrown\n\nfox", 0, space) != 3) { return 4; }
    if (__count_runs(" lead", 1, space) != 0) { return 5; }
    if (__count_runs(" lead", 0, space) != 1) { return 6; }
    if (__count_runs("\x01\x02\x01", 0, short) != 2) { return 7; }
    return 42;
}
`

// runCountRunsIR compiles countRunsIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runCountRunsIR(t *testing.T, target string) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	if target == "arm64-linux" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64-linux"}
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}

	progAsm, progDir := compileSourceModload(t, runner, driverBin, countRunsIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "count_runs_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostCountRunsIRX86_64(t *testing.T) {
	if got := runCountRunsIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__count_runs self-host x86-64 = %d, want 42 (see countRunsIRProg for what each code means)", got)
	}
}

func TestSelfHostCountRunsIRArm64(t *testing.T) {
	if got := runCountRunsIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__count_runs self-host arm64 = %d, want 42 (see countRunsIRProg for what each code means)", got)
	}
}

// TestSelfHostCountRunsIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostCountRunsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host count_runs wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = strings.NewReader(countRunsIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_count_runs")) {
		t.Fatal("emitted wat has no $__fern_count_runs helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "count_runs.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_, _ = run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("__count_runs self-host wasm = %d, want 42 (see countRunsIRProg for what each code means)", got)
	}
}
