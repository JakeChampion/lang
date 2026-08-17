package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The #6491 owned-call-result reclaim, extended to METHOD callees (#6544).
//
// `mk().k` — a field read off a call whose callee the strict-fresh registry
// proves returns a sole-owned box — releases that box right there. The
// admission ran through `owned_fresh_call_callee`, which matched only a bare
// ExprIdent callee, so the identical `seed.next().a` was refused and leaked
// 48 bytes a round where the free-function spelling was flat. The registry has
// keyed methods "<Type>.<method>" all along; only the lookup was missing.
//
// The refusal rows matter more than usual here: a wrongly admitted result is a
// use-after-free, not a leak, because the released box may be one a live local
// still holds. Each reads the receiver back after thousands of further rounds
// have recycled the freelist.
var methodResultFieldCases = []struct {
	name     string
	src      string
	expected int
}{
	// REFUSED — an IDENTITY return. `same()` hands back the receiver's own box,
	// so it is not strictly fresh and no key exists. Freeing it would free
	// `seed`, which is read on every round and again at the end.
	{"methodresult-identity-not-freed", `struct P { a: i32, b: i32 }
function (p: P) same(): P { return p; }
function main(): i32 {
    var acc: i32 = 0;
    var seed: P = P { a: 7, b: 9 };
    var i: i32 = 0;
    while (i < 4000) { acc = (acc + seed.same().a) % 251; if (seed.a != 7) { return 95; } i = i + 1; }
    if (seed.b != 9) { return 96; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// REFUSED — the method returns a box built from a LIVE local's fields, so
	// its buffers alias and the strict-fresh gate declines. Values stay correct
	// and nothing is over-released.
	{"methodresult-aliased-field-not-freed", `struct Box { tag: string, n: i32 }
function (b: Box) share(): Box { return Box { tag: b.tag, n: b.n + 1 }; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4000) {
        var b: Box = Box { tag: "start-tag-value", n: i % 8 };
        acc = (acc + b.share().n) % 251;
        if (b.tag.len() != 15) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ADMITTED, and balanced at scale: the released box is the method's own
	// allocation and no name holds it. 200,000 rounds with the receiver read
	// each time; an over-release would tick the underflow counter.
	{"methodresult-fresh-scale-balanced", `struct P { a: i32, b: i32 }
function (p: P) next(): P { return P { a: p.a + 1, b: p.b }; }
function main(): i32 {
    var acc: i32 = 0;
    var seed: P = P { a: 1, b: 2 };
    var i: i32 = 0;
    while (i < 200000) { acc = (acc + seed.next().a) % 251; if (seed.a != 1) { return 95; } i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// methodResultFieldLeakCases assert heap FLATNESS, so they are register-backend
// only — the wasm driver's own allocations sit between the probes.
var methodResultFieldLeakCases = []struct {
	name string
	src  string
}{
	// The headline: a strictly-fresh METHOD result consumed by a scalar field
	// read is reclaimed, matching the free-function spelling. 48 B/round before.
	{"methodresult-method-callee-flat", `struct P { a: i32, b: i32 }
function (p: P) next(): P { return P { a: p.a + 1, b: p.b }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var seed: P = P { a: 1, b: 2 };
    var i: i32 = 0;
    while (i < n) { acc = (acc + seed.next().a) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// The free-function spelling of the same program, which was ALREADY flat.
	// It is the control that says this row pair is about the callee spelling
	// and nothing else.
	{"methodresult-free-callee-flat", `struct P { a: i32, b: i32 }
function nextp(p: P): P { return P { a: p.a + 1, b: p.b }; }
function rounds(n: i32): i32 {
    var acc: i32 = 0;
    var seed: P = P { a: 1, b: 2 };
    var i: i32 = 0;
    while (i < n) { acc = (acc + nextp(seed).a) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    var acc: i32 = rounds(200);
    var b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
}

func methodResultAllCases() []struct {
	name     string
	src      string
	expected int
} {
	out := append([]struct {
		name     string
		src      string
		expected int
	}{}, methodResultFieldCases...)
	for _, lc := range methodResultFieldLeakCases {
		out = append(out, struct {
			name     string
			src      string
			expected int
		}{lc.name, lc.src, 0})
	}
	return out
}

// TestSelfHostMethodResultFieldReclaimX86_64: 98 = the result box leaked,
// 99 = over-release, 95/96 = a refused result was freed anyway.
func TestSelfHostMethodResultFieldReclaimX86_64(t *testing.T) {
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

	for _, tc := range methodResultAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d (98 = result box leaked; 99 = over-release; 95/96 = a refused result was freed)", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostMethodResultFieldReclaimArm64 runs the same cases through the
// arm64 IR driver under qemu.
func TestSelfHostMethodResultFieldReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range methodResultAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-ir", "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d (98 = result box leaked; 99 = over-release; 95/96 = a refused result was freed)", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostMethodResultFieldReclaimWasm runs the SAFETY cases on the wasm IR
// backend, where __rc_underflow() is the witness.
func TestSelfHostMethodResultFieldReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host method-result field reclaim wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range methodResultFieldCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("method-result field reclaim wasm %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
