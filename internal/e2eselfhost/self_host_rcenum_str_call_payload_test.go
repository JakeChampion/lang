package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A STRING payload built by a fresh-producer CALL (#7364) -----------------
//
// `R.Full(w("x"))` — a user function's result as the variant's string payload —
// was refused by variant_struct_payloads_fresh, whose freshness set was purely
// syntactic (literal / concat / named builtin / string method). The refusal cost
// the whole enum its "RCENUM:"/"RCENUMS:" credit, so nothing swept the local at
// all: 150/0, 72 B/round, against a balanced native, in both the exit-sweep and
// the match-consumed shapes. The byte-identical program with the concat INLINED
// was already credited, so factoring the payload through a function was the
// entire difference.
//
// The fix consults str_fresh_ret_fns — the whole-program fixpoint of free
// functions whose every return is a fresh sole-owned string box, the registry
// every other owner of a fresh-string verdict already uses — threaded into the
// admission gates that decide reclaim credits. The strict syntactic set still
// applies where the registry is out of reach (struct-literal enum fields, array
// elements, the RCE: registration proof).
//
// The refusal row is half the point: a callee that returns its PARAMETER is not
// in the registry, stays refused, and its box keeps its sound leak — MORE frees
// there would mean the sweep freeing a box the caller still owns.
//
// Every want was confirmed against BOTH oracles (bin/fern -interp and the
// native x86-64 backend agreed on each); alloc/free counts are the self-host
// build's own, pinned exactly (the 3-vs-1 allocs-per-string ratio against
// native is #7351, not this change).

type rcEnumStrCallCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func rcEnumStrCallCases() []rcEnumStrCallCase {
	return []rcEnumStrCallCase{
		{
			// THE REPRO (#7364): exit-sweep shape, if-block local, producer call
			// payload. Base: 150/0, 3600 live bytes.
			name: "call_payload_if_block",
			src: `enum R { Full(string), Empty }
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: R = R.Full(w("x")); t = t + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 50, allocs: 150, frees: 150,
		},
		{
			// The match-consumed sibling (consumed_rcpayload_enum_frees), same
			// payload. Base: 300/0, 7200 live bytes.
			name: "call_payload_match_consumed",
			src: `enum R { Full(string), Empty }
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var o: R = R.Full(w("x"));
    match (o) { R.Full(s) => { return s.len(); }, R.Empty => { return 0; } }
    return 0;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		},
		{
			// The REBIND path (all_assigns_fresh_rcenum): every assignment a
			// fresh producer-call construction, superseded chain plus final
			// value all released.
			name: "call_payload_rebind",
			src: `enum R { Full(string), Empty }
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: R = R.Full(w("x")); o = R.Full(w("yz")); t = t + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 50, allocs: 300, frees: 300,
		},
		{
			// CONTROL — a literal payload was already credited. Must stay
			// balanced and unchanged.
			name: "ctl_literal_payload",
			src: `enum R { Full(string), Empty }
function round(i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: R = R.Full("x"); t = t + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 50, allocs: 50, frees: 50,
		},
		{
			// REFUSED — the callee returns its PARAMETER, so the payload
			// aliases `keep`, a live local this frame's own sweep releases.
			// str_fresh_ret_fns refuses a bare-ident return, the enum stays
			// uncredited, and the box keeps its sound leak. MORE frees here is
			// the over-release this gate exists to prevent; exit 99 would be
			// the underflow counter catching exactly that.
			name: "refused_alias_returning_callee",
			src: `enum R { Full(string), Empty }
function w(a: string): string { return a + "!"; }
function id(a: string): string { return a; }
function round(i: i32): i32 {
    var keep: string = w("base");
    var t: i32 = 0;
    if (i % 2 == 0) { var o: R = R.Full(id(keep)); t = t + 1; }
    t = t + keep.len();
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 52, allocs: 250, frees: 0,
		}}
}

// TestSelfHostRcEnumStrCallPayloadX86_64 — a string payload from a
// str_fresh_ret_fns producer call earns the enum the same reclaim credit an
// inline concat does, and an alias-returning callee stays refused.
func TestSelfHostRcEnumStrCallPayloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcEnumStrCallCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcenumstrcall_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow — a refused "+
					"shape got credited and freed a live box)", tc.name, exit, tc.want)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. MORE on the refused row means the "+
					"credit reached an aliasing callee; FEWER on an admitted row means "+
					"the registry consultation stopped resolving", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostRcEnumStrCallPayloadWasmIR — the wasm sibling. Exit codes only:
// leak rows do not move one, so this leg catches a release that frees a LIVE
// box on wasm.
func TestSelfHostRcEnumStrCallPayloadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping rc-enum string-call payload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range rcEnumStrCallCases() {
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
			watFile := filepath.Join(dir, "rcenumstrcall_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("rc-enum string-call payload wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostRcEnumStrCallPayloadIRArm64 — the arm64 sibling under qemu.
func TestSelfHostRcEnumStrCallPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcEnumStrCallCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "rcenumstrcall_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
