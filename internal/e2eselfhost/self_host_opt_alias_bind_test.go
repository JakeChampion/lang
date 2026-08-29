package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- An UNMATCHED Option local's alias bind gets a retain and a credit -------
//
// `var x: Option[T] = src` denied `src` its whole reclaim credit whenever `src`
// had no consuming match of its own — the `!name_is_alias_bound` conjunct in
// `opt_unmatched_esc_ok` (#7687). Nothing else released either slot, so the bind
// alone leaked the box and its payload:
//
//	shape (100 rounds)                     native      self-host (before)
//	Option[i32[]] alias, never read        200/200/0   200/0  live 8000
//	Option[i32[]] alias, alias matched     200/200/0   200/0  live 8000
//	Option[string] alias, never read       100/100/0   300/0  live 7200
//
// The denial was correct for the code as it stood: these releases are a payload
// release plus a box dec, the bind took no retain, and forgiving it alone leaves
// two slots decing one count. The fix is the pairing every other container class
// already has — the bind RETAINS the box, and the alias takes the class credit
// qualified by "NODEEP:" so its release is the box dec alone while the source
// keeps the one deep release.
//
// EVERY ROW IS GATED ON `__rc_underflow()`, NOT BYTES, and that is not
// belt-and-braces here. Both intermediate states of this change balanced the
// census perfectly while corrupting memory:
//
//	build                                   exit   census
//	credit without the retain               99     200/200 live 0
//	credit + retain, deep release on both   99     200/200 live 0
//	correct                                 36     200/200 live 0
//
// A leak-accounting assertion passes on all three. The exit code separates them,
// and every row runs a second leg under FERN_SANITIZE=1 — the quarantining
// allocator reports the over-release directly rather than by arithmetic. #7687
// was explicit that this family needs an instrument the census does not provide.
//
// Every want was confirmed against BOTH oracles — `bin/fern -interp` and the
// native x86-64 backend agreed on each — never read off the self-host run.
type optAliasBindCase struct {
	name      string
	src       string
	want      int
	balance   bool
	wantFrees int64 // asserted exactly on every row that does not set balance
}

const optAliasBindMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

