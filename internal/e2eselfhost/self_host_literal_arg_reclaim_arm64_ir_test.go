package e2eselfhost

import (
	"testing"
)

// TestSelfHostLiteralArgReclaimIRArm64 is the arm64 port of
// TestSelfHostLiteralArgReclaimIRX86_64 (#4355 slice 6): the literal
// string-arg box reclaim at borrowable call positions, on the arm64 IR
// backend (the reclaim is emitted at the IR layer, so both natives share
// it; arm64 additionally exercises __fern_str_free's register discipline
// mid-expression). Lighter churn under qemu.
func TestSelfHostLiteralArgReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = literal-arg box leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// Literal arg at a borrowable position — churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit("ab"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + readit("ab"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 2400) { return 97; }
    return 0;
}`, "literal-arg-borrowable-flat-arm64", 0)

	// NON-borrowable position (callee returns its param): the bound value
	// stays readable at detector zero.
	run(t, `function keepit(nm: string): string { return nm; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var got: string = keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "literal-arg-retained-safe-arm64", 0)

	// #6544: the same reclaim at a METHOD call, which was wired at the
	// free-call site only.
	run(t, `function (s: string) readit(nm: string): i32 { return s.len() + nm.len(); }
function main(): i32 {
    var recv: string = "rr";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + recv.readit("ab"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + recv.readit("ab"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (recv.len() != 2) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 4800) { return 97; }
    return 0;
}`, "method-literal-arg-borrowable-flat-arm64", 0)

	// The param MOVED into a struct field is not borrowable, so the literal
	// keeps its prior sound leak and the field stays readable.
	run(t, `struct Box { tag: string, n: i32 }
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var b: Box = Box { tag: "start", n: i % 8 };
        var r: Box = b.relabel("fresh-tag-value");
        if (r.tag.len() != 15) { bad = 1; }
        if (b.tag.len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "method-literal-arg-consumed-safe-arm64", 0)

	// The fresh PRODUCER CALL in argument position, on the second register
	// backend. Same reclaim, a different emitter.
	run(t, `function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function size(s: string): i32 { return s.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + size(mks(i))) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { acc = (acc + size(mks(j))) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "producer-call-arg-borrowable-flat-arm64", 0)

	// The callee returns the argument: refused, and the aliased result is read
	// back rather than merely counted.
	run(t, `function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function pick(s: string): string { return s; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var r: string = pick(mks(i));
        if (r.len() < 41) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-returned-safe-arm64", 0)

	// The ARRAY sibling on the second register backend.
	run(t, `function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function size(d: i32[]): i32 { return d.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = (acc + size(mk(i))) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 3000) { acc = (acc + size(mk(j))) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "producer-call-arr-arg-borrowable-flat-arm64", 0)

	// Refused: the callee returns the array, and the alias is read back.
	run(t, `function mk(n: i32): i32[] { var out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function pick(d: i32[]): i32[] { return d; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) { var r: i32[] = pick(mk(i)); if (r.len() != 3 || r[2] != i + 2) { bad = 1; } i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-returned-safe-arm64", 0)

	// The counted-retain STRING position on the second register backend, with
	// the byte-index read that keeps the parameter credited. 24 B/round before;
	// lighter churn under qemu.
	run(t, `struct Q { tag: string, k: i32 }
function mkq2(t: string, k: i32): Q { return Q { tag: t, k: k + (t[0] as i32) }; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var c: Q = mkq2("tag", i); acc = (acc + c.k + c.tag.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { var d: Q = mkq2("tag", j); acc = (acc + d.k + d.tag.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "counted-retain-str-arg-index-read-flat-arm64", 0)
}
