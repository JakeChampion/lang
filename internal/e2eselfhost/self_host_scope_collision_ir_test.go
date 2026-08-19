package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// scopeCollisionProg exercises the scope-blind-locals fix: a local name reused
// with a DIFFERENT type across sibling match arms. `m` is a `var Ent[]` (array
// slot) in the NIf arm and the struct payload binding in the NMatch arm. The IR
// lowerer allocates slots by name function-wide, so before the fix both `m`s
// collapsed onto one slot with conflicting type metadata (is_arr AND struct) and
// `lower_func` bailed (`g: BAIL lower`). With lower_block
// retiring the locals a block introduces on exit, the NIf arm's `m` is retired
// before the NMatch arm binds its `m`, so each gets a distinct, correctly-typed
// slot and `g` lowers through the IR. main() exercises both arms: g(NIf,b) joins
// b with itself (len 4); g(NMatch,..) returns b (len 2) -> 4*10 + 2 = 42.
const scopeCollisionProg = `struct Ent { k: i32 }
struct MatchPayload { scrutinee: i32, arms: i32 }
enum Node { NIf(i32), NMatch(MatchPayload) }
function join(a: Ent[], b: Ent[]): Ent[] {
    var out: Ent[] = a;
    var i: i32 = 0;
    while (i < b.len()) { out = out.append(b[i]); i = i + 1; }
    return out;
}
function g(n: Node, base: Ent[]): Ent[] {
    match (n) {
        NIf(f) => {
            var m: Ent[] = join(base, base);
            return m;
        },
        NMatch(m) => {
            var x: i32 = m.scrutinee + m.arms;
            return base;
        },
        _ => { return base; }
    }
}
function main(): i32 {
    var b: Ent[] = [Ent { k: 7 }, Ent { k: 8 }];
    var r1: Ent[] = g(NIf(1), b);
    var r2: Ent[] = g(NMatch(MatchPayload { scrutinee: 3, arms: 4 }), b);
    return r1.len() * 10 + r2.len();
}
`

// TestSelfHostScopeCollisionIRX86_64 builds the asm_ir_run stdin driver and (1)
// probes the program, asserting the colliding function `g` now lowers through
// the IR (`g: ir`, `module: IR`) instead of bailing, and (2) emits + links +
// runs it, asserting the result is 42 — proving the distinct-slot lowering is
// not just eligible but correct.
func TestSelfHostScopeCollisionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	run := func(args ...string) ([]byte, int) {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(scopeCollisionProg))
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	// (1) eligibility: the collision now lowers through the IR.
	rep, _ := run("-ir-probe")
	for _, want := range []string{"g: ir", "main: ir", "module: IR"} {
		if !bytes.Contains(rep, []byte(want)) {
			t.Errorf("probe report missing %q (scope collision did not lower)\n--- report ---\n%s", want, rep)
		}
	}

	// (2) correctness: emit -> link -> run, expect 42.
	asm, _ := run()
	if len(asm) == 0 {
		t.Fatal("driver emitted 0 bytes for the scope-collision program")
	}
	progBin := buildBin(t, gcc, dir, "scope_collision", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_, _ = cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("scope-collision program exited %d, want 42", code)
	}
}
