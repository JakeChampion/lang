package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- The STRING-position assign-form tuple rebind (#7226) --------------------
//
// `t = (k, u)` where the tuple's annotation puts a string at position 1. The
// array limb of this rebind has released since #7929; the string limb was held
// back because the element-kinds string is recorded ONCE, from the var site's
// literal, and then replayed by every site that frees the box — the rebind
// store, the scope-exit sweep, the precise drop. Those sites therefore replay it
// against a box some OTHER writer filled, and a writer that left a view there
// would have its box freed out from under the view's own sweep.
//
// The admission is WRITER AGREEMENT: an 's' kind is recorded only when every
// assign-form rebind stores a counted owned string at that position — a literal,
// a fresh concat / producer call, or a bare ident whose sole binding is one of
// those and which is never reassigned. Views and borrowed aliases are refused by
// positive proof rather than detected, because the pass that decides the credit
// runs before lowering and has no slot facts to ask.
//
// Two counts per shape are the discriminator this needs. The leak these close is
// per ROUND, so `live_bytes` doubles with the loop bound; a bounded strand does
// not move. Every want below was confirmed against BOTH oracles — bin/fern
// -interp and the native x86-64 backend agreed on each — never read off the
// self-host run under test.

type tupStrRebindCase struct {
	name string
	src  string
	want int
}

func tupStrRebindCases() []tupStrRebindCase {
	// The headline shape at two round counts. On the parent both fail with
	// live_bytes 16000 and 32000 — exactly 2.0x per doubling, which is what
	// separates this from a bounded strand.
	repro := `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var u: string = w("cd");
    t = (i + 1, u);
    return t.1.len() + s.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < ROUNDS) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`

	return []tupStrRebindCase{
		{name: "str_pos_rebind_100", src: strings.Replace(repro, "ROUNDS", "100", 1), want: 31},
		{name: "str_pos_rebind_200", src: strings.Replace(repro, "ROUNDS", "200", 1), want: 62},
		{
			// Both writers bound before the tuple, so the var site and the rebind
			// name two locals of the same producer. This was the recorded
			// "string_pos_rebind_refused" hazard; the answer is unchanged and the
			// leak is gone, so it moves here.
			name: "str_pos_two_fresh_writers",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s1: string = w("ab");
    var s2: string = w("cd");
    var t: (i32, string) = (i, s1);
    t = (i + 1, s2);
    return t.0 + t.1.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 17,
		},
		{
			// ONE local at both writers. Both tuples retain it, so two releases
			// are owed on one box and the refcount arbitrates; a walk that freed
			// the box outright at the first would dangle the second.
			name: "one_source_both_writers",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    t = (i + 1, s);
    return t.1.len() + s.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 62,
		},
		{
			// Kinds "as": an array position and a string position in one tuple,
			// both rebound. The two limbs release through one walk, so a kinds
			// string that admitted only one character class would drop the other.
			name: "str_and_arr_pos_rebind",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var xs: i32[] = [i, i + 1];
    var t: (i32[], string) = (xs, s);
    var u: string = w("cd");
    var ys: i32[] = [i + 2, i + 3];
    t = (ys, u);
    return t.1.len() + t.0[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 39,
		},
		{
			// Both source locals read AFTER the rebind. The walk gives back only
			// the tuple's reference, so each local's own box must still be live
			// here — the case a source-releasing walk corrupts.
			name: "both_sources_read_after_rebind",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var u: string = w("cd");
    t = (i + 1, u);
    return t.1.len() + s.len() + u.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 4,
		},
		{
			// The rebind on one branch only: on the untaken branch the store's cow
			// guard and the element walk's null guard must leave the var site's
			// tuple alone, and the scope-exit sweep still owes exactly one release.
			name: "str_pos_cond_rebind",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var u: string = w("cd");
    if (i % 2 == 0) { t = (i + 1, u); }
    return t.1.len() + s.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 62,
		},
		{
			// The ARRAY-position control, unchanged by the widening: it released
			// before this change and must release identically after.
			name: "arr_pos_rebind_control",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 2, i + 3];
    var t: (i32, i32[]) = (i, xs);
    t = (i + 1, ys);
    return t.1[0];
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 8,
		},
	}
}

// tupStrRebindHazards — the writers the agreement must NOT admit. Each keeps a
// bounded-per-round leak (the safe direction) and is pinned by answer plus
// __rc_underflow_count(): a doubly-released block returns to the freelist, so
// the byte count and the sum both still come out right and only the counter
// separates the two readings.
func tupStrRebindHazards() []tupStrRebindCase {
	return []tupStrRebindCase{
		{
			// A string LITERAL at the rebind's rc position. The class gate wants a
			// bare ident there, so the rebind earns no release at all; what this
			// row pins is that the var site's own kinds are not disturbed by it
			// (the literal's box is static, so agreement holds) and nothing
			// under-flows.
			name: "str_pos_literal_writer",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    t = (i + 1, "a-literal-string-payload-past-any-inline-threshold");
    return t.1.len() + s.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 63,
		},
		{
			// A borrowed ALIAS: `u` names the box `b` still holds. Refused because
			// the writer test proves ownership positively, and `b`'s own release
			// would otherwise be racing the tuple's.
			name: "str_pos_alias_writer",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, string) = (i, s);
    var b: string = w("cd");
    var u: string = b;
    t = (i + 1, u);
    return t.1.len() + s.len() + b.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 4,
		},
		{
			// A VIEW: the rebind stores a `slice_unchecked` result, which shares the
			// receiver's buffer. This is the writer the restriction was written
			// for, and it must stay refused — an owned-string release here would
			// claim a box the view's own sweep still owns. The element is read
			// through the source local rather than `t.1`, which keeps the row off
			// the separate `str`-spelled tuple-element divergence recorded in
			// docs/rc-log/2026-09-03-tuple-str-rebind-writer-agreement.md.
			name: "str_pos_slice_view_writer",
			src: `@noinline
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (s: string) tail(n: i32): str { return slice_unchecked(s, n, s.len()); }
function round(i: i32): i32 {
    var s: string = w("ab");
    var t: (i32, str) = (i, s);
    var b: string = w("cd");
    var u: str = b.tail(2);
    t = (i + 1, u);
    return t.0 + u.len() + s.len() + b.len();
}
function main(): i32 { var x: i32 = 0; var r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 35,
		},
	}
}

// TestSelfHostTupleStrRebindX86_64 — the admitted shapes balance exactly, and a
// sanitizer leg re-runs each. allocs == frees is load-bearing in both
// directions: short is the leak this closes, above is a double free.
func TestSelfHostTupleStrRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupStrRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupstr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (both oracles agree on %d; an offset of "+
					"+N is N rc underflows)", tc.name, exit, tc.want, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). The "+
					"retain is per round, so anything stranded scales with the loop "+
					"bound; compare the 100- and 200-round rows", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupstrsan_"+tc.name, sanAsm)
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

// TestSelfHostTupleStrRebindHazardsX86_64 — the refused writers, each answered
// correctly with a zero underflow count under the sanitizer too. These assert
// answers, not leak counts: a refusal falls back to leak-mode by design.
func TestSelfHostTupleStrRebindHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupStrRebindHazards() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tupstrhaz_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d (an offset of +N is N rc underflows — "+
					"a wrongly granted credit, not a wrong sum)", tc.name, exit, tc.want)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupstrhazsan_"+tc.name, sanAsm)
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

// TestSelfHostTupleStrRebindWasmIR — the wasm sibling. Exit codes only:
// FERN_LEAKCHECK is x86-64-only, and the answer (with the underflow count folded
// in) is what proves the releases claimed no live box.
func TestSelfHostTupleStrRebindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple string-position rebind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range append(tupStrRebindCases(), tupStrRebindHazards()...) {
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
			watFile := filepath.Join(dir, "tupstr_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple string-position rebind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleStrRebindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleStrRebindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range append(tupStrRebindCases(), tupStrRebindHazards()...) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupstr_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
