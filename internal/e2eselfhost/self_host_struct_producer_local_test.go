package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A struct producer that returns a LOCAL (#7343) --------------------------
//
// `function mk(): P { var p: P = P { xs: [..] }; return p; }` handed its caller
// a box no reclaim credit covered, so every `var v: P = mk()` leaked the struct,
// its buffer and its string: 128 B/round, unbounded, against 0 on native and
// interp. The byte-identical producer that returns the literal DIRECTLY was
// already admitted, so one extra statement in the callee was the whole
// difference.
//
// The gate is `return_value_is_strictfresh_struct`, which had an ExprStructLit
// arm and an ExprCall forwarding arm and no ExprIdent arm at all. It already
// receives `fnbody`, `fnparams`, `arr_fresh`, `fwd` and `sfok`, so the proof
// needed no signature change — the issue's own "signature change rather than a
// two-line edit" framing named a different predicate (`fresh_struct_ret_fns_of`,
// the LOOSE registry, consumed only by snapshot_local_names_of).
//
// WHY THIS WAITED FOR #7349. Applied to the name-keyed table, this exact
// widening SIGSEGVd — `sibling_alias` below at exit 139, not an rc counter,
// because the same-named alias inherited the newly granted credit and freed the
// caller's box. Site-keying the struct credit was a precondition, not a parallel
// cleanup, and this suite pins that it now holds: `sibling_alias` and
// `sibling_alias_renamed` measure identically.
//
// The refusals are half the suite, because a widening is only as good as what it
// still declines. A returned PARAMETER, an aliased local, a reassigned one, two
// declarations of the name, and a receiver call that moves a field out are all
// still refused and still leak — unchanged, byte for byte.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type structProducerCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func structProducerCases() []structProducerCase {
	return []structProducerCase{
		{
			// THE REPRO (#7343). `mk()` binds a strict-fresh struct literal to
			// a local and returns it — the form real code has, since anything that
			// assembles fields before handing them back cannot use the literal-return
			// form. Base: allocs=800 frees=0, 25600 over 200 rounds, unbounded.
			name: "producer_returns_local",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { var p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(i: i32): i32 { var v: P = mk(); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 200) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 19, allocs: 800, frees: 800,
		},
		{
			// CONTROL — the same producer returning the literal DIRECTLY, which
			// was already admitted. Unchanged.
			name: "producer_returns_literal",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { return P { xs: [1,2,3], s: w("p") }; }
function round(i: i32): i32 { var v: P = mk(); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 200) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 19, allocs: 800, frees: 800,
		},
		{
			// CONTROL — no producer at all. Unchanged.
			name: "inline_no_producer",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }

function round(i: i32): i32 { var v: P = P { xs: [1,2,3], s: w("p") }; return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 200) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 19, allocs: 800, frees: 800,
		},
		{
			// The smallest shape: one rc-array field, no string. Base 200/0
			// (8800).
			name: "minimal_array_field",
			src: `struct P { xs: i32[] }
function mk(): P { var p: P = P { xs: [1, 2, 3] }; return p; }
function round(i: i32): i32 { var v: P = mk(); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 51, allocs: 200, frees: 200,
		},
		{
			// THE OVER-RELEASE GUARD, and the reason this fix waited for
			// #7349. Two same-named `v`, one from the producer and one aliasing a
			// parameter. Applying THIS widening to the name-keyed table SIGSEGVd —
			// exit 139, not a counter — because the alias inherited the newly
			// granted credit and freed the caller's box. With the credit site-keyed
			// it is exit 1 and measures identically to its rename control.
			name: "sibling_alias",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { var p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(base: P, i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: P = mk();  t = t + v.xs.len(); }
    if (i % 2 == 1) { var v: P = base;  t = t + v.xs.len(); }
    return t;
}
function main(): i32 {
    var b: P = P { xs: [7, 8], s: w("b") };
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 1, allocs: 204, frees: 200,
		},
		{
			// Its pairwise witness. The colliding program above now matches
			// these numbers exactly, which is checkable without deciding whether the
			// residual 120 bytes are right — they are not this issue's.
			name: "sibling_alias_renamed",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { var p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(base: P, i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: P = mk();  t = t + v.xs.len(); }
    if (i % 2 == 1) { var u: P = base;  t = t + u.xs.len(); }
    return t;
}
function main(): i32 {
    var b: P = P { xs: [7, 8], s: w("b") };
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 1, allocs: 204, frees: 200,
		},
		{
			// ADMITTED, and deliberately so after being measured rather than
			// assumed. `var stolen: i32[] = p.xs;` before `return p` takes a second
			// reference to the buffer the caller's deep drop frees — but `stolen` is
			// a local of the producer's own frame and dies at its exit, so the drop
			// is balanced: 200/200 at live_bytes 0, underflow 0, both oracles
			// agreeing, against 200/0 (8000) before.
			//
			// Worth knowing precisely: NEITHER moves_fields_stmts NOR
			// optstruct_body_moves_field reports this shape — a bare field READ is
			// the blind spot #7259 records — so it is admitted on the measurement
			// plus frame locality, not on a predicate. If this class ever
			// over-releases, this row is the first suspect.
			name: "field_read_admitted",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { var p: P = P { xs: [i, i + 1] }; var stolen: i32[] = p.xs; return p; }
function round(i: i32): i32 { var v: P = mk(i); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 200,
		},
		{
			// REFUSED — `return p` where p is a PARAMETER. The caller owns that
			// box; this is the `return self` shape the registry exists to refuse.
			// Still leaks 8000, unchanged.
			name: "refused_param_returned",
			src: `struct P { xs: i32[] }
function mk(p: P): P { return p; }
function round(i: i32): i32 { var src: P = P { xs: [i, i + 1] }; var v: P = mk(src); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 0,
		},
		{
			// REFUSED — a second binding aliases the local before it is
			// returned, so two references leave the frame. Unchanged.
			name: "refused_aliased",
			src: `struct P { xs: i32[] }
function keepit(q: P): i32 { return q.xs.len(); }
function mk(i: i32): P { var p: P = P { xs: [i, i + 1] }; var alias: P = p; return p; }
function round(i: i32): i32 { var v: P = mk(i); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 0,
		},
		{
			// REFUSED — the local is reassigned, so its declaration's init is not
			// the whole story about the final value. Unchanged.
			name: "refused_reassigned",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { var p: P = P { xs: [i, i + 1] }; p = P { xs: [i + 2, i + 3] }; return p; }
function round(i: i32): i32 { var v: P = mk(i); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 400, frees: 0,
		},
		{
			// REFUSED — two declarations of the same name in the body, so the
			// init witness could be a shadowed sibling. Same first condition, same
			// reason, as arr_field_ident_is_frame_built. Unchanged.
			name: "refused_two_declarations",
			src: `struct P { xs: i32[] }
function mk(i: i32): P {
    if (i % 2 == 0) { var p: P = P { xs: [i, i + 1] }; return p; }
    var p: P = P { xs: [i + 2, i + 3] };
    return p;
}
function round(i: i32): i32 { var v: P = mk(i); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 0,
		},
		{
			// REFUSED — a method call on the local moves a field into the
			// result (`P { xs: p.xs }`), so the returned box no longer sole-owns it.
			// This is the row that proves the field-move guard is live rather than
			// decorative: base and after both 300/0, 12000.
			name: "refused_receiver_field_move",
			src: `struct P { xs: i32[] }
function (p: P) grow(): P { return P { xs: p.xs }; }
function mk(i: i32): P { var p: P = P { xs: [i, i + 1] }; var q: P = p.grow(); return p; }
function round(i: i32): i32 { var v: P = mk(i); return v.xs.len(); }
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 0,
		}}
}

// TestSelfHostStructProducerLocalX86_64 — a producer that returns a local earns
// its caller the same reclaim credit the literal-returning form does, and the
// shapes that must stay refused still are.
func TestSelfHostStructProducerLocalX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structProducerCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = the "+
					"segfault this widening caused on the name-keyed table)", tc.name, exit, tc.want)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. MORE on a refused row means the "+
					"widening reached a shape it must decline; FEWER on an admitted row "+
					"means the credit stopped resolving", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStructProducerLocalWasmIR — the wasm sibling. Exit codes only: the
// leak rows do not move one, so what this leg catches is a release that frees a
// LIVE box on wasm.
func TestSelfHostStructProducerLocalWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct producer-local wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structProducerCases() {
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
			watFile := filepath.Join(dir, "structprod_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct producer-local wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStructProducerLocalIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStructProducerLocalIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structProducerCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "structprod_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
