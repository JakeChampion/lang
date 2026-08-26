package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The FIELD-READ spelling of the STRING counted share ---------------------
//
// `var p: P = P { f: q.f, … }` off a live sibling holder, string flavour — the
// construction matrix's str__fieldread cell (400 allocs / 200 frees against
// native's 300/300). The read lowers through struct_get to the SOURCE box's
// buffer, so the new box co-owns it.
//
// Both holders were marked box-only ("NODEEP:"), each correctly while the share
// is uncounted: q because a field read in a struct-literal field is a positive
// MOVE position, p because its literal borrows a string it did not retain.
// slot_nodeep then withholds __struct_drop_P from BOTH, so neither holder frees
// the string at all — two leaked boxes per round. Dumping the credit rows for the
// pair is what settled it:
//
//	inline spelling:  NODEEP:p      NODEEP:q
//	hoisted spelling: FLDCHECKED:p  FLDCHECKED:q
//
// Hoisting the read to a local (`var t = q.f; P { f: t }`) was already clean —
// strfld_safe_operand forgives a direct field-read init and the #4768 read-side
// retain counts it. The two spellings are the same program and return the same
// answer; only the inline one leaked.
//
// ONE predicate decides all three sites (str_field_share_read): the retain in the
// ExprStructLit lowering and the two marker flips in bind_var_slot. They agree by
// construction rather than by three conditions lining up, which is the safety
// argument — a marker flipped without the inc behind it turns one box under two
// rc-aware k_str decs into a free followed by a dangle.
//
// Flipping the verdict means WRITING "FLDCHECKED:", not merely revoking
// "NODEEP:" — they are two arms of one either/or and a block-scoped slot deep-
// drops only on the second (#6127). `blockscoped` is the row that would catch a
// revoke-only change.
//
// TWO SHAPES STAY REFUSED, and both are load-bearing rather than decorative:
//
//   - `respread`: a `T { ...base }` copies every field pointer into a fresh box
//     with NO inc, minting an uncounted third owner. Gated by FIELD TYPE
//     (LowerState.spread_sites), not by holder name, because the dangerous base
//     can name a local with no slot yet when the share is decided.
//   - `moved_ret`: no bind, so no marker flip, and the inc goes with the move
//     (#6726).
//
// Both therefore keep the leak they had — deliberately — so they assert their
// exit code only, not balance. If either ever starts balancing here, the gate
// that refuses it has stopped firing.
//
// Every `want` was measured against the NATIVE x86-64 backend, never read off the
// self-host run under test. `source_uaf` is the wrong-ANSWER probe: p dies inside
// the branch and q must read its string back intact after allocation churn has
// had the chance to reuse anything freed too early. The census cannot separate a
// correct fix from an over-release — both read allocs == frees — so that row, not
// the counts, is what makes the share sound.

const strFieldReadDecl = `struct P { f: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }
`

