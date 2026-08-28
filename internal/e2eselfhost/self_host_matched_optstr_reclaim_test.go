package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A MATCHED Option[string] local leaks its payload (#7710) ----------------
//
// The OPTSTR family had exactly one empty quadrant of a 2x2. Every other Option
// payload kind is reclaimed whether or not a `match` consumes the local; the
// string payload was reclaimed only when the local was NEVER matched — and
// matching is the ordinary way to consume an Option:
//
//	payload           matched   native      self-host (before)
//	Option[i32[]]     no        400/400/0   400/400/0
//	Option[i32[]]     yes       400/400/0   400/400/0
//	Option[i32[][]]   yes       800/800/0   800/800/0
//	Option[string]    no        200/200/0   600/600/0
//	Option[string]    YES       200/200/0   600/0  live 14400
//
// Nothing at all was freed, and it was linear in the round count — 600/0 at 200
// rounds, 1200/0 at 400 — so 72 B/round unbounded against 0 on native.
//
// `collect_unmatched_optstr_names` owns the never-matched quadrant by
// construction: its escape gate reads a bare-ident match scrutinee as an escape,
// so it refuses every match-consumed Option outright. The matched quadrant was
// left to "both match analyses", which for the array kinds is the entire reclaim
// and for the string kind released nothing. `collect_fresh_optstr_names` fills
// it, disjoint from its sibling for exactly that reason.
//
// #7712 then fills the REBIND quadrant of the same collector: a reassigned name
// whose every rebind is itself fresh, which its OPTARRARR and OPTSTRUCT siblings
// already admitted via `opt_rebinds_all_fresh`. Nothing new releases it — the
// assign path already routed an OPTSTR slot to `emit_optstr_reclaim_store`, so
// only the credit was missing. Admitting reassignment must not weaken either
// proof, which is what `refuses_rebind_aliasing_param` (an aliased rebind) and
// `refuses_reassigned_escaping` (the escape gate) pin.
//
// THE REFUSALS ARE THE LOAD-BEARING HALF. A string payload is stored UNCOUNTED
// (`op_opt_make`) and a string assignment BORROWS, so the arm binding takes no
// retain — which is why the credit is safe when the arm only reads, and why
// freeing a payload the arm hands out would be a use-after-free rather than a
// double-count. The escape analysis is an allow-list: an unrecognised use denies
// the credit (the shape keeps leaking) instead of releasing a live reference.
// The five `refuses_*` rows below each pin an exact free count, so a widening
// that starts releasing one of them fails here rather than in a sanitizer run.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run under
// test. All twelve rows are additionally sanitizer-clean under FERN_SANITIZE=1.
type matchedOptstrCase struct {
	name      string
	src       string
	want      int
	balance   bool  // assert allocs == frees at live_bytes 0
	wantFrees int64 // asserted exactly on every row that does not set balance
}

const matchedOptstrMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

const matchedOptstrW = "function w(a: string): string { return a + \"!\"; }\n"

func matchedOptstrCases() []matchedOptstrCase {
	return []matchedOptstrCase{
		{
			// THE REPRO: matched, payload read. Was 600/0 live 14400.
			name: "matched_payload_read",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19, balance: true,
		},
		{
			// The binding present but unused — identical leak before, which is
			// what showed this is the LOCAL's reclaim and not the binding's.
			name: "matched_binding_unused",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4, balance: true,
		},
		{
			// No binding at all. Also leaked before, so the arm binding was never
			// the releaser.
			name: "matched_wildcard",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4, balance: true,
		},
		{
			// The never-matched quadrant, which already worked. It must stay
			// working AND must not now be credited twice — a second release here
			// would be an over-release, caught by the 99 guard.
			name: "unmatched_control",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    return i % 7;
}` + matchedOptstrMain,
			want: 13, balance: true,
		},
		{
			// The array payload, matched: the quadrant that always worked and the
			// reason this was diagnosable at all.
			name: "arr_matched_control",
			src: `function round(i: i32): i32 {
    var o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { return a[0]; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 63, balance: true,
		},
		{
			// The arr-of-arr payload, matched — the OPTARRARR sibling whose
			// collector this one is modelled on.
			name: "arrarr_matched_control",
			src: `function round(i: i32): i32 {
    var o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4, balance: true,
		},
		{
			// THE REBIND QUADRANT (#7712): reassigned, every rebind itself fresh.
			// Was 900/0 live 21600 — neither the superseded box at the rebind nor
			// the final one at exit. The release machinery already existed (the
			// assign path routes an OPTSTR slot to emit_optstr_reclaim_store); only
			// the credit was missing, because the collector refused a reassigned
			// name outright where its OPTARRARR and OPTSTRUCT siblings admit one
			// whose rebinds are all fresh.
			name: "reassigned_all_rebinds_fresh",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(w("cd")); }
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19, balance: true,
		},
		{
			// The flat-ARRAY payload reassigned: reassignment is precisely
			// collect_fresh_optarr_names' own class, so this always worked and must
			// stay working.
			name: "reassigned_array_control",
			src: `function round(i: i32): i32 {
    var o: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { o = Some([i + 2, i + 3]); }
    match (o) { Some(a) => { return a[0]; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 14, balance: true,
		},
		{
			// REFUSED: a rebind that is NOT fresh — `Some(p)` of a parameter the
			// caller owns. Admitting reassignment must not admit an aliased rebind,
			// which would release a live reference at the NEXT rebind.
			name: "refuses_rebind_aliasing_param",
			src: matchedOptstrW + `function run(p: string, i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(p); }
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}
function round(i: i32): i32 { var s: string = w("zz"); return run(s, i); }` + matchedOptstrMain,
			want: 19, wantFrees: 0,
		},
		{
			// REFUSED: reassigned AND the payload escapes. The escape gate must
			// still bite once reassignment is admitted.
			name: "refuses_reassigned_escaping",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var held: string = "";
    var o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(w("cd")); }
    match (o) { Some(v) => { held = v; }, None => {} }
    return held.len();
}` + matchedOptstrMain,
			want: 19, wantFrees: 0,
		},
		{
			// REFUSED: the arm RETURNS the payload, so the caller owns it.
			// Releasing it here is a use-after-free, not a double count.
			name: "refuses_returned_payload",
			src: matchedOptstrW + `function mk(i: i32): string {
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return v; }, None => { return "z"; } }
    return "y";
}
function round(i: i32): i32 { var s: string = mk(i); return s.len(); }` + matchedOptstrMain,
			want: 19, wantFrees: 0,
		},
		{
			// REFUSED: the payload is stored into a local that outlives the match.
			name: "refuses_stored_outer",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var held: string = "";
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { held = v; }, None => {} }
    return held.len();
}` + matchedOptstrMain,
			want: 19, wantFrees: 0,
		},
		{
			// REFUSED: the payload is passed to a callee, which may retain it.
			name: "refuses_passed_to_callee",
			src: matchedOptstrW + `function take(s: string): i32 { return s.len(); }
function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return take(v); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19, wantFrees: 0,
		},
		{
			// REFUSED: the payload goes into a container.
			name: "refuses_into_container",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var keep: string[] = [];
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { keep = keep.append(v); }, None => {} }
    return keep.len();
}` + matchedOptstrMain,
			want: 34, wantFrees: 400,
		},
		{
			// REFUSED, and deliberately conservative: `v + "z"` BORROWS the
			// payload, so this one is admissible in principle and is left out
			// because nothing here proves it. Pinned so that widening the
			// allow-list to cover it is a visible, measured change rather than a
			// silent one — it must arrive with its own balance, not by accident.
			name: "refuses_concat_conservative",
			src: matchedOptstrW + `function round(i: i32): i32 {
    var o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { var t: string = v + "z"; return t.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 53, wantFrees: 400,
		},
		{
			// A non-fresh payload: `Some(p)` of a PARAMETER the caller owns.
			// Freshness is required for this family because the payload is stored
			// uncounted and assignment borrows, so an aliased payload would be
			// released under a live reference.
			name: "refuses_aliased_param_payload",
			src: matchedOptstrW + `function wrap(p: string, i: i32): i32 {
    var o: Option[string] = Some(p);
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}
function round(i: i32): i32 { var s: string = w("ab"); return wrap(s, i); }` + matchedOptstrMain,
			want: 19, wantFrees: 0,
		},
	}
}

// TestSelfHostMatchedOptstrReclaimX86_64 — a match-consumed Option[string] local
// reclaims its payload, and every shape that lets the payload escape does not.
func TestSelfHostMatchedOptstrReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "matchoptstr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a payload the "+
					"arm handed out was released under a live reference)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}
			if !tc.balance && frees != tc.wantFrees {
				t.Errorf("%s: %s — want exactly %d frees. A HIGHER count is the "+
					"refusal breaking down and the escaping payload being released; "+
					"a lower one means the probe stopped exercising the path",
					tc.name, summary, tc.wantFrees)
			}
		})
	}
}

// TestSelfHostMatchedOptstrReclaimWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostMatchedOptstrReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping matched Option[string] wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
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
			watFile := filepath.Join(dir, "matchoptstr_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("matched Option[string] wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostMatchedOptstrReclaimIRArm64 — the arm64 sibling under qemu.
func TestSelfHostMatchedOptstrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "matchoptstr_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("matched Option[string] arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
