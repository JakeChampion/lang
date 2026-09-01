package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The moved-payload dec-skip applies to a hand-over, not to every escape ----
//
// match_moved_rc_payloads drops a (variant, field) into the MOVED set whenever an
// arm binds an rc payload to a name that ESCAPES the arm, and the post-match deep
// drop then SKIPS that field's dec — on the stated theory that "the binding
// inherits the box's counted reference". binding_escapes_arm is body_unsafe_for
// over an EMPTY borrowable registry, so a return, a store to an outer local and a
// call argument all reach that one verdict.
//
// Only the first of those hands the reference over. A store takes its own counted
// claim (one __fern_rc_inc), so the drop's dec lands on that claim rather than on
// zero; skipping it strands one and the payload leaks. A borrow-only callee takes
// no claim at all, so the box is sole owner and the skip suppresses its only dec.
// Measured against native over 100 rounds: the store was 300/200 against 300/300,
// the call argument 200/100 against 200/200.
//
// The narrowing is guarded on both sides, and the guards are the point of this
// file. Removing the skip WHOLESALE — the shape of the fix the counts alone
// suggest, since every row then reports N/N — breaks three ways:
//
//	return escape, conditional or not  -> exit 99, rc underflow (over-release)
//	STRING payload                     -> wrong value (use-after-free)
//	nested STRUCT payload              -> SIGSEGV
//
// The reason is NOT that those drops are unguarded — __fern_str_free reads rc at
// box-8 and only frees at rc==1. It is that their escaping STORE is uncounted: an
// i32[] payload's `keep = xs` emits one __fern_rc_inc, a string payload's and a
// nested struct's emit none, so the box stays sole owner and a dec at the match
// frees a value the alias still reads. Those keep the skip whatever the escape is;
// only a payload whose store retains gives it up, and only when it does not leave
// by return.
//
// Every want below was confirmed against the native x86-64 backend. Exit 99 is
// reserved for __rc_underflow_count().
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. Every row was re-measured against the commit
// before it, and every live_bytes is unchanged — the clean rows stayed clean
// and each refusal-leak row leaks the same bytes — so what moved is block
// volume, not behaviour. A pre-fusion number in a row note below is the older
// one.

type movedSkipCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const mvsMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

const mvsChurnMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func movedSkipCases() []movedSkipCase {
	const decls = `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []movedSkipCase{
		{
			// THE REPRO. `keep = xs` retains, so the payload has two counted
			// owners and the drop's dec would land on 1, not 0. Base: 300/200.
			name: "array_payload_stored_out",
			src: decls + `function round(i: i32): i32 {
    var v: E = mkv(i);
    var keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + mvsMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			// The same shape read back as a VALUE with three fresh arrays after
			// the match, so a payload freed too early would be reused before the
			// read. Counts cannot see a use-after-READ. Native returns 9.
			name: "stored_out_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function mkv(): E { return E.A([7, 8]); }
function round(i: i32): i32 {
    var v: E = mkv();
    var keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    var j1: i32[] = [111, 222];
    var j2: i32[] = [333, 444];
    var j3: i32[] = [555, 666];
    return keep[0] + keep[keep.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
` + mvsChurnMain,
			want: 9, allocs: 120, frees: 120,
		},
		{
			// A BORROW-ONLY callee. It takes no claim, so the box is the payload's
			// sole owner and the skip suppressed its only dec. Base: 200/100.
			name: "array_payload_borrowed_by_callee",
			src: decls + `function sink(a: i32[]): i32 { return a.len() + a[0]; }
function round(i: i32): i32 {
    var v: E = mkv(i);
    var n: i32 = 0;
    match (v) { E.A(xs) => { n = sink(xs); }, E.B => { n = 0; } }
    return n % 101;
}
` + mvsMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			// The call-argument row as a VALUE with churn. Native returns 9.
			name: "borrowed_by_callee_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function mkv(): E { return E.A([7, 8]); }
function sink(a: i32[]): i32 { return a[0] + a[a.len() - 1]; }
function round(i: i32): i32 {
    var v: E = mkv();
    var n: i32 = 0;
    match (v) { E.A(xs) => { n = sink(xs); }, E.B => { n = 0; } }
    var j1: i32[] = [111, 222];
    var j2: i32[] = [333, 444];
    var j3: i32[] = [555, 666];
    return n + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
` + mvsChurnMain,
			want: 9, allocs: 100, frees: 100,
		},
		{
			// A GUARDED arm whose payload is stored out. This row moved without
			// being aimed at: consumed_rcpayload_enum_frees refuses any candidate
			// mixing a guard with a NON-EMPTY moved set (guarded_move), and the
			// set is empty for this shape now, so the candidate is admitted and
			// the free fires. Base: 350/150 — pinned as a gap by #7509.
			name: "guarded_arm_store_now_reclaimed",
			src: decls + `function round(i: i32): i32 {
    var v: E = mkv(i);
    var keep: i32[] = [0];
    match (v) { E.A(xs) when i % 2 == 0 => { keep = xs; }, E.A(ys) => { keep = [ys.len()]; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + mvsMain,
			want: 81, allocs: 350, frees: 350,
		},
		{
			// The guarded row as a VALUE with churn — both arms are exercised
			// (the guard keys on the round index). Native returns 96.
			name: "guarded_arm_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function mkv(): E { return E.A([7, 8]); }
function round(i: i32): i32 {
    var v: E = mkv();
    var keep: i32[] = [0];
    match (v) { E.A(xs) when i % 2 == 0 => { keep = xs; }, E.A(ys) => { keep = [ys[0]]; }, E.B => { keep = [0]; } }
    var j1: i32[] = [111, 222];
    var j2: i32[] = [333, 444];
    var j3: i32[] = [555, 666];
    return keep[0] + keep[keep.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
` + mvsChurnMain,
			want: 96, allocs: 130, frees: 130,
		},
		{
			// GUARD 1 — the RETURN escape, which really does hand the reference
			// over: the self-host IR path has no return-transfer inc, so the value
			// leaves owning the payload. The skip is retained and this row is at
			// native parity. Drop the skip here and it becomes exit 99, an rc
			// underflow: the drop decs a payload the returned value owns.
			name: "return_escape_keeps_the_skip",
			src: decls + `function take(i: i32): i32[] {
    var v: E = mkv(i);
    match (v) { E.A(xs) => { return xs; }, E.B => { return [0]; } }
    return [0];
}
function round(i: i32): i32 {
    var r: i32[] = take(i);
    return (r.len() + r[0]) % 101;
}
` + mvsMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			// GUARD 2 — a CONDITIONAL return. Half the rounds fall through, so the
			// post-match drop runs on a path where nothing took the payload, and
			// the retained skip leaks there: 250/200 against native's 250/250. A
			// sound leak and a real gap, pinned as one. Closing it needs a
			// per-PATH verdict, not a per-binding one. Drop the skip here instead
			// and the return path over-releases (exit 99), which is why this stays.
			name: "conditional_return_keeps_the_skip_and_leaks",
			src: decls + `function take(i: i32): i32[] {
    var v: E = mkv(i);
    match (v) { E.A(xs) => { if (i % 2 == 0) { return xs; } }, E.B => { } }
    return [7];
}
function round(i: i32): i32 {
    var r: i32[] = take(i);
    return (r.len() + r[0]) % 101;
}
` + mvsMain,
			want: 40, allocs: 250, frees: 200,
		},
		{
			// GUARD 3 — a STRING payload stored out. `keep = s` emits NO retain, so
			// the box remains the payload's sole owner and a dec at the match frees
			// what `keep` still reads. The skip is retained whatever the escape: 140/100, a sound leak against native's 20/20 (native folds
			// the constant concat, hence the different alloc count — what matters
			// is that both reclaim everything they allocate and return 24).
			// Drop the skip here and the value comes back 33: a use-after-free that
			// the leak counts and __rc_underflow_count() both report as clean.
			name: "string_payload_keeps_the_skip",
			src: `enum T { W(string), N }
function round(i: i32): i32 {
    var v: T = T.W("ab" + "cd");
    var keep: string = "zz";
    match (v) { T.W(s) => { keep = s; }, T.N => { keep = "q"; } }
    var j1: string = "pp" + "qq";
    var j2: string = "rr" + "ss";
    return keep.len() * 10 + (keep[0] as i32) + j1.len() - j1.len() + j2.len() - j2.len();
}
` + mvsChurnMain,
			want: 24, allocs: 80, frees: 60,
		},
		{
			// GUARD 4 — a nested STRUCT payload stored out. Its store emits no retain
			// either, same uncounted alias as GUARD 3. Retained: 140/60, a sound leak against native's 140/140.
			// Drop the skip here and the program SEGFAULTS (exit 139).
			name: "struct_payload_keeps_the_skip",
			src: `struct P { xs: i32[] }
enum S { V(P), N }
function round(i: i32): i32 {
    var v: S = S.V(P { xs: [7, 8] });
    var keep: P = P { xs: [0] };
    match (v) { S.V(p) => { keep = p; }, S.N => { keep = P { xs: [0] }; } }
    var j1: i32[] = [111, 222];
    var j2: i32[] = [333, 444];
    return keep.xs[0] + keep.xs[keep.xs.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0];
}
` + mvsChurnMain,
			want: 9, allocs: 140, frees: 60,
		},
		{
			// A 16-element payload, which is what established that the block the
			// old behaviour stranded was the PAYLOAD and not keep's initial array:
			// the leak scaled with the element count (152 bytes/round) rather than
			// staying at a one-element array's size.
			name: "large_payload_stored_out",
			src: `enum E { A(i32[]), B }
function mkv(): E { return E.A([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]); }
function round(i: i32): i32 {
    var v: E = mkv();
    var keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + mvsChurnMain,
			want: 49, allocs: 60, frees: 60,
		},
	}
}

// TestSelfHostMovedPayloadSkipX86_64 is the leak-accounting leg.
func TestSelfHostMovedPayloadSkipX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range movedSkipCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "mvskip_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the drop released "+
					"a payload its escapee still owns; 139 = segfault)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER on a reclaiming row means the "+
					"narrowing stopped applying; MORE on a *keeps_the_skip* row means the "+
					"skip was widened away and that shape now over-releases", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostMovedPayloadSkipWasmIR — exit codes only, so what this leg catches
// is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostMovedPayloadSkipWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping moved-payload skip wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range movedSkipCases() {
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
			watFile := filepath.Join(dir, "mvskip_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("moved-payload skip wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostMovedPayloadSkipIRArm64 — the arm64 sibling under qemu.
func TestSelfHostMovedPayloadSkipIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range movedSkipCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mvskip_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
