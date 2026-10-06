package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A payload bound out of a match arm, by store, call or return ----------
//
// Each row binds an rc enum payload to a name that escapes the arm, by a store
// to an outer local, a borrow-only call or a return, and reads it back. On the
// typed lowering every row reclaims all it allocates. The exit codes are the
// guards: 99 is an rc underflow (the drop released a payload its escapee still
// owns), a wrong value is a use-after-free, and 139 a segfault. Every want was
// confirmed against the native x86-64 backend.
//
// The churn arrays the *_read_back_after_churn rows build are constant and not
// heap-allocated, so their counts cover only the payload. A constant payload
// reaches its box through `id`, which hides the constant from the static-box plan, so the box is
// allocated rather than placed as a static one.

type movedSkipCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const mvsMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

const mvsChurnMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
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
			// `keep = xs` retains, so the payload has two counted owners.
			name: "array_payload_stored_out",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + mvsMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			// The same shape read back as a VALUE with three fresh arrays after
			// the match, so a payload freed too early would be reused before the
			// read. Counts cannot see a use-after-READ. Native returns 9.
			name: "stored_out_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function id(xs: i32[]): i32[] { return xs; }
function mkv(): E { return E.A(id([7, 8])); }
function round(i: i32): i32 {
    let v: E = mkv();
    let keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    let j3: i32[] = [555, 666];
    return keep[0] + keep[keep.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
` + mvsChurnMain,
			want: 9, allocs: 20, frees: 20,
		},
		{
			// A borrow-only callee takes no claim, so the box is the payload's
			// sole owner.
			name: "array_payload_borrowed_by_callee",
			src: decls + `function sink(a: i32[]): i32 { return a.len() + a[0]; }
function round(i: i32): i32 {
    let v: E = mkv(i);
    let n: i32 = 0;
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
function id(xs: i32[]): i32[] { return xs; }
function mkv(): E { return E.A(id([7, 8])); }
function sink(a: i32[]): i32 { return a[0] + a[a.len() - 1]; }
function round(i: i32): i32 {
    let v: E = mkv();
    let n: i32 = 0;
    match (v) { E.A(xs) => { n = sink(xs); }, E.B => { n = 0; } }
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    let j3: i32[] = [555, 666];
    return n + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
` + mvsChurnMain,
			want: 9, allocs: 20, frees: 20,
		},
		{
			// A guarded arm whose payload is stored out.
			name: "guarded_arm_store_now_reclaimed",
			src: decls + `function round(i: i32): i32 {
    let v: E = mkv(i);
    let keep: i32[] = [0];
    match (v) { E.A(xs) when i % 2 == 0 => { keep = xs; }, E.A(ys) => { keep = [ys.len()]; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + mvsMain,
			want: 81, allocs: 250, frees: 250,
		},
		{
			// The guarded row as a VALUE with churn — both arms are exercised
			// (the guard keys on the round index). Native returns 96.
			name: "guarded_arm_read_back_after_churn",
			src: `enum E { A(i32[]), B }
function id(xs: i32[]): i32[] { return xs; }
function mkv(): E { return E.A(id([7, 8])); }
function round(i: i32): i32 {
    let v: E = mkv();
    let keep: i32[] = [0];
    match (v) { E.A(xs) when i % 2 == 0 => { keep = xs; }, E.A(ys) => { keep = [ys[0]]; }, E.B => { keep = [0]; } }
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    let j3: i32[] = [555, 666];
    return keep[0] + keep[keep.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0] + j3[0] - j3[0];
}
` + mvsChurnMain,
			want: 96, allocs: 30, frees: 30,
		},
		{
			// The return escape hands the reference over. A drop that also
			// released the payload would underflow: exit 99.
			name: "return_escape_keeps_the_skip",
			src: decls + `function take(i: i32): i32[] {
    let v: E = mkv(i);
    match (v) { E.A(xs) => { return xs; }, E.B => { return [0]; } }
    return [0];
}
function round(i: i32): i32 {
    let r: i32[] = take(i);
    return (r.len() + r[0]) % 101;
}
` + mvsMain,
			want: 5, allocs: 200, frees: 200,
		},
		{
			// The return path moves the payload out; the fallthrough path
			// releases it.
			name: "conditional_return_balances_both_paths",
			src: decls + `function take(i: i32): i32[] {
    let v: E = mkv(i);
    match (v) { E.A(xs) => { if (i % 2 == 0) { return xs; } }, E.B => { } }
    return [7];
}
function round(i: i32): i32 {
    let r: i32[] = take(i);
    return (r.len() + r[0]) % 101;
}
` + mvsMain,
			want: 40, allocs: 200, frees: 200,
		},
		{
			// A string payload stored out. A release at the match would free what
			// `keep` still reads, and the value would come back 33.
			name: "string_payload_keeps_the_skip",
			src: `enum T { W(string), N }
function round(i: i32): i32 {
    let v: T = T.W("ab" + "cd");
    let keep: string = "zz";
    match (v) { T.W(s) => { keep = s; }, T.N => { keep = "q"; } }
    let j1: string = "pp" + "qq";
    let j2: string = "rr" + "ss";
    return keep.len() * 10 + (keep[0] as i32) + j1.len() - j1.len() + j2.len() - j2.len();
}
` + mvsChurnMain,
			want: 24, allocs: 80, frees: 80,
		},
		{
			// A nested struct payload stored out. Releasing it at the match
			// segfaults (exit 139).
			name: "struct_payload_keeps_the_skip",
			src: `struct P { xs: i32[] }
enum S { V(P), N }
function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let v: S = S.V(P { xs: id([7, 8]) });
    let keep: P = P { xs: id([0]) };
    match (v) { S.V(p) => { keep = p; }, S.N => { keep = P { xs: [0] }; } }
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    return keep.xs[0] + keep.xs[keep.xs.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0];
}
` + mvsChurnMain,
			want: 9, allocs: 60, frees: 60,
		},
		{
			// A 16-element payload, which is what established that the block the
			// old behaviour stranded was the PAYLOAD and not keep's initial array:
			// the leak scaled with the element count (152 bytes/round) rather than
			// staying at a one-element array's size.
			name: "large_payload_stored_out",
			src: `enum E { A(i32[]), B }
function id(xs: i32[]): i32[] { return xs; }
function mkv(): E { return E.A(id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16])); }
function round(i: i32): i32 {
    let v: E = mkv();
    let keep: i32[] = [0];
    match (v) { E.A(xs) => { keep = xs; }, E.B => { keep = [0]; } }
    return (keep.len() + keep[0]) % 101;
}
` + mvsChurnMain,
			want: 49, allocs: 20, frees: 20,
		},
	}
}

// TestSelfHostMovedPayloadSkipX86_64 is the leak-accounting leg.
func TestSelfHostMovedPayloadSkipX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
				t.Errorf("%s: %s — want frees=%d: every row reclaims what it allocates", tc.name, summary, tc.frees)
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
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
