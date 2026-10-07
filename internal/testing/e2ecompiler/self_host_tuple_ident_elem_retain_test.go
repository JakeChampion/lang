package e2ecompiler

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
// A tuple literal holding a bare ident that names an rc-container local
// retains it, so the tuple box is a second owner of the buffer. That reference
// must be given back when the tuple dies; left held, it stranded the buffer at
// rc 1 forever:
//
//	(i32, i32[]) from a bare ident   allocs=400 frees=200   live 8000 (40 B/round)
//	(i32[], i32[]) two idents        allocs=600 frees=200   live 16000
//
// The release must cover exactly the positions the construction retained, not
// every position of rc type: releasing a position the construction never
// retained is the use-after-free TestSelfHostRcTupleSweepHazardsX86_64 pins.
//
// Two shapes must stay correct, and both are covered below: a PARAM ident
// element (the retain is on a buffer the frame does not own, so it must be
// released exactly once), and an untaken-branch tuple whose slot still holds
// its entry zero at the exit (a null tuple's elements must not be read).
//
// Every want below was confirmed against bin/fern -interp, never read off the
// self-host run under test.

type tupIdentElemCase struct {
	name string
	src  string
	want int
}

func tupIdentElemCases() []tupIdentElemCase {
	return []tupIdentElemCase{
		{
			// The main shape: one bare-ident array element.
			name: "ident_elem_array",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.0 + t.1[0] + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 10,
		},
		{
			// Two retained positions in one tuple — the release must walk both, so a
			// per-position list rather than a single flag.
			name: "two_ident_elems",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32[], i32[]) = (xs, ys);
    return t.0[0] + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 74,
		},
		{
			// The source local is READ after the tuple is built. The release is at
			// scope exit, after every use, so the read must still see live bytes —
			// this is the case a release emitted at construction would corrupt.
			name: "ident_read_after",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.1[0] + xs[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 40,
		},
		{
			// A PARAM ident element: the construction retains a buffer the
			// frame does not own, so it must be released exactly once. Getting
			// it wrong over-releases the caller's live array, which the
			// underflow counter would catch.
			name: "param_ident_elem",
			src: `function use_it(xs: i32[], i: i32): i32 {
    let t: (i32, i32[]) = (i, xs);
    return t.1[0] + t.1[1];
}
function main(): i32 { let xs: i32[] = [7, 11]; let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + use_it(xs, r); r = r + 1; } return x % 83; }`,
			want: 57,
		},
		{
			// The tuple's last mention is NOT the final statement, so the
			// precise drop-on-last-use fires as LIVE code instead of being
			// emitted after the return as dead code. That path frees the box and
			// ZEROES the slot, so the exit sweep — the one that does replay the
			// element kinds — then finds null and releases nothing. The two are
			// alternatives, not a sequence, and every release site that can claim
			// a "TUP:" box has to give the element retains back.
			//
			// This is most real code: any use of the tuple other than in the
			// final return reaches it.
			name: "last_use_before_return",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = t.1[0];
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53,
		},
		{
			// The same path reached through a SCALAR element read. Nothing about
			// the tuple's own use is rc-relevant here — it is purely that `t` is
			// mentioned before the last statement — which is what rules out the
			// extraction gate as the cause and pins it on the drop site.
			name: "last_use_scalar_read",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = t.0;
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53,
		},
		{
			// Two same-named tuple locals in SIBLING BLOCKS, retaining at
			// DIFFERENT positions. Each binding must release the position it
			// retained: mixing them up by name releases position 1 of a tuple
			// that retained position 0 — a 4000-byte strand, and a live-buffer
			// release once the positions disagree about what is owned.
			name: "same_name_sibling_blocks",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { let t: (i32[], i32) = (xs, i); acc = acc + t.0[1]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
		{
			// The tuple is built only on one branch, so on the other the slot still
			// holds its entry zero when the sweep runs. __fern_rc_dec null-guards the
			// box; op_tuple_get would dereference. A missing guard here is a segfault,
			// not a leak, so this case is about surviving at all.
			name: "untaken_branch_null",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i % 2 == 0) { let xs: i32[] = [i, i + 1]; let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 43,
		},
	}
}

// TestSelfHostTupleIdentElemExtractionHazardX86_64 — a tuple whose owned-pointer
// element is EXTRACTED must not earn the element release.
//
// `return t.1` / `let u = t.1` hands the element's reference to a new owner, so
// releasing it at the tuple's scope exit releases a reference the frame no longer
// holds — an over-release that reads as **exit 99** (rc underflow) against the
// interpreter's 40.
//
// Each probe ends with an explicit `__rc_underflow_count()` check, and that is the
// essential part: WITHOUT it both cases pass on a compiler that over-releases,
// because a doubly-released block goes back to the freelist and the arithmetic
// still comes out at 40. The counter is the only thing that separates the two
// readings.
//
// These assert the ANSWER, not leak counts: a wrongly-granted release here is
// a wrong answer or a crash, not a number.
//
// Both wants came from bin/fern -interp.
func TestSelfHostTupleIdentElemExtractionHazardX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// THE over-release. The extracted element leaves the frame and the
			// caller binds it to an exit-swept slot, so a release here is the
			// second claim on one reference. The decoy allocation recycles the
			// freed block, so a surviving bug corrupts the value as well as
			// tripping the counter.
			name: "elem_extracted_escaping",
			src: `function grab(i: i32): i32[] {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.1;
}
function decoy(n: i32): i32[] { return [n * 7, n * 11, n * 13]; }
function round(i: i32): i32 {
    let u: i32[] = grab(i);
    let d: i32[] = decoy(i);
    if (d[0] < 0) { return 0; }
    return u[0] + u[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
		{
			// The same extraction without leaving the frame. Refused too — the
			// gate is about a second owner existing, not about where it lives.
			name: "elem_extracted_local",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let u: i32[] = t.1;
    return u[0] + u[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tupextract_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d (99 = rc underflow: the element release "+
					"claimed a reference the frame handed to another owner)", tc.name, exit, tc.want)
			}
		})
	}
}

// TestSelfHostTupleIdentElemRetainX86_64 — the retained element references are
// given back, so allocs and frees balance exactly.
//
// allocs == frees is the essential assertion in both directions. frees short
// of allocs is the leak this closes; frees ABOVE allocs would mean the sweep and
// the rebind store both claimed one reference, which is a double free.
func TestSelfHostTupleIdentElemRetainX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
