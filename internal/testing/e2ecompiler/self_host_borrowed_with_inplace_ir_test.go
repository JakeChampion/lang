package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostBorrowedWithInPlaceIRX86_64(t *testing.T) {
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

	run(t, `struct St { seen: i32[], n: i32 }
function mark(st: St, pc: i32): St {
    let sn: i32[] = st.seen;
    if (sn[pc] == 1) { return St { seen: sn, n: 0 }; }
    sn = sn.with(pc, 1);
    return St { seen: sn, n: 1 };
}
function main(): i32 {
    let st: St = St { seen: [0, 0, 0, 0], n: 0 };
    let a: St = mark(st, 2);
    let b: St = mark(st, 2);
    return a.n * 10 + b.n;
}`, "borrowed-struct-param-field", 11)

	run(t, `struct St { seen: i32[], n: i32 }
function main(): i32 {
    let st: St = St { seen: [0, 0, 0, 0], n: 0 };
    let sn: i32[] = st.seen;
    sn = sn.with(2, 1);
    return st.seen[2] * 10 + sn[2];
}`, "local-struct-field", 1)

	run(t, `struct St { seen: i32[], n: i32 }
function bump(own st: St, pc: i32): St {
    let sn: i32[] = st.seen;
    st = St { ...st, seen: [] };
    sn = sn.with(pc, 1);
    return St { seen: sn, n: 1 };
}
function main(): i32 {
    let a: St = bump(St { seen: [0, 0, 0, 0], n: 0 }, 2);
    return a.seen[2] * 10 + a.n;
}`, "own-param-field-cleared", 11)

	run(t, `struct St { xs: i32[], n: i32 }
function grow(st: St, v: i32): i32 {
    let a: i32[] = st.xs;
    a = a.append(v);
    return a.len();
}
function main(): i32 {
    let st: St = St { xs: [1, 2], n: 0 };
    let p: i32 = grow(st, 9);
    let q: i32 = grow(st, 8);
    return p * 10 + q;
}`, "append-unaffected", 33)

	run(t, `function fill(own buf: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) { buf = buf.with(i, i); i = i + 1; }
    return buf;
}
function main(): i32 {
    let b: i32[] = [];
    let k: i32 = 0;
    while (k < 4000) { b = b.append(0); k = k + 1; }
    let before: i64 = __heap_bump_bytes();
    b = fill(b, 4000);
    let after: i64 = __heap_bump_bytes();
    if (b[3999] != 3999) { return 90; }
    // Any cloning at all shows up here: one clone is 16 KB, and the loop would
    // do 4000 of them. Allow 64 KB of slack for unrelated allocation.
    if (after - before > 65536) { return 91; }
    return 7;
}`, "own-param-with-loop-allocates-nothing", 7)

	run(t, `function run(heap_in: i32[], at: i32, v: i32): i32[] {
    let heap: i32[] = heap_in;
    heap = heap.with(at, v);
    return heap;
}
function main(): i32 {
    let h: i32[] = [0, 0, 0, 0];
    let r: i32[] = run(h, 1, 7);
    return h[1] * 10 + r[1];
}`, "rebind-of-borrowed-param", 7)

	run(t, `function main(): i32 {
    let a: i32[] = [0, 0, 0, 0];
    let b: i32[] = a;
    b = b.with(1, 7);
    return a[1] * 10 + b[1];
}`, "rebind-of-live-local", 7)

	run(t, `function fill(own heap_in: i32[], n: i32): i32[] {
    let heap: i32[] = heap_in;
    let i: i32 = 0;
    while (i < n) { heap = heap.with(i, i); i = i + 1; }
    return heap;
}
function main(): i32 {
    let b: i32[] = [];
    let k: i32 = 0;
    while (k < 4000) { b = b.append(0); k = k + 1; }
    let before: i64 = __heap_bump_bytes();
    b = fill(b, 4000);
    let after: i64 = __heap_bump_bytes();
    if (b[3999] != 3999) { return 90; }
    if (after - before > 65536) { return 91; }
    return 7;
}`, "own-param-rebind-loop-allocates-nothing", 7)

	run(t, `function run(heap: i32[], at: i32, v: i32): i32[] {
    heap = heap.with(at, v);
    return heap;
}
function main(): i32 {
    let h: i32[] = [0, 0, 0, 0];
    let r: i32[] = run(h, 1, 7);
    return h[1] * 10 + r[1];
}`, "direct-borrowed-param", 7)

	run(t, `function main(): i32 {
    let k: i32 = 5;
    let g: (i32[]) => i32 = (a: i32[]): i32 => { a = a.with(0, 9); return a[0] + k; };
    let b: i32[] = [1, 2];
    let r: i32 = g(b);
    return b[0] * 10 + r;
}`, "lifted-lambda-declared-array-param", 24)

	run(t, `function main(): i32 {
    let x: i32 = 0;
    let f: () => i32 = (): i32 => { x = x + 4; return 0; };
    x = 3;
    let r: i32 = f();
    return x + 35;
}`, "mut-capture-cell-param-writes-through", 42)
}
