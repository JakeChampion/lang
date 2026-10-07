package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnumStructPayloadDropIRX86_64 covers the Perceus enum-payload slice:
// a variant carrying a deep-drop-ok nested STRUCT payload (`Full(Inner)` where Inner
// holds an rc-array field) is RECURSIVELY reclaimed when the enum local is
// consumed by its match: the payload's array buffers are released, then the
// payload box is freed.
//
// The leak/reclaim signal is heap exhaustion: a long fall-through churn that leaks
// the payload's array buffer (and box) each iteration exhausts the bump heap and is
// SIGKILLed (137); with the deep-drop reclaiming them the churn stays bounded (0).
// Each items array goes through id so the payload is built on the heap rather than
// placed as a constant. Variant constructors are UNQUALIFIED (`Full(..)`, not
// `Box.Full(..)`).
func TestSelfHostEnumStructPayloadDropIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// CHURN: 40M consume-by-match cycles over `Full(Inner{items:[..]})`. The payload
	// is a fresh literal, so Inner.items, the Inner box and the enum box are all
	// released each iteration and the churn stays bounded (exit 0); a leak
	// exhausts the heap and is SIGKILLed (137).
	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
enum Box { Full(Inner), Empty }
function mk(): i32 {
    let b: Box = Full(Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) });
    match (b) {
        Full(_) => {},
        Empty => {},
    }
    return 5;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 40000000) { s = mk(); f = f + 1; }
    return s - 5;
}`, "enum_struct_payload_churn", 0)

	// VALUE: bound-borrow-only payload — the arm reads inner.items before the match's
	// post-arm reclaim deep-drops it. A wrong free of a live buffer (or a double-free)
	// would corrupt the read. items[0]+items[15] = 1 + 16 = 17.
	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
enum Box { Full(Inner), Empty }
function f(): i32 {
    let b: Box = Full(Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) });
    let r: i32 = 0;
    match (b) {
        Full(inner) => { r = inner.items[0] + inner.items[15]; },
        Empty => { r = 0; },
    }
    return r;
}
function main(): i32 { return f(); }`, "enum_struct_payload_value", 17)
}
