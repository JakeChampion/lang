package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- The cross-tuple reuse donor's children, given back (#7275) --------------
//
// emit_cross_tuple_reuse recycles a dead donor tuple's box for a later tuple
// construction. Whatever that box OWNED died with the recycling and nothing gave
// it back: the donor is deliberately excluded from the precise drop-on-last-use
// (the xtuple.donors guard, which stops a free from racing the reuse), and its
// slot is zeroed at the reuse, so the exit sweep finds null too. Two classes of
// child were stranded, one per credit:
//
//	"TUP:"    a bare-ident element's construction retain (#7226)   48 B/round
//	"TUPRCS:" a fresh array-literal element the deep free owns     88 B/round
//
// against 0 on native and interp for both. A third shape sits at the LOOP-CARRIED
// recipient, where the prior iteration's box is released by
// emit_reuse_recip_prior_release rather than by the exit sweep: a shallow box dec
// is the whole release only when the box owns nothing, and the "TUPRCS:" class was
// outside that site's gate entirely, so both the buffer and the box strand. `FERN_SELFHOST_NO_REUSE=1` took every
// shape below to 0 without touching the compiler, which is what attributed them
// to this path rather than to the credit gates.
//
// The release CANNOT sit where the other three sites put theirs. The reuse is
// runtime-guarded on __fern_rc_is_unique(d): on the reuse arm the box is recycled
// and its children are dead, but on the fresh arm the donor's box survives under
// its other owners and the same release would free buffers those owners still
// read — an over-release, strictly worse than the leak. So it sits INSIDE the
// uniqueness test (emit_tup_donor_releases).
//
// The fresh arm is not reachable from a source-level shape: the donor gate
// (cross_tuple_construction_donor) admits only a non-escaping, never-reassigned
// local with no mention after the construction, so its box is provably sole-owned
// and the runtime guard is there to make an ANALYSIS miss degrade rather than
// corrupt. That is why no case below witnesses the fresh arm — the placement is
// defence for a bug that does not exist yet, and asserting it needs a unit on the
// emitted branch, not a program.
//
// The recipient limb is the second half. emit_cross_tuple_reuse tagged c's slot
// from the element EXPRESSIONS, and elem_type_tag coarsens a scalar-array element
// to the bare "i32" — so `(i32, i32[])` was recorded as "i32,i32" and the
// type-driven deep free found no array child. It now prefers the declared tuple
// type, which is what bind_var_slot's ExprTuple arm does on the non-reuse path;
// the two agreeing is the whole fix.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run under
// test.

type tupXReuseElemCase struct {
	name string
	src  string
	want int
	// allocs is the exact allocation count, and it is the non-vacuity guard: it
	// is BELOW what the same program allocates under FERN_SELFHOST_NO_REUSE=1
	// (one box per round is recycled instead of allocated), so a change that
	// stops pairing these shapes fails here rather than passing trivially with
	// nothing to release.
	allocs int64
}