const strFieldReadMain = `
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

type strFieldReadCase struct {
	name string
	src  string
	want int
	// balance: the run must end at live_bytes 0 with allocs == frees. False for
	// the two shapes the share is deliberately refused for — they keep their
	// pre-existing leak, and asserting balance would pin the wrong behaviour.
	balance bool
}

func strFieldReadCases() []strFieldReadCase {
	return []strFieldReadCase{
		{
			// The matrix cell: two live holders over one string box.
			name: "basic",
			src: strFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    var p: P = P { f: q.f, n: i };
    t = p.f.len() + p.n;
    return (t + q.n + q.f.len()) % 101;
}` + strFieldReadMain,
			want: 10, balance: true,
		},
		{
			// Identical to `basic` but for the braces. A block-scoped slot deep-
			// drops only on the "FLDCHECKED:" witness, so a change that revokes
			// "NODEEP:" without writing the other arm leaves p with neither marker
			// and this row leaks while `basic` stays clean.
			name: "blockscoped",
			src: strFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    if (i >= 0) { var p: P = P { f: q.f, n: i }; t = p.f.len() + p.n; }
    return (t + q.n + q.f.len()) % 101;
}` + strFieldReadMain,
			want: 10, balance: true,
		},
		{
			// The share runs half the time, so the rounds that skip it exercise the
			// source's own walk with no co-owner at all.
			name: "conditional",
			src: strFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: q.f, n: i }; t = p.f.len() + p.n; }
    return (t + q.n + q.f.len() + 7) % 101;
}` + strFieldReadMain,
			want: 76, balance: true,
		},
		{
			// The new holder goes to a callee that may keep it; the source finding
			// rc > 1 simply declines its walk.
			name: "holder_escapes",
			src: strFieldReadDecl + `function keepit(p: P): i32 { return p.f.len() + p.n; }
function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i + 1 };
    var t: i32 = keepit(p);
    return (t + q.n + q.f.len()) % 101;
}` + strFieldReadMain,
			want: 9, balance: true,
		},
		{
			// Three holders over one box, each link counted: rc 3, and the walks
			// hand off down to whichever finds rc 1.
			name: "chain",
			src: strFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i + 1 };
    var z: P = P { f: p.f, n: i + 2 };
    return (q.f.len() + p.f.len() + z.f.len() + z.n) % 101;
}` + strFieldReadMain,
			want: 59, balance: true,
		},
		{
			// REFUSED: the spread mints an uncounted third owner, so the share is
			// declined and the pre-existing leak stands.
			name: "respread",
			src: strFieldReadDecl + `function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i + 1 };
    var z: P = P { ...p, n: i + 2 };
    return (p.f.len() + z.n + q.n + z.f.len()) % 101;
}` + strFieldReadMain,
			want: 8,
		},
		{
			// REFUSED: the move-elided shape (#6726) — no bind, so no marker flip.
			name: "moved_ret",
			src: strFieldReadDecl + `function hold(i: i32): P {
    var q: P = P { f: mkv(i), n: i };
    return P { f: q.f, n: i };
}
function round(i: i32): i32 { var p: P = hold(i); return (p.f.len() + p.n) % 101; }` + strFieldReadMain,
			want: 40,
		},
		{
			// The wrong-ANSWER probe. `p` dies at the end of the branch; `q` must
			// still read its string back intact after churn has had the chance to
			// reuse anything freed early. 31 = len("k") + the 30-char suffix.
			name: "source_uaf",
			src: strFieldReadDecl + `function churn(i: i32): i32 {
    var a: string = w("chunkA");
    var b: string = w("chunkB");
    return a.len() + b.len() + i;
}
function round(i: i32): i32 {
    var q: P = P { f: mkv(i), n: i };
    var t: i32 = 0;
    if (i % 2 == 0) {
        var p: P = P { f: q.f, n: i };
        t = p.f.len() + p.n;
    }
    var junk: i32 = churn(i * 7 + 3);
    if (q.f.len() != 31) { return 0 - 1; }
    return (t + q.n + junk) % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 68, balance: true,
		},
	}
}

// TestSelfHostStrFieldReadShareX86_64 — a struct-literal field READ of a string is
// a counted share: the new box retains it and both holders trade their box-only
// marker for the deep walk, with the two shapes whose share count stays
// incomplete refusing it.
func TestSelfHostStrFieldReadShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strFieldReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strfr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a marker was flipped "+
					"without the retain behind it; 100 = the source read back wrong; "+
					"139 = it read freed memory)", tc.name, exit, tc.want)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
			if !tc.balance && allocs == frees && live == 0 {
				t.Errorf("%s: %s — this shape is deliberately REFUSED the share; "+
					"balancing means the gate that declines it stopped firing", tc.name, summary)
			}
		})
	}
}

// TestSelfHostStrFieldReadShareWasmIR — the wasm sibling. Exit code only; the
// wasm leg carries no leak census.
func TestSelfHostStrFieldReadShareWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string field-read share wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strFieldReadCases() {
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
			watFile := filepath.Join(dir, "strfr_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("string field-read share wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrFieldReadShareIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrFieldReadShareIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strFieldReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "strfr_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
