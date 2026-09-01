package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A string[]-field FORWARDER is read at its call sites, not at its return --
//
// `function (h: Holder) get(): string[] { return h.xs; }` is a read of `h.xs`,
// and the string[]-field admission walk marked it. The mark is keyed on the
// TYPE, so ONE such declaration anywhere in the program refused
// __struct_drop_Holder for every Holder in every function — and the method
// never had to run. 288 B/round, unbounded, on a loop that only constructs the
// struct (#7417).
//
// The verdict moves to the call sites. strarrfld_forwarders_of registers every
// function whose returns all hand out one borrowed string[] field; inside such a
// function the forwarding return is exempt, and at a CALL of one the walk
// applies its existing read rules to the forwarded field — a `.len()` receiver
// admits, everything else marks. So `keep.get().len()` is the borrow
// `keep.xs.len()` already was, and `var g = keep.get()` is the escape
// `var g = keep.xs` already was.
//
// Every want was confirmed against native x86-64 and `bin/fern -interp`, which
// agree on every row. Native allocates a different number of boxes for the same
// source, so its COUNTS are not a comparison — its ANSWERS are, and they match
// on every row. Native is flat at zero everywhere except
// forwarder_element_escapes_by_call, which leaks there too. Exit 99 is reserved for
// __rc_underflow_count(). All thirteen rows were also run under FERN_SANITIZE=1
// + FERN_RC_UNDERFLOW_TRAP=1 + FERN_RC_FREE_DEBUG=1: the five admitted rows are
// silent, and the refused ones report only the leak.
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. A pre-fusion number quoted in a row note below is
// twice its pin.

type strArrFwdCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func strArrFwdCases() []strArrFwdCase {
	return []strArrFwdCase{
		{
			// The headline shape. `keep.get().len()` is a whole-array borrow
			// one step further out than `keep.xs.len()`, and it now reads as
			// one. Base: 800/100, 288 B/round unbounded.
			name: "forwarder_called_len",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			// #7417's finding, and what made it worth its own issue: the
			// method is DECLARED and never runs, `main` does nothing to the
			// struct at all, and the declaration alone cost every Holder in
			// the program its field reclaim. Base: 800/100.
			name: "forwarder_declared_never_called",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + 3;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			// The same forwarder as a FREE function taking the struct by
			// parameter. Registered under "f:", marked at the call by name.
			// Base: 800/100.
			name: "free_fn_forwarder_called_len",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function grab(h: Holder): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + grab(keep).len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			// THE SOUNDNESS ROW for the admitted case. `live` is built once
			// and borrowed through the forwarder on every round while 100
			// admitted Holders are deep-freed around it. A field reclaim that
			// reached a borrowed buffer would show up in the answer, which is
			// native's 1, or in __rc_underflow_count() as a 99. Base:
			// 806/101.
			name: "forwarder_holder_outlives_churn",
			src: `struct Holder { xs: string[] }
function (h: Holder) get(): string[] { return h.xs; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    var live: Holder = Holder { xs: [w("y"), w("z")] };
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len() + live.get().len();
        i = i + 1;
    }
    t = t + live.get().len() * 7;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 1, allocs: 504, frees: 504,
		},
		{
			// The control that was already clean: the identical field read
			// with no forwarder declared anywhere. Flat before and after, so
			// the rows above are about the forwarder and not about the read.
			name: "no_forwarder_direct_read",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.xs.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			// NEGATIVE CONTROL. The result is BOUND, so the caller holds the
			// buffer past the call and the type must stay refused. If this
			// reaches 800/800 the deep free is dangling a live alias.
			name: "forwarder_result_bound",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        var got: string[] = keep.get();
        t = t + got.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 100,
		},
		{
			// NEGATIVE CONTROL. `keep.get()[0]` binds an ELEMENT — the exact
			// alias the scan exists to refuse, laundered through a call.
			// A direct `keep.xs[0]` was already caught; this is the route that
			// was not.
			name: "forwarder_element_read",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        var s0: string = keep.get()[0];
        t = t + s0.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 18, allocs: 500, frees: 100,
		},
		{
			// NEGATIVE CONTROL. `for s in keep.get()` binds every element in
			// turn.
			name: "forwarder_iterated",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        for s in keep.get() { t = t + s.len(); }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 16, allocs: 500, frees: 100,
		},
		{
			// NEGATIVE CONTROL, free-function half.
			name: "free_fn_forwarder_result_bound",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function grab(h: Holder): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        var got: string[] = grab(keep);
        t = t + got.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 100,
		},
		{
			// NEGATIVE CONTROL, one frame deeper: the element leaves through
			// a second function's return. Native leaks this shape too
			// (500/400), so only the exit code is an oracle here.
			name: "forwarder_element_escapes_by_call",
			src: `struct Holder { xs: string[] }
function (h: Holder) get(): string[] { return h.xs; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function stash(h: Holder): string { return h.get()[0]; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        var got: string = stash(keep);
        t = t + got.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 18, allocs: 500, frees: 100,
		},
		{
			// The registry is single-valued: a body whose returns forward two
			// DIFFERENT fields is not a forwarder, so both reads mark as before
			// and the type stays refused. Admitting it would need a mark set
			// per call site, which no call site can choose between.
			name: "two_field_forwarder_not_registered",
			src: `struct Holder { xs: string[], ys: string[] }
function (h: Holder) get(pick: boolean): string[] { if (pick) { return h.xs; } return h.ys; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b")], ys: [w("c")] };
        t = t + keep.get(true).len() + keep.get(false).len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 600, frees: 100,
		},
		{
			// The STORE half is untouched: a bare-ident element shared by two
			// literals is not element-fresh, so the type stays refused however
			// its reads look. Unchanged, and the row that says so.
			name: "shared_element_two_holders",
			src: `struct Holder { xs: string[] }
function (h: Holder) get(): string[] { return h.xs; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var s: string = w("a");
        var h1: Holder = Holder { xs: [s, s] };
        var h2: Holder = Holder { xs: [s] };
        t = t + h1.get().len() + h2.get().len() + s.len();
        var j1: string = w("p");
        var j2: string = w("q");
        t = t + (s[0] as i32) + s.len() + j1.len() - j1.len() + j2.len() - j2.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 4, allocs: 700, frees: 400,
		},
		{
			// A tolerated forwarder borrow and a refused direct element read
			// in one body. The refusal wins — the marks are a union — and the
			// values still read back as native's 6 after three allocations of
			// churn.
			name: "forwarder_len_plus_direct_element",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len();
        var j1: string = w("p");
        var j2: string = w("q");
        var j3: string = w("r");
        t = t + (keep.xs[0][0] as i32) + (keep.xs[2][0] as i32) + keep.xs[1].len()
            + j1.len() - j1.len() + j2.len() - j2.len() + j3.len() - j3.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 6, allocs: 800, frees: 400,
		},
	}
}

// TestSelfHostStrArrFieldForwarderX86_64 is the leak-accounting leg.
func TestSelfHostStrArrFieldForwarderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFwdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "safwd_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; any other change means a "+
					"reclaim reached a buffer the program still reads)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER on an admitted row means the forwarder "+
					"stopped being recognised and its type is stranded again; MORE on a negative "+
					"control means the widening admitted a call that hands the buffer out",
					tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStrArrFieldForwarderWasmIR — exit codes only, so what this leg
// catches is a reclaim that frees a LIVE buffer on wasm, the 99 included.
func TestSelfHostStrArrFieldForwarderWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[]-field forwarder wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strArrFwdCases() {
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
			watFile := filepath.Join(dir, "safwd_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("string[]-field forwarder wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldForwarderIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrArrFieldForwarderIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFwdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "safwd_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
