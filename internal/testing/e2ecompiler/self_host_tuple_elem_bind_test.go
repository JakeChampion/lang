package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Binding an rc-tuple ELEMENT to a local keeps the tuple's release (#7766) --
//
// `let e: T = t.1` where the frame KEEPS `e`: at the exit sweep `e` is dead, so
// the tuple box and its element buffer are both released rather than leaked
// (80 B/round, unbounded). An element that is returned, stored, or rebound
// hands its reference to a NEW owner instead, and the deep free must not
// release it under that owner — `return t.1` exits 99 when it does.
//
// The `refuses_*` rows bind an element that is returned, stored, or rebound;
// each must exit its oracle answer and stay sanitizer-clean. Every row
// balances.
//
// Every row is gated on `__rc_underflow_count()` and runs a second leg under
// FERN_SANITIZE=1: a deep free is the shape that double-frees, and the census
// cannot see an over-release into a freelist.
//
// Every want was confirmed against `bin/fern -interp`.
type tupleElemBindCase struct {
	name string
	src  string
	want int
}

func tupleElemBindMain(rounds string) string {
	return "\nfunction main(): i32 { let x: i32 = 0; let r: i32 = 0; " +
		"while (r < " + rounds + ") { x = x + round(r); r = r + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"
}

func tupleElemBindCases() []tupleElemBindCase {
	const bindElem = `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let e: i32[] = t.1;
    return e.len() + i;
}`
	return []tupleElemBindCase{
		{
			// THE REPRO. Was 200/0 live 8000 — box and buffer both stranded.
			name: "elem_bound_to_a_local",
			src:  bindElem + tupleElemBindMain("100"),
			want: 4,
		},
		{
			// The same shape at 4x the rounds. Was 800/0 live 32000 — the row that
			// makes the unboundedness a fact rather than an inference, which a
			// single count cannot show.
			name: "elem_bound_400_rounds",
			src:  bindElem + tupleElemBindMain("400"),
			want: 7,
		},
		{
			// A STRING element: a different release from the flat buffer dec, so
			// it needs its own row. Was 300/0 live 7200.
			name: "string_elem_bound",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: (i32, string) = (i, w("ab"));
    let e: string = t.1;
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 21,
		},
		{
			// The element is read after binding and then dead, which is the shape
			// the narrowing is actually about: `e` is live across statements and
			// still finished before the exit sweep.
			name: "elem_bound_used_then_dead",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let e: i32[] = t.1;
    let n: i32 = e.len() + e[0];
    return n + i;
}` + tupleElemBindMain("100"),
			want: 57,
		},
		{
			// A THREE-element tuple with a second rc element the bind does not
			// touch: the deep free still has to reach the one that was not bound.
			// Was 400/0 live 12000.
			name: "three_elem_tuple_one_bound",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: (i32, i32[], string) = (i, [i, i + 1], w("ab"));
    let e: i32[] = t.1;
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 4,
		},
		{
			// LOOP-RESIDENT: tuple and bind both re-made each iteration, so the
			// release has to land per round rather than once at function exit.
			// Was 600/0 live 24000.
			name: "elem_bound_in_a_loop",
			src: `function round(i: i32): i32 {
    let n: i32 = 0;
    let k: i32 = 0;
    while (k < 3) { let t: (i32, i32[]) = (i, [i, k]); let e: i32[] = t.1; n = (n + e.len()) % 101; k = k + 1; }
    return n + i;
}` + tupleElemBindMain("100"),
			want: 72,
		},
		{
			// The bind is in an IF ARM while the tuple outlives it.
			name: "elem_bound_in_a_conditional",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let n: i32 = 0;
    if (i % 2 == 0) { let e: i32[] = t.1; n = e.len(); }
    return n + i;
}` + tupleElemBindMain("100"),
			want: 70,
		},
		{
			// The element is RETURNED, so it outlives the frame and a deep
			// free would release it under the caller.
			name: "refuses_elem_returned",
			src: `function esc(i: i32): i32[] { let t: (i32, i32[]) = (i, [i, i + 1]); return t.1; }
function round(i: i32): i32 { return esc(i).len() + i; }` + tupleElemBindMain("100"),
			want: 4,
		},
		{
			// The element is STORED into a container that outlives the
			// bind.
			name: "refuses_elem_stored",
			src: `function sink(xs: i32[][]): i32 { return xs.len(); }
function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let e: i32[] = t.1;
    let held: i32[][] = [e];
    return sink(held) + i;
}` + tupleElemBindMain("100"),
			want: 70,
		},
		{
			// The target is REASSIGNED, so its final value is not the
			// element bound.
			name: "refuses_elem_reassigned",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let e: i32[] = t.1;
    e = [i];
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 70,
		},
		{
			// The ALIAS side (#7466): the same element bind reached through a
			// plain alias of the tuple. `v.1` is a borrow, so the release stays
			// with `t` and the bind balances — the element's own accounting is
			// independent of the box pair. Pinned as CLEAN.
			name: "elem_bound_through_alias",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let v: (i32, i32[]) = t;
    let e: i32[] = v.1;
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 4,
		},
		{
			// Through the alias: the element escapes the frame by the
			// alias's own return.
			name: "refuses_elem_returned_through_alias",
			src: `function esc(i: i32): i32[] { let t: (i32, i32[]) = (i, [i, i + 1]); let v: (i32, i32[]) = t; return v.1; }
function round(i: i32): i32 { return esc(i).len() + i; }` + tupleElemBindMain("100"),
			want: 4,
		},
		{
			// Controls that were already clean and must stay so: a BORROW of the
			// element rather than a bind, and a SCALAR element bind, neither of
			// which the gate ever refused.
			name: "elem_borrow_unchanged",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    return t.1.len() + i;
}` + tupleElemBindMain("100"),
			want: 4,
		},
		{
			name: "scalar_elem_bind_unchanged",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let e: i32 = t.0;
    return e + t.1.len() + i;
}` + tupleElemBindMain("100"),
			want: 57,
		},
	}
}

// TestSelfHostTupleElemBindX86_64 — every admitted row balances at live_bytes 0
// with no rc underflow, on the census leg and again under the quarantining
// allocator.
func TestSelfHostTupleElemBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleElemBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupelembind_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the deep free "+
					"released an element a live local still holds)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A "+
					"short free count is the element bind costing the tuple its "+
					"whole credit again", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupelembind_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

// TestSelfHostTupleElemBindWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostTupleElemBindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple element-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupleElemBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "tupelembind_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple element-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleElemBindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleElemBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleElemBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupelembind_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("tuple element-bind arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
