package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optStrBlockReclaimCases pin the #4353 item-2 PER-BLOCK consumed-match
// Option/Result reclaim: the fn-level consumed_rcpayload_option_frees pass
// scans only top-level fn-body statements, so a LOOP-LOCAL
// `var o = Some(<fresh rc payload>)` consumed by exactly one match in the
// same nested block leaked the payload AND the option box per iteration
// (probe p2 in the 2026-07-20 recon on #4353). lower_block now runs the same
// classifier + gates over each nested block's own statement list and emits
// the payload deep-drop + box free after the consuming match. Escaping arm
// bindings, post-match uses, and double matches keep today's sound leak.
var optStrBlockReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The core p2 shape: loop-local Option[string] with a fresh concat
	// payload, consumed by a borrowing match. Flat after the fix.
	{"optstr-loop-local-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var o: Option[string] = Some("v" + w.to_string()); match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var o2: Option[string] = Some("v" + i.to_string()); match (o2) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Result sibling: loop-local Err(<fresh string>) consumed by a borrowing
	// match (the classifier admits Ok/Err the same way).
	{"resulterr-loop-local-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var r: Result[i32, string] = Err("e" + w.to_string()); match (r) { Ok(v) => { acc = (acc + v) % 251; }, Err(m) => { acc = (acc + m.len()) % 251; } } w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var r2: Result[i32, string] = Err("e" + i.to_string()); match (r2) { Ok(v) => { acc = (acc + v) % 251; }, Err(m) => { acc = (acc + m.len()) % 251; } } i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// Array-payload sibling: loop-local Some([..]) consumed by a borrowing
	// match — the non-string rc payload rides emit_opt_payload_drop.
	{"optarr-loop-local-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var o: Option[i32[]] = Some([w, w + 1]); match (o) { Some(xs) => { acc = (acc + xs[0]) % 251; }, None => { } } w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var o2: Option[i32[]] = Some([i, i + 1]); match (o2) { Some(xs) => { acc = (acc + xs[0]) % 251; }, None => { } } i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// IF-BODY sibling: the seam is lower_block, so a non-loop nested block
	// takes the same reclaim (churned via an outer loop).
	{"optstr-if-body-churn", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { if (w >= 0) { var o: Option[string] = Some("v" + w.to_string()); match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } } w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { if (i >= 0) { var o2: Option[string] = Some("v" + i.to_string()); match (o2) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } } i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative: the Some-arm binding is stored outside the match —
	// opt_arm_binding_escapes rejects the credit, the extracted string stays
	// valid (leak-safe, no UAF, detector zero).
	{"optstr-binding-escape-safe", `function main(): i32 {
    var keep: string = "";
    var w: i32 = 0;
    while (w < 100) {
        var o: Option[string] = Some("k" + w.to_string());
        match (o) { Some(s) => { keep = s; }, None => { } }
        w = w + 1;
    }
    if (keep.len() < 2) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// ALIAS negative: `var al = o` aliases the option box, and al is matched
	// AFTER o's consuming match — the escape gate must reject o's credit or
	// al's match reads a freed box. Values exact, detector zero.
	{"optstr-alias-after-safe", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) {
        var o: Option[string] = Some("k" + w.to_string());
        var al: Option[string] = o;
        match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } }
        match (al) { Some(s2) => { acc = (acc + s2.len()) % 251; }, None => { } }
        w = w + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// DOUBLE-MATCH negative: two matches consume o — n_match != 1, no
	// credit, both matches read valid data, detector zero.
	{"optstr-double-match-safe", `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) {
        var o: Option[string] = Some("k" + w.to_string());
        match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } }
        match (o) { Some(s2) => { acc = (acc + s2.len()) % 251; }, None => { } }
        w = w + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostOptStrBlockReclaimIRX86_64 drives the cases through the
// self-hosted x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostOptStrBlockReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range optStrBlockReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
