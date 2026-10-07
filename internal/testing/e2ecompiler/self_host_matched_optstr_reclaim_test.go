package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A MATCHED Option[string] local releases its payload (#7710) -------------
//
// Every Option payload kind is reclaimed whether or not a `match` consumes the
// local, and the string kind must be too: matching is the ordinary way to
// consume an Option, and a matched payload left unreleased leaks 72 B/round,
// linear in the round count. A reassigned local whose every rebind is itself
// fresh is reclaimed as well (#7712). The `*_control` rows pin the never-matched
// case and the array payload kinds.
//
// THE REFUSALS ARE THE ESSENTIAL HALF. A string assignment BORROWS, so the arm
// binding takes no retain — which is why freeing a payload the arm hands out
// would be a use-after-free rather than a double-count. The `refuses_*` rows
// exercise the handed-out shapes, an aliased rebind and an escaping reassigned
// local; each must exit its oracle answer, and every row balances.
//
// Every want was confirmed against bin/fern -interp, never read off the
// self-host run under test.
type matchedOptstrCase struct {
	name string
	src  string
	want int
}

const matchedOptstrMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const matchedOptstrW = "function w(a: string): string { return a + \"!\"; }\n"

func matchedOptstrCases() []matchedOptstrCase {
	return []matchedOptstrCase{
		{
			// THE REPRO: matched, payload read. Was 600/0 live 14400.
			name: "matched_payload_read",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19,
		},
		{
			// The binding present but unused — identical leak before, which is
			// what showed this is the LOCAL's reclaim and not the binding's.
			name: "matched_binding_unused",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4,
		},
		{
			// No binding at all. Also leaked before, so the arm binding was never
			// the releaser.
			name: "matched_wildcard",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4,
		},
		{
			// The never-matched quadrant, which already worked. It must stay
			// working AND must not now be credited twice — a second release here
			// would be an over-release, caught by the 99 guard.
			name: "unmatched_control",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    return i % 7;
}` + matchedOptstrMain,
			want: 13,
		},
		{
			// The array payload, matched: the quadrant that always worked and the
			// reason this was diagnosable at all.
			name: "arr_matched_control",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { return a[0]; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 63,
		},
		{
			// The arr-of-arr payload, matched — the OPTARRARR sibling whose
			// collector this one is modelled on.
			name: "arrarr_matched_control",
			src: `function round(i: i32): i32 {
    let o: Option[i32[][]] = Some([[i, i + 1], [i + 2]]);
    match (o) { Some(_) => { return 5; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 4,
		},
		{
			// THE REBIND QUADRANT (#7712): reassigned, every rebind itself fresh.
			// Both the superseded box at the rebind and the final one at exit must
			// be released (900/0 live 21600 when neither was).
			name: "reassigned_all_rebinds_fresh",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(w("cd")); }
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19,
		},
		{
			// The flat-ARRAY payload reassigned: the control.
			name: "reassigned_array_control",
			src: `function round(i: i32): i32 {
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i % 2 == 0) { o = Some([i + 2, i + 3]); }
    match (o) { Some(a) => { return a[0]; }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 14,
		},
		{
			// A rebind that is NOT fresh — `Some(p)` of a parameter the
			// caller owns. Admitting reassignment must not admit an aliased rebind,
			// which would release a live reference at the NEXT rebind.
			name: "refuses_rebind_aliasing_param",
			src: matchedOptstrW + `function run(p: string, i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(p); }
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}
function round(i: i32): i32 { let s: string = w("zz"); return run(s, i); }` + matchedOptstrMain,
			want: 19,
		},
		{
			// Reassigned AND the payload escapes. The escape gate must
			// still apply once reassignment is admitted.
			name: "refuses_reassigned_escaping",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let held: string = "";
    let o: Option[string] = Some(w("ab"));
    if (i % 2 == 0) { o = Some(w("cd")); }
    match (o) { Some(v) => { held = v; }, None => {} }
    return held.len();
}` + matchedOptstrMain,
			want: 19,
		},
		{
			// The arm RETURNS the payload, so the caller owns it.
			// Releasing it here is a use-after-free, not a double count.
			name: "refuses_returned_payload",
			src: matchedOptstrW + `function mk(i: i32): string {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return v; }, None => { return "z"; } }
    return "y";
}
function round(i: i32): i32 { let s: string = mk(i); return s.len(); }` + matchedOptstrMain,
			want: 19,
		},
		{
			// The payload is stored into a local that outlives the match.
			name: "refuses_stored_outer",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let held: string = "";
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { held = v; }, None => {} }
    return held.len();
}` + matchedOptstrMain,
			want: 19,
		},
		{
			// The payload is passed to a callee, which may retain it.
			name: "refuses_passed_to_callee",
			src: matchedOptstrW + `function take(s: string): i32 { return s.len(); }
function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { return take(v); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 19,
		},
		{
			// The payload goes into a container.
			name: "refuses_into_container",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let keep: string[] = [];
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { keep = keep.append(v); }, None => {} }
    return keep.len();
}` + matchedOptstrMain,
			want: 34,
		},
		{
			// `v + "z"` BORROWS the payload.
			name: "refuses_concat_conservative",
			src: matchedOptstrW + `function round(i: i32): i32 {
    let o: Option[string] = Some(w("ab"));
    match (o) { Some(v) => { let t: string = v + "z"; return t.len(); }, None => { return 2; } }
    return 0;
}` + matchedOptstrMain,
			want: 53,
		},
		{
			// A non-fresh payload: `Some(p)` of a PARAMETER the caller owns.
			// Freshness is required for this family because the payload is stored
			// uncounted and assignment borrows, so an aliased payload would be
			// released under a live reference.
			name: "refuses_aliased_param_payload",
			src: matchedOptstrW + `function wrap(p: string, i: i32): i32 {
    let o: Option[string] = Some(p);
    match (o) { Some(v) => { return v.len(); }, None => { return 2; } }
    return 0;
}
function round(i: i32): i32 { let s: string = w("ab"); return wrap(s, i); }` + matchedOptstrMain,
			want: 19,
		},
	}
}

// TestSelfHostMatchedOptstrReclaimX86_64 — a match-consumed Option[string] local
// reclaims its payload, and every shape that lets the payload escape does not.
func TestSelfHostMatchedOptstrReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
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
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range matchedOptstrCases() {
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
