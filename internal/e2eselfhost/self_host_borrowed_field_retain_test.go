package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostBorrowedFieldRetainIRX86_64 pins the pairing that broke the
// three-way circularity behind alloc_flat_fresh_array_arg's residual.
//
// `mk(deps: string[]): H { return H { deps: deps }; }` stores a BORROWED
// parameter into the struct it returns. Three refusals used to hold each other
// up: the strict-fresh return gate refused a bare-ident array field, so the
// CALLER's binding earned no drop at all; strarrfld_scan refused the same store,
// so the type was not STRFLDOK-admitted; and the struct-literal construction
// took no retain, because that rides struct_routes_field_reclaim — which the
// second decides. Opening the store gate turns the retain on, which is what
// makes crediting the caller safe; either alone is inert, and the caller credit
// alone would free the caller's array.
//
// The witness is the ASM SHAPE, not a byte count: the caller emits
// __struct_drop_H / __field_reclaim_H where it previously emitted neither. The
// row measures 398 -> 268 B/round on the method/struct probes and 798 -> 537 on
// the conformance case, so it is an improvement rather than a flattening, and a
// high-water threshold would be a budget for the rest of the port instead of a
// gate on this. The value rows carry the soundness half.
func TestSelfHostBorrowedFieldRetainIRX86_64(t *testing.T) {
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

	// userCodeCalls: a `call <helper>` under a `__fn_<name>:` label that is not
	// itself a runtime helper. The runtime block emits helper bodies
	// unconditionally, so a whole-asm substring match reads every compile as
	// admitted.
	userCodeCalls := func(asm, helper string) bool {
		inUser := false
		for _, ln := range strings.Split(asm, "\n") {
			if strings.HasPrefix(ln, "__fn_") && strings.HasSuffix(ln, ":") {
				inUser = !strings.HasPrefix(ln, "__fn___fern_")
				continue
			}
			if inUser && strings.Contains(ln, "call "+helper) {
				return true
			}
		}
		return false
	}

	run := func(t *testing.T, prog, name string, want int, wantDrop string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantDrop != "" {
			has := userCodeCalls(string(asm), wantDrop)
			if !has {
				t.Fatalf("%s: caller emits no `call %s` — the borrowed-parameter field store was not admitted, so the returned struct earns no drop", name, wantDrop)
			}
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
			t.Errorf("%s exited %d, want %d (99 = over-release; other non-zero = a value read after the drop was wrong)", name, code, want)
		}
	}

	// ADMISSION: the caller of a borrowed-parameter producer now drops the
	// returned struct. Absent on the parent, where `round` emitted no drop at
	// all — not a shallower one, none.
	run(t, `struct H { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function mk(deps: string[]): H { return H { deps: deps }; }
function build(pre: string): i32 { var live: string[] = deps_of(pre); var h: H = mk(live); return h.deps.len() + live.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 6) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"borrowed-field-caller-drops", 0, "__fn___struct_drop_H")

	// SOUNDNESS, the sharpest shape: the struct is built from a LIVE local and
	// dropped FIRST, in an inner scope, and the local's elements are read
	// afterwards under heavy recycling pressure (a 40-block string plus a fresh
	// array between the drop and the reads). Without the construction retain the
	// drop frees the array and every element, and these reads see recycled
	// memory rather than their own bytes.
	run(t, `struct H { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mk(deps: string[]): H { return H { deps: deps }; }
function build(pre: string): i32 {
    var live: string[] = deps_of(pre);
    var seen: i32 = 0;
    if (live.len() > 0) { var h: H = mk(live); seen = h.deps.len(); }
    var junk1: string = fill(40);
    var junk2: string[] = deps_of(pre);
    if (junk1.len() < 0 || junk2.len() < 0) { return 0; }
    return seen + live.len() + live[0].len() + live[2].len();
}
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 92) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"borrowed-field-local-outlives-struct", 0, "")

	// TWO structs over ONE array: each drop decs, and the array survives both.
	// A retain taken once but dec'd twice would tick the underflow detector.
	run(t, `struct H { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mk(deps: string[]): H { return H { deps: deps }; }
function build(pre: string): i32 {
    var live: string[] = deps_of(pre);
    var a: H = mk(live);
    var b: H = mk(live);
    var junk: string = fill(40);
    if (junk.len() < 0) { return 0; }
    return a.deps.len() + b.deps.len() + live[1].len();
}
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 49) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(3000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"borrowed-field-two-structs-one-array", 0, "")
}
