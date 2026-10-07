package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A `return` inside a match arm takes the return-path release (#7725) -----
//
// A `return` inside a consuming match's arm leaves before the statement after
// the match, so the scrutinee's release must also run on the return path — and
// it must be the same DEEP release the fall-through takes. A nested-Option
// payload and a struct payload are the two shapes whose box owns something
// further (the inner string, the `xs` buffer); a shallow release there frees
// the boxes and strands what they own, unbounded and sanitizer-clean. Each is
// paired with a control that assigns in the arm and returns after the match.
//
// Each row asserts `__rc_underflow_count() == 0` before its answer, because an
// over-release on the return path is a double free, which no byte count shows
// — `docs/rc-log/2026-08-29-option-alias-payload-out.md` measured an
// over-releasing build reading `300/300 live 0` where the correct one carried an
// actual leak. `TestSelfHostNestedMatchBorrowNoUnderflowX86_64` covers the same
// hazard for other shapes.
//
// The last four rows pin the other payload kinds on the same return path:
// string, string[], tagged (call-bound) and leak-safe-array.
//
// Every want was confirmed against `bin/fern -interp`, never read off the
// self-host run under test.
type armReturnDropCase struct {
	name string
	src  string
	want int
}

const armReturnDropMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func armReturnDropCases() []armReturnDropCase {
	return []armReturnDropCase{
		{
			// THE REPRO: a shallow drop frees both option boxes and strands the
			// string.
			name: "optopt_return_in_arm",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			// Its control: the same program differing only in where the arm
			// returns.
			name: "optopt_return_after_match",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let o: Option[Option[string]] = Some(Some(w("ab")));
    match (o) { Some(inner) => { match (inner) { Some(v) => { t = v.len(); }, None => { t = 3; } } }, None => { t = 2; } }
    return t;
}` + armReturnDropMain,
			want: 19,
		},
		{
			// The same shape with the candidate and its match inside an `if`
			// block.
			name: "optopt_return_in_arm_nested_block",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    if (i >= 0) {
        let o: Option[Option[string]] = Some(Some(w("ab")));
        match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			// The inner tag guard on the return path. A statically-None inner has
			// no string to release; an unguarded two-level walk would hand
			// __fern_str_free whatever the payload word holds.
			name: "optopt_none_return_in_arm",
			src: `function round(i: i32): i32 {
    let o: Option[Option[string]] = Some(None);
    match (o) { Some(inner) => { match (inner) { Some(v) => { return v.len(); }, None => { return 3; } } }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			// THE SECOND SHAPE: a shallow drop frees the struct box and strands
			// its `xs` buffer, whose release is the struct's field drop.
			name: "optstruct_return_in_arm",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { return p.n % 7; }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 13,
		},
		{
			name: "optstruct_return_after_match",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let t: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { t = p.n % 7; }, None => { t = 2; } }
    return t;
}` + armReturnDropMain,
			want: 13,
		},
		{
			name: "optstruct_return_in_arm_nested_block",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    if (i >= 0) {
        let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
        match (o) { Some(p) => { return p.n % 7; }, None => { return 2; } }
    }
    return 0;
}` + armReturnDropMain,
			want: 13,
		},
		{
			// A STRING payload.
			name: "str_return_in_arm",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(s) => { return s.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 19,
		},
		{
			// A string[] payload. Its release walks every element, so a fallback
			// to the plain box dec strands all of them.
			name: "strarr_return_in_arm",
			src: `function round(i: i32): i32 {
    let o: Option[string[]] = Some(["a" + "b", "c" + "d"]);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 68,
		},
		{
			// A TAGGED (call-bound) candidate whose producer returns None on half
			// the rounds, so the None arm's `return` runs the sweep over a box
			// carrying no payload — the case the tagged release's guard is for.
			name: "tagged_return_in_arm",
			src: `function mk(i: i32): Option[i32[]] { if (i % 2 == 0) { return None; } return Some([i, i + 1]); }
function round(i: i32): i32 {
    let o: Option[i32[]] = mk(i);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 68,
		},
		{
			// A leak-safe array payload, whose release is the shallow drop.
			name: "arr_return_in_arm",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { return a.len(); }, None => { return 2; } }
    return 0;
}` + armReturnDropMain,
			want: 68,
		},
	}
}

// TestSelfHostArmReturnConsumingDropX86_64 — every row balances at live_bytes 0
// with no rc underflow, which is the pair of facts a shallow return-path release
// (leak) and a doubled one (over-release) each break in one direction only.
func TestSelfHostArmReturnConsumingDropX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range armReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "armretdrop_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the payload "+
					"released by both the return-path sweep and the post-match drop)",
					tc.name, exit, tc.want)
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
					"short free count is the return path taking a shallower release "+
					"than the post-match drop selected", tc.name, summary)
			}
		})
	}
}

// TestSelfHostArmReturnConsumingDropWasmIR — the wasm sibling. Exit codes only:
// an over-release moves no byte count on any backend, and the underflow guard
// is what carries the signal here.
func TestSelfHostArmReturnConsumingDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arm-return consuming-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range armReturnDropCases() {
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
			watFile := filepath.Join(dir, "armretdrop_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("arm-return consuming drop wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostArmReturnConsumingDropIRArm64 — the arm64 sibling under qemu.
func TestSelfHostArmReturnConsumingDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range armReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "armretdrop_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("arm-return consuming drop arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
