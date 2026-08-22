package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- The tuple class credits, keyed on the binding rather than the name (#7272)
//
// The class credits are collected from the AST, before any slot exists, so their
// key had always been the variable NAME — and a name has no scope. Two `var t` in
// sibling blocks are two slots under one key, and when they are not the same class
// the table hands each slot BOTH credits: the box takes the "TUP:" shallow dec AND
// the "TUPRCS:" deep free, and is released twice.
//
//	self-host 99 (rc underflow)   native 10   interp 10
//
// with `allocs=400 frees=400 live_bytes=0` throughout — a doubly-released block
// goes straight back to the freelist, so the byte count is clean and the answer is
// only wrong because the probe checks the counter. Nothing else dissents.
//
// What isolated it was a one-word diff: renaming the second block's local to `u`,
// changing nothing else, made the same program correct (and left the alloc counts
// identical, so it is the key resolution rather than the classes granted). Two
// same-class blocks were already clean, in both directions.
//
// The fix is #7253's step 1 for this fact family: a `StmtVar` carries line and col,
// so `name@line:col` names the BINDING, bind_var_slot records it on the slot, and
// the four tuple predicates resolve the credit their own binding earned. The other
// ~70 namespaces multiplexed into reclaimable_names are still name-keyed — #7253 is
// the issue for the rest — so `reclaim_slot_name` is untouched and this is a
// parallel accessor rather than a change to it.
//
// The shapes below are deliberately ordinary: the blocks do not have to be sibling
// `{ }` scopes, and two `if` arms or two loop bodies collide the same way.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type tupClassKeyCase struct {
	name string
	src  string
	want int
}

func tupClassKeyCases() []tupClassKeyCase {
	return []tupClassKeyCase{
		{
			// The issue's repro: sibling blocks, ident element then array literal.
			name: "sibling_blocks",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    { var t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { var t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = acc + t.1[1]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 10,
		},
		{
			// The reverse ordering. Which block comes first decides which credit
			// tagged_value_of returns FIRST, so both orders have to be pinned —
			// a fix that only reorders the lookup would pass one and fail the other.
			name: "sibling_blocks_reversed",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    { var t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = t.1[1]; }
    { var t: (i32, i32[]) = (i, xs); acc = acc + t.1[0]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 10,
		},
		{
			// Two IF ARMS — the same collision in code nobody would call unusual,
			// and the reason this is worth fixing at the key rather than per shape.
			name: "if_arms",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    if (i % 2 == 0) { var t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    else { var t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = t.1[1]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 5,
		},
		{
			// Two LOOP BODIES, so each binding is also rebound per iteration — the
			// rebind path reads "TUPREBIND:" / "TUPRC:" off the same slot, so this
			// covers the credits the exit sweep does not.
			name: "loop_bodies",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 2) { var t: (i32, i32[]) = (i, xs); acc = acc + t.1[0]; k = k + 1; }
    var m: i32 = 0;
    while (m < 2) { var t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = acc + t.1[1]; m = m + 1; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 20,
		},
		{
			// The control: two same-named locals of the SAME class. Already correct
			// before the change, and it has to stay correct — a site key that failed
			// to resolve at all would leak here rather than over-release, which the
			// leakcheck assertions below would catch.
			name: "same_class_both_blocks",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    { var t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { var t: (i32, i32[]) = (i, xs); acc = acc + t.1[1]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
		{
			// The other control: DIFFERENT names, different classes. This is the
			// program the repro becomes under a one-word rename, and the diff that
			// isolated the cause — it was already correct, and pins that the fix did
			// not achieve its result by disabling the classes.
			name: "distinct_names",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    { var t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { var u: (i32, i32[]) = (i, [i + 7, i + 9]); acc = acc + u.1[1]; }
    return acc;
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow() != 0) { return 99; } return x % 83; }`,
			want: 10,
		},
	}
}

// leakSummaryLine returns the LAST "leakcheck: " line of a probe's stderr, or ""
// when the run emitted none. Last rather than first: the summary is printed at
// exit, so a program that also writes diagnostics keeps the authoritative line.
func leakSummaryLine(stderr string) string {
	out := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			out = line
		}
	}
	return out
}

// TestSelfHostTupleClassSlotKeyX86_64 — each binding resolves its own class, so no
// box is released twice and none is left unreleased.
//
// The exit code is the load-bearing assertion here and the byte count is the
// guard rail. An over-release does not move `live_bytes` — the block returns to
// the freelist — so only `__rc_underflow()` separates a correct compiler from the
// broken one. `allocs == frees` is what catches the opposite failure: a site key
// that resolves to nothing would deny the credit and leak instead.
func TestSelfHostTupleClassSlotKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupClassKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupclasskey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: two same-named "+
					"tuple locals resolved one another's class credit)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — allocs and frees must balance at live_bytes 0. A site "+
					"key that resolves to no credit leaks here instead of over-releasing, "+
					"which the exit code above would not show", tc.name, summary)
			}
		})
	}
}

// TestSelfHostTupleClassSlotKeyWasmIR — the wasm sibling. Exit codes only, which
// is the whole signal for this bug: an over-release does not change the byte count
// on any backend.
func TestSelfHostTupleClassSlotKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple class slot-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupClassKeyCases() {
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
			watFile := filepath.Join(dir, "tupclasskey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple class slot-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleClassSlotKeyIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleClassSlotKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupClassKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupclasskey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
