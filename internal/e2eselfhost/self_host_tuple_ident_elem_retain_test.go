package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- The bare-ident tuple element's retain, given back (#7226) ---------------
//
// lower_expr's ExprTuple arm retains an element that is a bare ident naming an
// rc-container local (gated on slot_is_rc_container), so the tuple box is a
// second owner and __fern_rc_is_unique cannot call the source local unique while
// the tuple still points at its buffer. Nothing gave that reference back: the
// only dec was the is_arr sweep's, which covers the LOCAL's own reference, so
// incs 1 / decs 1 against a start rc of 1 left the buffer at 1 forever.
//
//	(i32, i32[]) from a bare ident   allocs=400 frees=200   live 8000 (40 B/round)
//	(i32[], i32[]) two idents        allocs=600 frees=200   live 16000
//
// against 0 on native for all of them.
//
// The release is keyed on what the retain actually did rather than on the
// element's type: bind_var_slot records the retained positions from the SAME
// slot_is_rc_container test, and the scope-exit sweep and the rebind store both
// replay exactly that list. Recording it is what makes the pair safe — a
// type-driven release would free a position the construction never retained,
// which is the use-after-free TestSelfHostRcTupleSweepHazardsX86_64 pins.
//
// Two shapes must NOT move, and both are covered below: a PARAM ident element
// (the retain is on a buffer the frame does not own, so the dec must still be
// emitted — but exactly once), and an untaken-branch tuple whose slot still holds
// its entry zero at the sweep (the box dec tolerates null because __fern_rc_dec
// null-guards; the op_tuple_get that reaches an element does not, so the walk
// carries its own guard).
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run under
// test.

type tupIdentElemCase struct {
	name string
	src  string
	want int
}

func tupIdentElemCases() []tupIdentElemCase {
	return []tupIdentElemCase{
		{
			// The headline shape: one bare-ident array element.
			name: "ident_elem_array",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    return t.0 + t.1[0] + t.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 10,
		},
		{
			// Two retained positions in one tuple — the release must walk both, so a
			// per-position list rather than a single flag.
			name: "two_ident_elems",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 2, i + 3];
    var t: (i32[], i32[]) = (xs, ys);
    return t.0[0] + t.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 74,
		},
		{
			// The source local is READ after the tuple is built. The release is at
			// scope exit, after every use, so the read must still see live bytes —
			// this is the case a release emitted at construction would corrupt.
			name: "ident_read_after",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    return t.1[0] + xs[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 40,
		},
		{
			// A PARAM ident element. slot_is_rc_container has no >= n_params guard, so
			// the construction retains a buffer the frame does not own — while the
			// is_arr sweep starts AT n_params and so never dec'd it. Fixing the
			// general case fixes this one; getting it wrong over-releases the
			// caller's live array, which the underflow counter would catch.
			name: "param_ident_elem",
			src: `function use_it(xs: i32[], i: i32): i32 {
    var t: (i32, i32[]) = (i, xs);
    return t.1[0] + t.1[1];
}
function main(): i32 { var xs: i32[] = [7, 11]; var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + use_it(xs, r); r = r + 1; } return x % 83; }`,
			want: 57,
		},
		{
			// The tuple is built only on one branch, so on the other the slot still
			// holds its entry zero when the sweep runs. __fern_rc_dec null-guards the
			// box; op_tuple_get would dereference. A missing guard here is a segfault,
			// not a leak, so this case is about surviving at all.
			name: "untaken_branch_null",
			src: `function round(i: i32): i32 {
    var acc: i32 = 0;
    if (i % 2 == 0) { var xs: i32[] = [i, i + 1]; var t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 43,
		},
	}
}

// TestSelfHostTupleIdentElemRetainX86_64 — the retained element references are
// given back, so allocs and frees balance exactly.
//
// allocs == frees is the load-bearing assertion in both directions. frees short
// of allocs is the leak this closes; frees ABOVE allocs would mean the sweep and
// the rebind store both claimed one reference, which is a double free.
func TestSelfHostTupleIdentElemRetainX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupidentelem_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (both oracles agree on %d)", tc.name, exit, tc.want, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
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
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0. The construction retain is per "+
					"round, so anything stranded here scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly; frees above "+
					"allocs is a double free, not an improvement", tc.name, summary)
			}
		})
	}
}

// TestSelfHostTupleIdentElemRetainWasmIR — the wasm sibling. Exit codes only:
// FERN_LEAKCHECK is x86-64-only, and the answer is what proves the release did
// not free a live buffer.
func TestSelfHostTupleIdentElemRetainWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple ident-elem retain wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
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
			watFile := filepath.Join(dir, "tupidentelem_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple ident-elem retain wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleIdentElemRetainIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleIdentElemRetainIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupidentelem_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
