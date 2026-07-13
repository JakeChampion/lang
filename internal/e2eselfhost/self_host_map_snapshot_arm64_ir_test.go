package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostMapKeysSnapshotIRArm64 is the arm64 port of
// TestSelfHostMapKeysSnapshotIRX86_64 (#4353 slices 1+2): scalar keys()/values()
// snapshot-copy via the arm64 __fern_map_snapshot_col, plus the owncols
// (x4 bit 1) owned grow in asm_arm64.fern's __fern_map_set. Lighter churn under
// qemu, same assertions: snapshot semantics, absolute grow-churn flatness,
// keys-taken flatness (no double free: __rc_underflow() == 0), and the
// post-loop release of the `for (k, v) in m` column snapshots.
func TestSelfHostMapKeysSnapshotIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (1 = leak; 99 = over-release/double free; other = correctness step)", name, code, want)
		}
	}

	// Snapshot semantics across a growing insert run (cap 4 -> 8).
	run(t, `function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20 };
    var ks = m.keys();
    var vs: i32[] = m.values();
    m.set(9, 90);
    m.set(10, 100);
    m.set(11, 110);
    m.set(1, 11);
    if (ks.len() != 2) { return 10; }
    if (vs.len() != 2) { return 11; }
    var sv: i32 = 0;
    var i: i32 = 0;
    while (i < vs.len()) { sv = sv + vs[i]; i = i + 1; }
    if (sv != 30) { return 12; }
    if (m.len() != 5) { return 13; }
    if (m.get_or(1, 0) != 11) { return 14; }
    if (m.get_or(10, 0) != 100) { return 15; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "map-keys-snapshot-semantics-arm64", 0)

	// i32/i32 grow churn, no keys() taken: the owned grow + map_free make it
	// FLAT (the #4877 grow-leak closing on arm64).
	run(t, `function build(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2 };
    var j: i32 = 0;
    while (j < 12) { m.set(j + 10, j * 2); j = j + 1; }
    if (m.has(15)) { return m.len(); }
    return 0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 500) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "map-i32-grow-churn-flat-arm64", 0)

	// keys()-taken churn: snapshot copy swept + map_free frees the real
	// buffers exactly once — flat, no underflow.
	run(t, `function build(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2 };
    var j: i32 = 0;
    while (j < 8) { m.set(j + 10, j); j = j + 1; }
    var ks = m.keys();
    var vs = m.values();
    return ks.len() + vs.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 500) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "map-keys-taken-churn-flat-arm64", 0)

	// Mutation during `for (k, v) in m` + grow: snapshot iteration semantics,
	// no use-after-free from the owned grow, post-loop snapshot release.
	run(t, `function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    var total: i32 = 0;
    for (k, v) in m {
        total = total + k + v;
        m.set(k + 100, v);
    }
    if (total != 66) { return 50; }
    if (m.len() != 6) { return 51; }
    if (m.get_or(102, 0) != 20) { return 52; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "map-kv-iter-mutate-snapshot-arm64", 0)
}
