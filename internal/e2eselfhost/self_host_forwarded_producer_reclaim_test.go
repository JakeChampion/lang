package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A frame whose body is nothing but `return <producer>(…)` leaked the box it
// forwarded (#7062). The strict-fresh struct registry admitted only a body whose
// every return was a struct LITERAL, so `outer(a, b) { return mkp(a, b); }`
// earned no entry and the caller's binding got no reclaim credit — the box was
// never freed at all, 48 B a round on a struct with nothing but scalar fields,
// where calling the same producer directly was flat. `FERN_LEAKCHECK=1` said it
// plainly: allocs=3 frees=0 forwarded, allocs=3 frees=3 direct.
//
// The forwarding arm rests on what the callee's own entry already proves. `mkp`
// is admitted because every return is a strict-fresh literal, so its result is a
// box it allocated and nobody else references; a frame that neither binds nor
// aliases that result hands the caller the same sole reference. `return <ident>`
// of a bound local stays refused — it could have been aliased in between.
//
// The registry's bare-name half became a least fixpoint to carry this, so a
// chain of forwarding frames is admitted end to end and an ungrounded mutual
// recursion stays uncredited.
func TestSelfHostForwardedProducerReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = forwarded box leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// The row itself. 48 B/round before.
	run(t, `struct P { a: i32, b: i32 }
function mkp(a: i32, b: i32): P { return P { a: a, b: b }; }
function outer(a: i32, b: i32): P { return mkp(a, b); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var c: P = outer(i, i + 1); acc = (acc + c.a + c.b) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { var d: P = outer(j, j + 1); acc = (acc + d.a + d.b) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "forwarded-producer-flat", 0)

	// Two frames deep. A single pass over the declarations credits `mid` but not
	// `outer`, which still sees an uncredited callee when its own turn comes —
	// this is what makes the registry's bare-name half a fixpoint rather than a
	// scan. Also 48 B/round before.
	run(t, `struct P { a: i32, b: i32 }
function mkp(a: i32, b: i32): P { return P { a: a, b: b }; }
function mid(a: i32, b: i32): P { return mkp(a, b); }
function outer(a: i32, b: i32): P { return mid(a, b); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var c: P = outer(i, i + 1); acc = (acc + c.a + c.b) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { var d: P = outer(j, j + 1); acc = (acc + d.a + d.b) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "forwarded-producer-chain-flat", 0)

	// REFUSED at the intermediate callee. `pick` hands back its PARAMETER, so it
	// is not strict-fresh and never enters the registry; `outer` forwarding to it
	// therefore earns nothing either, and the caller keeps its prior safe leak.
	// The fields are re-read, so a wrongly admitted release shows as a wrong
	// value rather than a byte count.
	run(t, `struct P { a: i32, b: i32 }
function mkp(a: i32, b: i32): P { return P { a: a, b: b }; }
function pick(p: P): P { return p; }
function outer(a: i32, b: i32): P { return pick(mkp(a, b)); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var c: P = outer(i, i + 1);
        if (c.a != i) { bad = 1; }
        if (c.b != i + 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "forwarded-through-aliasing-callee-safe", 0)

	// An UNGROUNDED cycle: each of the two forwards to the other and neither
	// reaches a literal. The fixpoint starts empty and only adds, so neither is
	// ever credited — the conservative direction. Both are live code, so the
	// classifier really does have to walk them.
	run(t, `struct P { a: i32, b: i32 }
function ping(a: i32, b: i32): P { if (a > 100000) { return pong(a, b); } return P { a: a, b: b }; }
function pong(a: i32, b: i32): P { return ping(b, a); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var c: P = pong(i, i + 1);
        if (c.a != i + 1) { bad = 1; }
        if (c.b != i) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "forwarded-ungrounded-cycle-safe", 0)

	// The forwarded result must outlive the release when the caller keeps it.
	// Every struct built through the forwarding frame is held to the end and its
	// string field re-read there, so a release that took the last reference
	// rather than a redundant one shows as a wrong value.
	run(t, `struct Q { tag: string, k: i32 }
function mkq(k: i32): Q { return Q { tag: "payload-tag", k: k }; }
function outer(k: i32): Q { return mkq(k); }
function main(): i32 {
    var keep: Q[] = [];
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { keep = keep.append(outer(i)); i = i + 1; }
    var j: i32 = 0;
    while (j < 200) {
        if (keep[j].tag != "payload-tag") { bad = 1; }
        if (keep[j].k != j) { bad = 1; }
        j = j + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "forwarded-producer-survives-safe", 0)
}

// TestSelfHostForwardedProducerReclaimWasmIR is the wasm port. The admission is
// decided at the IR layer, so both backends take it; this leg asserts values and
// the underflow detector rather than heap flatness, since the WAT driver's own
// allocations sit between any two probes.
func TestSelfHostForwardedProducerReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host forwarded-producer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name     string
		src      string
		expected int
	}{
		{"forwarded-producer-wasm", `struct P { a: i32, b: i32 }
function mkp(a: i32, b: i32): P { return P { a: a, b: b }; }
function mid(a: i32, b: i32): P { return mkp(a, b); }
function outer(a: i32, b: i32): P { return mid(a, b); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var c: P = outer(i, i + 1);
        if (c.a != i) { bad = 1; }
        if (c.b != i + 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"forwarded-through-aliasing-callee-safe-wasm", `struct P { a: i32, b: i32 }
function mkp(a: i32, b: i32): P { return P { a: a, b: b }; }
function pick(p: P): P { return p; }
function outer(a: i32, b: i32): P { return pick(mkp(a, b)); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var c: P = outer(i, i + 1);
        if (c.a != i) { bad = 1; }
        if (c.b != i + 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
		{"forwarded-producer-survives-safe-wasm", `struct Q { tag: string, k: i32 }
function mkq(k: i32): Q { return Q { tag: "payload-tag", k: k }; }
function outer(k: i32): Q { return mkq(k); }
function main(): i32 {
    var keep: Q[] = [];
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { keep = keep.append(outer(i)); i = i + 1; }
    var j: i32 = 0;
    while (j < 200) {
        if (keep[j].tag != "payload-tag") { bad = 1; }
        if (keep[j].k != j) { bad = 1; }
        j = j + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("forwarded-producer wasm IR %q = %d, want %d (99 = over-release; 88 = live value freed)", tc.name, got, tc.expected)
			}
		})
	}
}
