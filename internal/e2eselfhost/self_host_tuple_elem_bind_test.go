package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Binding an rc-tuple ELEMENT to a local kept its credit (#7766) ----------
//
// `var e: T = t.1` refused `"TUPRC:"` AND `"TUPRCS:"` together, so the local got
// no release at all and the tuple box and its element buffer both leaked:
//
//	rounds   native        self-host (before)
//	100      200/200/0     200/0  live 8000
//	400      800/800/0     800/0  live 32000
//
// 80 B/round, unbounded, `frees=0` — and `FERN_SANITIZE=1` said so directly:
// `fern-sanitizer: leak 8000 bytes in 200 blocks`.
//
// The refusal exists for a real case, which is why it is narrowed rather than
// removed. `rctuple_esc_expr`'s own header gives it: `return t.1` hands the
// element's reference to a NEW owner, so the whole-tuple deep free would release
// it under the caller — witnessed as exit 99 on that shape. That reasoning does
// not reach a bind the frame KEEPS: at the exit sweep `e` is dead, so the deep
// free is releasing memory nothing reads again, and refusing costs the tuple its
// BOX as well as its element.
//
// `tuple_elem_bind_sites_of` admits exactly the binds whose target is neither
// reassigned nor escaping, so the three refusal rows below still refuse — an
// element that is returned, stored, or rebound is not in the set.
//
// Every row is gated on `__rc_underflow()` and runs a second leg under
// FERN_SANITIZE=1. This change WIDENS a deep free, which is the shape that
// double-frees, and the census cannot see an over-release into a freelist —
// `docs/rc-log/` has recorded that four times in this family now.
//
// Every want was confirmed against BOTH oracles: `bin/fern -interp` and the
// native x86-64 backend agreed on each.
type tupleElemBindCase struct {
	name      string
	src       string
	want      int
	balance   bool
	wantFrees int64 // asserted exactly on every row that does not set balance
}

func tupleElemBindMain(rounds string) string {
	return "\nfunction main(): i32 { var x: i32 = 0; var r: i32 = 0; " +
		"while (r < " + rounds + ") { x = x + round(r); r = r + 1; } " +
		"if (__rc_underflow() != 0) { return 99; } return x % 83; }"
}

func tupleElemBindCases() []tupleElemBindCase {
	const bindElem = `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var e: i32[] = t.1;
    return e.len() + i;
}`
	return []tupleElemBindCase{
		{
			// THE REPRO. Was 200/0 live 8000 — box and buffer both stranded.
			name: "elem_bound_to_a_local",
			src:  bindElem + tupleElemBindMain("100"),
			want: 4, balance: true,
		},
		{
			// The same shape at 4x the rounds. Was 800/0 live 32000 — the row that
			// makes the unboundedness a fact rather than an inference, which a
			// single count cannot show.
			name: "elem_bound_400_rounds",
			src:  bindElem + tupleElemBindMain("400"),
			want: 7, balance: true,
		},
		{
			// A STRING element: a different release from the flat buffer dec, so
			// it needs its own row. Was 300/0 live 7200.
			name: "string_elem_bound",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: (i32, string) = (i, w("ab"));
    var e: string = t.1;
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 21, balance: true,
		},
		{
			// The element is read after binding and then dead, which is the shape
			// the narrowing is actually about: `e` is live across statements and
			// still finished before the exit sweep.
			name: "elem_bound_used_then_dead",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var e: i32[] = t.1;
    var n: i32 = e.len() + e[0];
    return n + i;
}` + tupleElemBindMain("100"),
			want: 57, balance: true,
		},
		{
			// A THREE-element tuple with a second rc element the bind does not
			// touch: the deep free still has to reach the one that was not bound.
			// Was 400/0 live 12000.
			name: "three_elem_tuple_one_bound",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: (i32, i32[], string) = (i, [i, i + 1], w("ab"));
    var e: i32[] = t.1;
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 4, balance: true,
		},
		{
			// LOOP-RESIDENT: tuple and bind both re-made each iteration, so the
			// release has to land per round rather than once at function exit.
			// Was 600/0 live 24000.
			name: "elem_bound_in_a_loop",
			src: `function round(i: i32): i32 {
    var n: i32 = 0;
    var k: i32 = 0;
    while (k < 3) { var t: (i32, i32[]) = (i, [i, k]); var e: i32[] = t.1; n = (n + e.len()) % 101; k = k + 1; }
    return n + i;
}` + tupleElemBindMain("100"),
			want: 72, balance: true,
		},
		{
			// The bind is in an IF ARM while the tuple outlives it.
			name: "elem_bound_in_a_conditional",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var n: i32 = 0;
    if (i % 2 == 0) { var e: i32[] = t.1; n = e.len(); }
    return n + i;
}` + tupleElemBindMain("100"),
			want: 70, balance: true,
		},
		{
			// REFUSED, and this is the case the whole gate exists for: the element
			// is RETURNED, so it outlives the frame and a deep free would release
			// it under the caller. Unchanged by this change.
			name: "refuses_elem_returned",
			src: `function esc(i: i32): i32[] { var t: (i32, i32[]) = (i, [i, i + 1]); return t.1; }
function round(i: i32): i32 { return esc(i).len() + i; }` + tupleElemBindMain("100"),
			want: 4, wantFrees: 100,
		},
		{
			// REFUSED: the element is STORED into a container that outlives the
			// bind, so the frame does not keep it after all.
			name: "refuses_elem_stored",
			src: `function sink(xs: i32[][]): i32 { return xs.len(); }
function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var e: i32[] = t.1;
    var held: i32[][] = [e];
    return sink(held) + i;
}` + tupleElemBindMain("100"),
			want: 70, wantFrees: 100,
		},
		{
			// REFUSED: the target is REASSIGNED, so its final value is not the
			// element the credit was reasoned about.
			name: "refuses_elem_reassigned",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var e: i32[] = t.1;
    e = [i];
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 70, wantFrees: 100,
		},
		{
			// The ALIAS side (#7466): the same element bind reached through a
			// plain alias of the tuple. `alias_bind_sites_of` vets the alias with
			// the coarse `body_unsafe_for`, which reads `v.1` as a borrow, so the
			// credit stays with `t` and the bind balances — the element's own
			// accounting is independent of the box pair. Pinned as CLEAN because
			// the element-aware gate the string[] side grew (#7391) would deny
			// this credit outright: porting it as specified trades a balanced
			// shape for a leaking one, which is why it was not done.
			name: "elem_bound_through_alias",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var v: (i32, i32[]) = t;
    var e: i32[] = v.1;
    return e.len() + i;
}` + tupleElemBindMain("100"),
			want: 4, balance: true,
		},
		{
			// REFUSED through the alias too: the element escapes the frame by
			// the alias's own return, so the box is freed and the buffer is
			// correctly stranded — the same half-release as the direct form.
			name: "refuses_elem_returned_through_alias",
			src: `function esc(i: i32): i32[] { var t: (i32, i32[]) = (i, [i, i + 1]); var v: (i32, i32[]) = t; return v.1; }
function round(i: i32): i32 { return esc(i).len() + i; }` + tupleElemBindMain("100"),
			want: 4, wantFrees: 100,
		},
		{
			// Controls that were already clean and must stay so: a BORROW of the
			// element rather than a bind, and a SCALAR element bind, neither of
			// which the gate ever refused.
			name: "elem_borrow_unchanged",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    return t.1.len() + i;
}` + tupleElemBindMain("100"),
			want: 4, balance: true,
		},
		{
			name: "scalar_elem_bind_unchanged",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var e: i32 = t.0;
    return e + t.1.len() + i;
}` + tupleElemBindMain("100"),
			want: 57, balance: true,
		},
	}
}

// TestSelfHostTupleElemBindX86_64 — every admitted row balances at live_bytes 0
// with no rc underflow, on the census leg and again under the quarantining
// allocator; every refused row keeps its exact free count.
func TestSelfHostTupleElemBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A "+
						"short free count is the element bind costing the tuple its "+
						"whole credit again", tc.name, summary)
				}
			} else if frees != tc.wantFrees {
				t.Errorf("%s: %s — refused row's frees moved (want exactly %d). A "+
					"HIGHER count is the refusal breaking down: an element released "+
					"while something outside the frame still holds it", tc.name, summary, tc.wantFrees)
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleElemBindCases() {
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