func tupXReuseElemCases() []tupXReuseElemCase {
	return []tupXReuseElemCase{
		{
			// The headline shape: donor and recipient each retain a bare-ident
			// array element. Only the donor's retain was stranded — the
			// recipient's slot is exit-swept normally.
			name: "donor_and_recipient_idents",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var a: i32 = t.1[0];
    var ys: i32[] = [i + 2, i + 3];
    var u: (i32, i32[]) = (i, ys);
    return a + u.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			// The RECIPIENT limb: its element is a fresh array literal, so the
			// exit sweep's type-driven deep free owns it — and could not see it
			// while the reuse tagged the slot from the element expression.
			name: "recipient_array_literal",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var a: i32 = t.1[0];
    var u: (i32, i32[]) = (i, [i + 2, i + 3]);
    return a + u.1[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			// The donor's SOURCE local is read after the reuse. The release gives
			// back the tuple's reference, not the local's, so the read must still
			// see live bytes — this is the case a release of the wrong reference
			// corrupts, and the underflow check is what separates the two.
			name: "donor_source_read_after",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var a: i32 = t.1[0];
    var ys: i32[] = [i + 2, i + 3];
    var u: (i32, i32[]) = (i, ys);
    return a + u.1[1] + xs[1];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 61, allocs: 300,
		},
		{
			// Both positions retained, so the reuse arm walks a list rather than
			// releasing one position.
			name: "two_retained_positions",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 2, i + 3];
    var t: (i32[], i32[]) = (xs, ys);
    var a: i32 = t.0[0] + t.1[1];
    var ps: i32[] = [i + 4, i + 5];
    var qs: i32[] = [i + 6, i + 7];
    var u: (i32[], i32[]) = (ps, qs);
    return a + u.0[1] + u.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 35, allocs: 500,
		},
		{
			// A CHAIN: u recycles t's box and then donates it to v. u is bound by
			// the reuse path, so its own fresh literal is owned by a slot that
			// bind_var_slot never saw — the case that needs the donor release to
			// consult the deep-free class, not only the recorded element kinds.
			name: "chained_donor",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var a: i32 = t.1[0];
    var u: (i32, i32[]) = (i, [i + 2, i + 3]);
    var b: i32 = u.1[1];
    var v: (i32, i32[]) = (i, [i + 4, i + 5]);
    return a + b + v.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 29, allocs: 400,
		},
		{
			// The donor's element is a PARAM ident — a buffer the frame does not
			// own. The construction still retained it (slot_is_rc_container has no
			// n_params guard), so the reuse arm still owes exactly one dec; one too
			// many frees the caller's live array.
			name: "param_ident_donor",
			src: `function feed(xs: i32[], i: i32): i32 {
    var t: (i32, i32[]) = (i, xs);
    var a: i32 = t.1[0];
    var ys: i32[] = [i + 2, i + 3];
    var u: (i32, i32[]) = (i, ys);
    return a + u.1[1];
}
function main(): i32 { var xs: i32[] = [7, 11]; var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + feed(xs, r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 201,
		},
		{
			// A LOOP-CARRIED recipient: `u`'s slot is re-bound every iteration, so
			// its prior box is released by emit_reuse_recip_prior_release rather
			// than by the exit sweep. That is the fifth site on the enumeration,
			// and a shallow box dec is the whole release only when the box owns
			// nothing — here it owns a fresh array literal.
			name: "loop_carried_recipient_literal",
			src: `function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[] = [i, i + 1];
        var t: (i32, i32[]) = (i, xs);
        var a: i32 = t.1[0];
        var u: (i32, i32[]) = (i, [i + 2, i + 3]);
        acc = acc + a + u.1[1];
        i = i + 1;
    }
    return acc;
}
function main(): i32 { var x: i32 = run(100); if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			// The same loop with a bare-ident recipient element. The reuse path
			// takes no element retain, so the source local's own sweep owns the
			// buffer and the prior-box release must NOT claim it — the direction
			// the widened prior-box gate could have got wrong.
			name: "loop_carried_recipient_ident",
			src: `function run(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[] = [i, i + 1];
        var t: (i32, i32[]) = (i, xs);
        var a: i32 = t.1[0];
        var ys: i32[] = [i + 2, i + 3];
        var u: (i32, i32[]) = (i, ys);
        acc = acc + a + u.1[1];
        i = i + 1;
    }
    return acc;
}
function main(): i32 { var x: i32 = run(100); if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			// The RECIPIENT's source local is read after the reuse. The recipient
			// takes a bare ident, so the deep free must NOT claim it — the
			// direction the corrected slot tags could have got wrong.
			name: "recipient_source_read_after",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    var a: i32 = t.1[0];
    var ys: i32[] = [i + 2, i + 3];
    var u: (i32, i32[]) = (i, ys);
    var b: i32 = u.1[1];
    return a + b + ys[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 78, allocs: 300,
		},
	}
}

// TestSelfHostTupleCrossReuseElemX86_64 — the donor's children are given back, so
// allocs and frees balance exactly and the underflow counter stays at 0.
//
// Three assertions, each load-bearing in a different direction. The exit code
// catches a release of a live buffer (99 = the counter tripped, and both oracles
// agree on the want otherwise). allocs == frees catches the leak this closes, and
// frees ABOVE allocs a double free. The exact alloc count catches the reuse
// silently ceasing to pair these shapes, which would make the rest vacuous.
func TestSelfHostTupleCrossReuseElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupXReuseElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupxreuse_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the donor release "+
					"claimed a reference someone else still owns)", tc.name, exit, tc.want)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d. This count is below the no-reuse "+
					"count by one box per round, so a change here means the cross-tuple "+
					"pairing stopped firing and the release assertions below went vacuous",
					tc.name, summary, tc.allocs)
			}
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0. The donor's children are "+
					"stranded per round, so anything here scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly; frees above "+
					"allocs is a double free, not an improvement", tc.name, summary)
			}
		})
	}
}

// TestSelfHostTupleCrossReuseElemWasmIR — the wasm sibling. Exit codes only:
// FERN_LEAKCHECK is x86-64-only, and the answer is what proves the release did
// not free a live buffer.
func TestSelfHostTupleCrossReuseElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple cross-reuse element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupXReuseElemCases() {
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
			watFile := filepath.Join(dir, "tupxreuse_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple cross-reuse element wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleCrossReuseElemIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleCrossReuseElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupXReuseElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupxreuse_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
