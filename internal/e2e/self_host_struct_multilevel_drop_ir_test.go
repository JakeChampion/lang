package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructMultiLevelDropIRX86_64 covers the Perceus MULTI-LEVEL deep-drop
// (#2649): nested_field_deep_drop_ok was generalised from a LEAF-only rule (depth-1)
// to an ACYCLIC-closure rule, so a chain of DIRECT nested-struct fields deep-drops
// all the way down. For `A { b: B }`, `B { c: C }`, `C { items: i32[] }`, dropping a
// reclaimable `A` local now emits `__struct_drop_A` -> `__struct_drop_B` ->
// `__struct_drop_C`, and C frees `items` — whereas the old leaf gate stopped at A's
// b-field (B is non-leaf), shallow-freeing B's box and leaking B.c and C.items.
//
// The emitted call graph is a DAG bounded by the struct-type count (a type cannot
// re-appear on the chain without a cycle, which the acyclicity check rejects), so
// the runtime recursion terminates. Each level re-applies the same is_unique guard
// the leaf case already used, so a shared inner (rc>1) still skips the field release.
//
// KEY DIFFERENTIAL: `call __fn___struct_drop_B` is emitted ONLY under the multi-level
// gate — B is a non-leaf, so the old leaf rule kept A's b-field shallow and never
// struct-dropped B. Its presence proves A recursed into a non-leaf inner. Runtime
// signal is heap exhaustion: a long churn that leaks C.items each iteration exhausts
// the bump heap and is SIGKILLed (137); with the multi-level reclaim it stays
// bounded (exit 0).
func TestSelfHostStructMultiLevelDropIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted asm missing %q — the multi-level nested field did not deep-drop", name, wantAsmSubstr)
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
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// MULTI-LEVEL DEEP-DROP + CHURN: a 3-level chain `A -> B -> C{ items }`. Every
	// struct in the chain is a fresh sole-owned literal (rc 1), so each is_unique gate
	// passes and the whole chain reclaims C.items each iteration. Asserts the recursive
	// `call __fn___struct_drop_B` (the non-leaf inner — impossible under the old leaf
	// gate) is emitted, and that 150M alloc->drop cycles stay bounded (exit 0); a
	// regression to the leaf-only drop leaks B.c + C.items every call -> SIGKILL (137).
	run(t, `struct C { items: i32[] }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
function mk(): i32 {
    var a: A = A { b: B { c: C { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, bt: 2 }, at: 7 };
    return a.b.c.items[0] + a.b.c.items[15] + a.b.bt + a.at;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 26;
}`, "struct_multilevel_drop_churn", 0, "call __fn___struct_drop_B")

	// VALUE-CORRECTNESS: the deep value is read back before the drop; a premature free
	// of a live buffer down the chain would corrupt the read. a.b.c.items[0..15] sum to
	// 136, + b.bt 2 + a.at 7 = 145.
	run(t, `struct C { items: i32[] }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
function main(): i32 {
    var a: A = A { b: B { c: C { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, bt: 2 }, at: 7 };
    var sum: i32 = 0; var j: i32 = 0;
    while (j < 16) { sum = sum + a.b.c.items[j]; j = j + 1; }
    return sum + a.b.bt + a.at;
}`, "struct_multilevel_drop_value", 145, "call __fn___struct_drop_C")

	// FOUR-LEVEL: pushes the DAG one deeper (`W -> X -> Y -> Z{ items }`) to prove the
	// transitive body-emission closure (need() re-checks) chains beyond depth-2 and the
	// runtime recursion still terminates. items sum 136 + z/y/x/w tags = 136+1+2+3+4=146.
	run(t, `struct Z { items: i32[], zt: i32 }
struct Y { z: Z, yt: i32 }
struct X { y: Y, xt: i32 }
struct W { x: X, wt: i32 }
function mk(): i32 {
    var w: W = W { x: X { y: Y { z: Z { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16], zt: 1 }, yt: 2 }, xt: 3 }, wt: 4 };
    return w.x.y.z.items[0] + w.x.y.z.items[15] + w.x.y.z.zt + w.x.y.yt + w.x.xt + w.wt;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 2000000) { s = mk(); f = f + 1; }
    return s - 27;
}`, "struct_multilevel_drop_four", 0, "call __fn___struct_drop_Y")
}
