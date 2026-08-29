package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- A `return` before a consuming match strands the local (#7742) -----------
//
// #7725 armed the return-path drop for a `return` inside the consuming match's
// own arm. `optret_pending` was installed around that one statement and restored
// after it, so a `return` from ANY earlier statement carried an empty pending
// set — and the post-match drop it jumped is the local's only release, because
// the consuming-match analysis owns the name and no exit-sweep class covers it.
//
// No alias, no nesting, no arm:
//
//	var src: Option[i32[]] = Some([i, i + 1]);
//	if (i >= 0) { return 5; }
//	match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
//
//	rounds   native        self-host (before)
//	100      200/200/0     200/0  live 8000
//	400      800/800/0     800/0  live 32000
//
// 80 B/round, unbounded, frees=0 — nothing released at all. The rc-enum sibling
// behaves identically; a flat `Option[string]` does NOT leak, because it carries
// a slot credit whose exit sweep still runs on the return path, which is the same
// asymmetry #7725 recorded.
//
// The fix arms the entry across the candidate's LIVE RANGE — after its `var`, up
// to and including its match — rather than at the match alone.
//
// TWO ROUND COUNTS ON THE ARRAY ROW, deliberately: the discriminator between this
// and a bounded leak is whether live_bytes moves with the round count, and a
// single count cannot show it. The `..._400` row is not redundant with the 100 one.
//
// Every row asserts `__rc_underflow() == 0` before its answer. Widening a release
// window is the shape that double-frees, and the census cannot see an
// over-release into a freelist — so each row also runs a second leg under
// FERN_SANITIZE=1. `TestSelfHostNestedMatchBorrowNoUnderflow` covers the same
// hazard from the other side and runs in the same suites.
//
// Every want was confirmed against BOTH oracles — `bin/fern -interp` and the
// native x86-64 backend agreed on each.
type earlyReturnDropCase struct {
	name string
	src  string
	want int
}

func earlyReturnDropMain(rounds string) string {
	return "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
		"while (i < " + rounds + ") { t = t + round(i); i = i + 1; } " +
		"if (__rc_underflow() != 0) { return 99; } return t % 83; }"
}

func earlyReturnDropCases() []earlyReturnDropCase {
	const arrEarly = `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) { return 5; }
    match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
    return 0;
}`
	return []earlyReturnDropCase{
		{
			// THE REPRO. Was 200/0 live 8000.
			name: "arr_early_return",
			src:  arrEarly + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			// The same shape at 4x the rounds. Was 800/0 live 32000 — the row that
			// makes the leak's unboundedness a fact rather than an inference.
			name: "arr_early_return_400_rounds",
			src:  arrEarly + earlyReturnDropMain("400"),
			want: 8,
		},
		{
			// The control: the `return` moved AFTER the match, so the post-match
			// drop is reached. Balanced before this change and after it.
			name: "arr_return_after_match",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var src: Option[i32[]] = Some([i, i + 1]);
    match (src) { Some(b) => { t = b.len(); }, None => { t = 2; } }
    if (i >= 0) { return t + 5; }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 36,
		},
		{
			// The rc-payload ENUM sibling — a different family
			// (consumed_rcpayload_enum_frees) reached through the same window, and
			// the one whose pending entry carries a moved set. Was 200/0.
			name: "rcenum_early_return",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    var src: E = E.Full([i, i + 1]);
    if (i >= 0) { return 5; }
    match (src) { E.Full(b) => { return b.len(); }, E.None => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			// The SCALAR enum family (consumed_scalar_enum_frees), the third of the
			// three that install pending entries.
			name: "scalar_enum_early_return",
			src: `enum S { A(i32), B }
function round(i: i32): i32 {
    var src: S = S.A(i);
    if (i >= 0) { return 5; }
    match (src) { S.A(b) => { return b; }, S.B => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			// A flat Option[string]: clean BEFORE this change, because its slot
			// credit's exit sweep still runs on the return path. Here to pin that
			// the widened window does not release it a second time — the row that
			// would go to 99 if it did.
			name: "str_early_return_unchanged",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: Option[string] = Some(w("ab"));
    if (i >= 0) { return 5; }
    match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			// The early return is TAKEN on half the rounds only, so both paths run
			// in one program: the return path releases through the pending entry and
			// the fall-through path through the post-match drop. Exactly one of them
			// fires per round, which is the property the whole window rests on.
			name: "early_return_conditional",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { return 5; }
    var t: i32 = 0;
    match (src) { Some(b) => { t = b.len(); }, None => { t = 2; } }
    return t;
}` + earlyReturnDropMain("100"),
			want: 18,
		},
		{
			// The `return` is inside a WHILE between the var and the match, so the
			// sweep is emitted from a nested block that inherits the enclosing
			// pending set rather than owning it.
			name: "early_return_inside_a_loop",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var k: i32 = 0;
    while (k < 3) { if (k == 1) { return 5; } k = k + 1; }
    var t: i32 = 0;
    match (src) { Some(b) => { t = b.len(); }, None => { t = 2; } }
    return t;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			// The lower_block sibling: candidate, early return and match all inside
			// an `if`, which is the second of the two sites that install entries.
			name: "nested_block_early_return",
			src: `function round(i: i32): i32 {
    if (i >= 0) {
        var src: Option[i32[]] = Some([i, i + 1]);
        if (i >= 0) { return 5; }
        match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
    }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
		{
			// A STRUCT payload, whose release is a field walk plus a box dec — the
			// deepest release reached through this window, and the one that strands
			// a field buffer rather than just a box when it is missed.
			name: "struct_payload_early_return",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    var src: Option[P] = Some(P { xs: [i, i + 1], n: i });
    if (i >= 0) { return 5; }
    match (src) { Some(p) => { return p.n % 7; }, None => { return 2; } }
    return 0;
}` + earlyReturnDropMain("100"),
			want: 2,
		},
	}
}

// TestSelfHostEarlyReturnConsumingDropX86_64 — every row balances at live_bytes 0
// with no rc underflow, on the census leg and again under the quarantining
// allocator.
func TestSelfHostEarlyReturnConsumingDropX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range earlyReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "earlyret_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the pending entry "+
					"and the post-match drop both fired on one path)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A short "+
					"free count is a `return` jumping the post-match drop with nothing "+
					"armed in its place", tc.name, summary)
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "earlyret_san_"+tc.name, sanAsm)
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

// TestSelfHostEarlyReturnConsumingDropWasmIR — the wasm sibling. Exit codes only:
// an over-release moves no byte count on any backend.
func TestSelfHostEarlyReturnConsumingDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping early-return consuming-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range earlyReturnDropCases() {
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
			watFile := filepath.Join(dir, "earlyret_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("early-return consuming drop wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostEarlyReturnConsumingDropIRArm64 — the arm64 sibling under qemu.
func TestSelfHostEarlyReturnConsumingDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range earlyReturnDropCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "earlyret_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("early-return consuming drop arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