func optAliasBindCases() []optAliasBindCase {
	return []optAliasBindCase{
		{
			// THE REPRO, in its barest form: the alias is never read at all, so
			// nothing but the bind is different from the control below. Was 200/0.
			name: "arr_alias_never_read",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    return 7;
}` + optAliasBindMain,
			want: 36, balance: true,
		},
		{
			// Its control: the same program without the bind. Clean throughout.
			name: "arr_no_alias",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    return 7;
}` + optAliasBindMain,
			want: 36, balance: true,
		},
		{
			// The alias is itself matched — the commonest use of one, and the row
			// that pins the vetting walker. `body_unsafe_for` reads a bare-ident
			// match scrutinee as an escape, which would refuse this shape outright;
			// `body_unsafe_for_match_borrow` plus the payload-out conjunct is the
			// pairing that admits the box while staying strict on the payload.
			name: "arr_alias_matched",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    match (x) { Some(a) => { t = a.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 34, balance: true,
		},
		{
			// Both matched. Clean before this change (the source's own
			// consuming-match family owns it) and clean after — the row that would
			// go to 99 if the alias took a second deep release.
			name: "arr_both_matched",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    match (x) { Some(a) => { t = a.len(); }, None => {} }
    match (src) { Some(b) => { t = t + b.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 68, balance: true,
		},
		{
			// Only the SOURCE is matched: the alias rides the consuming-match
			// family's own gate, not the unmatched one. Clean before and after.
			name: "arr_source_matched",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    match (src) { Some(b) => { t = b.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 34, balance: true,
		},
		{
			// The STRING payload class ("OPTSTR:"), whose release is
			// __fern_str_free on the payload rather than a flat dec — a distinct
			// release, so it needs its own row. Was 300/0 live 7200.
			name: "str_alias_never_read",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: Option[string] = Some(w("ab"));
    var x: Option[string] = src;
    return 7;
}` + optAliasBindMain,
			want: 36, balance: true,
		},
		{
			name: "str_alias_matched",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: i32 = 0;
    var src: Option[string] = Some(w("ab"));
    var x: Option[string] = src;
    match (x) { Some(s) => { t = s.len(); }, None => {} }
    return t;
}` + optAliasBindMain,
			want: 51, balance: true,
		},
		{
			// The NESTED-Option class ("OPTOPTRC:"), whose release is the guarded
			// two-level walk. The one whose second deep release would free the
			// inner string as well as the inner box.
			name: "optopt_alias_never_read",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var src: Option[Option[string]] = Some(Some(w("ab")));
    var x: Option[Option[string]] = src;
    return 7;
}` + optAliasBindMain,
			want: 36, balance: true,
		},
		{
			// A CONDITIONAL alias: the bind runs on half the rounds, so the alias
			// slot is null on the others. The box-only release is null-guarded for
			// exactly this, and a transfer model would leak the rounds where no
			// bind happened.
			name: "arr_conditional_alias",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) {
        var x: Option[i32[]] = src;
        return 5;
    }
    return 7;
}` + optAliasBindMain,
			want: 19, balance: true,
		},
		{
			// REFUSED, and it must stay refused: the alias carries the PAYLOAD out
			// of its arm, so the element buffer outlives the source's deep release.
			// Admitting it is a use-after-free, not a leak — the shape
			// docs/rc-log/2026-08-29-option-alias-payload-out.md measured at exit
			// 99 with a perfectly balanced census. Refusing costs a leak, which is
			// the safe direction and what the count below pins.
			name: "refuses_alias_carrying_payload_out",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    var out: i32[] = [0];
    match (x) { Some(xs) => { out = xs; }, None => {} }
    return out.len();
}` + optAliasBindMain,
			want: 34, wantFrees: 200,
		},
		{
			// REFUSED: the alias is REASSIGNED, so its final value is not the box
			// the credit describes.
			name: "refuses_reassigned_alias",
			src: `function round(i: i32): i32 {
    var src: Option[i32[]] = Some([i, i + 1]);
    var x: Option[i32[]] = src;
    x = None;
    return 7;
}` + optAliasBindMain,
			want: 36, wantFrees: 0,
		},
		{
			// A LOOP-resident pair: both slots are re-declared each iteration, so
			// the bind's dec-of-superseded-box and the sweep dec have to add up on
			// every round rather than only at function exit.
			name: "arr_alias_in_a_loop",
			src: `function round(i: i32): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var src: Option[i32[]] = Some([i, i + j]);
        var x: Option[i32[]] = src;
        match (x) { Some(a) => { t = (t + a.len()) % 101; }, None => {} }
        j = j + 1;
    }
    return t;
}` + optAliasBindMain,
			want: 19, balance: true,
		},
	}
}

// TestSelfHostOptAliasBindX86_64 — every row balances at live_bytes 0 with no rc
// underflow. Balance alone does not separate a correct build from either broken
// one here; the exit code is what does.
func TestSelfHostOptAliasBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optAliasBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optaliasbind_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the shared box "+
					"decremented once more than it was retained, or the payload "+
					"released by both owners)", tc.name, exit, tc.want)
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
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0 (native does). A "+
						"short free count is the alias bind denying the source its "+
						"credit again", tc.name, summary)
				}
			} else if frees != tc.wantFrees {
				t.Errorf("%s: %s — refused row's frees moved (want exactly %d). A "+
					"HIGHER count is the refusal breaking down: a payload released "+
					"under a live reference", tc.name, summary, tc.wantFrees)
			}

			// The census cannot separate a correct build from a double-freeing
			// one on this family, so every row runs again under the quarantining
			// allocator, which reports the over-release directly.
			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "optaliasbind_san_"+tc.name, sanAsm)
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

// TestSelfHostOptAliasBindWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend, and the underflow guard is
// what carries the signal here.
func TestSelfHostOptAliasBindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Option alias-bind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optAliasBindCases() {
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
			watFile := filepath.Join(dir, "optaliasbind_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("Option alias-bind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOptAliasBindIRArm64 — the arm64 sibling under qemu.
func TestSelfHostOptAliasBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optAliasBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "optaliasbind_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("Option alias-bind arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
