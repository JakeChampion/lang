package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructFreshRetFieldIRX86_64 covers the Perceus slice-3 deep-drop
// FOLLOW-UP: tightening the struct-construction no-inc set to fresh-RETURNING
// calls. A nested-struct field whose value is a CALL to a strict-fresh-struct-
// returning function (`Outer { inner: mk_inner() }`) is no longer alias-inc'd —
// the callee handed back a fresh sole-owned box (return_fresh_struct_ret_fns), so
// the new struct owns it outright and the field-drop reclaims the inner box,
// instead of leaking the rc-2 alias-inc'd box that the conservative retain left.
//
// SCOPE: this case's inner struct is leaf-safe (scalar fields only), so the
// reclaim win is the inner BOX itself and soundness is trivial — a leaf-safe
// inner owns no buffers, so the box free can never double-free a field, and the
// only requirement, that the box is the sole owner, is exactly what the
// strict-fresh classifier guarantees. The registry itself is not limited to that
// shape: it also admits a scalar-array field whose value is a direct literal, or
// (#6758) a local this frame built and never escaped elsewhere.
//
// The leak/reclaim signal is heap exhaustion: a long churn allocating Outer+Inner
// each iteration leaks the Inner box per iteration under the old alias-inc (rc 2,
// the shallow field-drop only decs to 1) → exhausts the bump heap → SIGKILL (137);
// with the inc skipped the Inner box is freed (rc 1 → 0) so the churn stays
// bounded (exit 0). Same differential the field-reclaim IR tests use.
func TestSelfHostStructFreshRetFieldIRX86_64(t *testing.T) {
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
			t.Fatalf("%s: emitted asm missing %q — the IR path was not taken", name, wantAsmSubstr)
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

	// FRESH-RET FIELD + CHURN: `o.inner` is `mk_inner()`, a call to a strict-fresh-
	// struct-returning fn, so the inner box is NOT alias-inc'd and the field-drop
	// reclaims it. 150M alloc→drop cycles stay bounded (exit 0); under the pre-slice
	// alias-inc the Inner box leaked every call → heap exhausted → SIGKILL (137).
	// The IR path is pinned by the emitted `__fn___struct_drop_Outer`.
	run(t, `struct Inner { a: i32, b: i32 }
struct Outer { inner: Inner, tag: i32 }
function mk_inner(): Inner { return Inner { a: 1, b: 2 }; }
function mk(): i32 {
    var o: Outer = Outer { inner: mk_inner(), tag: 7 };
    return o.inner.a + o.inner.b + o.tag;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 10;
}`, "struct_freshret_field_churn", 0, "call __fn___struct_drop_Outer")

	// VALUE-CORRECTNESS: the inner is read back before the drop; a wrong free of a
	// live box would corrupt it. a(1) + b(2) + tag(7) = 10.
	run(t, `struct Inner { a: i32, b: i32 }
struct Outer { inner: Inner, tag: i32 }
function mk_inner(): Inner { return Inner { a: 1, b: 2 }; }
function main(): i32 {
    var o: Outer = Outer { inner: mk_inner(), tag: 7 };
    return o.inner.a + o.inner.b + o.tag;
}`, "struct_freshret_field_value", 10, "")
}
