package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Self-host RC: a heap enum payload extracted via `match` is freed prematurely.
//
// When a `match (result) { Ok(names) => ... }` arm binds a HEAP payload (here a
// `string[]`), the self-host x86-64 IR RC drops the enum wrapper in a way that
// frees the extracted payload too — a missing ownership transfer (the native
// Perceus increments the moved-out binding / suppresses the wrapper's recursive
// dec of it; the self-host does not). It's a use-after-free: benign while the
// freed backing store is untouched, but corrupted the moment a later allocation
// reuses it — so it only bites when the payload is held live ACROSS an allocating
// call. gdb shows `names[j]` coming back as the allocator's filler bytes
// (0x7979...) and faulting at the element's `movq 8(%rax)`.
//
// The native compiler (interp + compiled) handles both shapes correctly, so this
// is a self-host-only gap — goal-2 (Perceus port) convergence work. Tracked on
// #2649; see rcMatchPayloadWorks / rcMatchPayloadUAF below for the isolating
// pair (var-binding vs match-extraction of the SAME append-built array).
//
// rcMatchPayloadWorks: the array is returned directly and bound to a `var` (no
// enum wrapper). Held across the same allocating `eat` loop, it stays intact.
// This is the ACTIVE guard — it must keep returning 17.
const rcMatchPayloadWorks = `function eat(n: i32): i32 {
    var s: string = "x";
    var i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function build(): string[] {
    var r: string[] = [];
    r = r.append("alpha");
    r = r.append("bravo");
    r = r.append("charlie");
    return r;
}
function main(): i32 {
    var names: string[] = build();
    var total: i32 = 0;
    var j: i32 = 0;
    while (j < names.len()) {
        var junk: i32 = eat(200);
        total = total + names[j].len();
        j = j + 1;
    }
    return total;
}`

// rcMatchPayloadUAF: the SAME array, but returned as Ok(r) and extracted via
// `match`. Everything else is identical. Currently SIGSEGVs on the self-host IR
// path (correct answer, matching rcMatchPayloadWorks + native, is 17). Un-skip
// the subtest below when the self-host match-arm RC gains the ownership transfer.
const rcMatchPayloadUAF = `function eat(n: i32): i32 {
    var s: string = "x";
    var i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function build(): Result[string[], i32] {
    var r: string[] = [];
    r = r.append("alpha");
    r = r.append("bravo");
    r = r.append("charlie");
    return Ok(r);
}
function main(): i32 {
    match (build()) {
        Ok(names) => {
            var total: i32 = 0;
            var j: i32 = 0;
            while (j < names.len()) {
                var junk: i32 = eat(200);
                total = total + names[j].len();
                j = j + 1;
            }
            return total;
        },
        Err(_) => { return 99; }
    }
}`

func compileAndRunSelfHostIR(t *testing.T, gcc string, runner []string, dir, driverBin, name, src string) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed for %s: %v", name, err)
	}
	progBin := buildBin(t, gcc, dir, name, string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	return run.ProcessState.ExitCode()
}

// TestSelfHostMatchPayloadRC pins the working half of the match-payload RC pair
// (a heap array bound to a `var` and held across an allocating call stays intact)
// and documents the broken half (the same array extracted via `match` is freed
// prematurely — a self-host-only UAF, #2649) as a skipped target for the fix.
func TestSelfHostMatchPayloadRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Active guard: the var-binding shape must stay correct (17 = 5+5+7).
	t.Run("var_binding_across_alloc", func(t *testing.T) {
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_work", rcMatchPayloadWorks); code != 17 {
			t.Errorf("var-binding array across an allocating call exited %d, want 17", code)
		}
	})

	// Known bug (#2649): the match-extracted shape is a self-host UAF. Remove the
	// Skip when the self-host match-arm RC gains the payload ownership transfer.
	t.Run("match_payload_across_alloc", func(t *testing.T) {
		t.Skip("known self-host RC bug (#2649): match-extracted heap payload freed prematurely; native returns 17")
		if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "rc_uaf", rcMatchPayloadUAF); code != 17 {
			t.Errorf("match-extracted array across an allocating call exited %d, want 17", code)
		}
	})
}
