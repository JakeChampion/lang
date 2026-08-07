package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostEnumStructPayloadDropIRX86_64 covers the Perceus enum-payload slice:
// a variant carrying a deep-drop-ok nested STRUCT payload (`Full(Inner)` where Inner
// holds an rc-array field) is now RECURSIVELY reclaimed when the enum local is
// consumed by its match — `__struct_drop_<Inner>` releases the payload's array
// buffers, then the payload box is freed, instead of the whole enum bailing the
// reclaim, which leaks the box + payload when the payload type is undroppable.
//
// SOUNDNESS: the consume gate (consumed_rcpayload_enum_frees) admits the enum only
// when the struct payload was a FRESH struct LITERAL (variant_struct_payloads_fresh),
// so the payload box is the sole owner (rc == 1) — a bare-ident / call payload that
// aliases a live local is rejected, since deep-dropping it would double-free its
// inner buffers (the same fresh-literal rule the struct-field deep-drop uses). The
// recursion is depth-1 (Inner is a deep-drop-ok LEAF), so it cannot cycle.
//
// The leak/reclaim signal is heap exhaustion: a long fall-through churn that leaks
// the payload's array buffer (and box) each iteration exhausts the bump heap and is
// SIGKILLed (137); with the deep-drop reclaiming them the churn stays bounded (0).
// Variant constructors are UNQUALIFIED (`Full(..)`, not `Box.Full(..)`) — the
// fresh-ctor analysis matches a bare-ident callee.
func TestSelfHostEnumStructPayloadDropIRX86_64(t *testing.T) {
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
			t.Fatalf("%s: emitted asm missing %q — the enum struct payload did not deep-drop", name, wantAsmSubstr)
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
	// is a fresh literal, so the deep-drop reclaims Inner.items + the Inner box + the
	// enum box each iteration → bounded (exit 0). Asserts __struct_drop_Inner is
	// emitted; an undroppable enum leaks until SIGKILL (137).
	run(t, `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function mk(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] });
    match (b) {
        Full(_) => {},
        Empty => {},
    }
    return 5;
}
function main(): i32 {
    var s: i32 = 0; var f: i32 = 0;
    while (f < 40000000) { s = mk(); f = f + 1; }
    return s - 5;
}`, "enum_struct_payload_churn", 0, "__struct_drop_Inner")

	// VALUE: bound-borrow-only payload — the arm reads inner.items before the match's
	// post-arm reclaim deep-drops it. A wrong free of a live buffer (or a double-free)
	// would corrupt the read. items[0]+items[15] = 1 + 16 = 17.
	run(t, `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function f(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] });
    var r: i32 = 0;
    match (b) {
        Full(inner) => { r = inner.items[0] + inner.items[15]; },
        Empty => { r = 0; },
    }
    return r;
}
function main(): i32 { return f(); }`, "enum_struct_payload_value", 17, "__struct_drop_Inner")
}
