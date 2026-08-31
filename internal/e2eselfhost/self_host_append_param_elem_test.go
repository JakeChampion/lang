package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A struct element a CALLEE appends is owned by the array, not the caller
//
// `emitf(s, o) { return St { ops: s.ops.append(o) }; }` — the shape the
// self-host's own LowerState/EmitState threading is built from — stored the
// caller's box into the array without retaining it. The caller then released it
// on the binding's next rebind, and the array was left pointing at freed
// memory.
//
// It surfaced as a WRONG ANSWER, and every counter read healthy while it did.
// Reading the elements back after three appends, `t = t * 10 + st.ops[k].a`:
//
//	                                  answer   allocs/frees   live
//	callee appends a named local       212      1800 / 1800      0
//	same, elements never read           ok      1800 / 1800      0
//	append written inline in main       012      1800 / 1404  19008
//
// Both oracles say 012. The middle row is why nothing caught it: with only a
// `.len()` read the dangling element is never dereferenced. The bottom row is
// correct only because it LEAKS — nothing is recycled into the dead box.
//
// The threshold is the freelist, not a capacity boundary: one and two appends
// answer correctly because no allocation recycles the freed box before the
// read; the third one does, and elements 0 and 2 then alias — hence 212.
//
// The fix is the counted-store contract, in the two halves it always takes.
// `lower_arr_append_value` retains a struct element that is a borrowed
// PARAMETER (the enum arm directly above it has done this since #6049), and
// `param_counted_of` grows a "PCNT:" tier so a caller handing over a fresh temp
// emits the matching post-call release. Either half alone leaks: the retain
// with no credit leaves the temp unreleased, the credit with no retain releases
// a box nobody counted.
//
// A LOCAL is excluded from the retain, and that exclusion is the whole
// precision of the test: a local that escapes into a container has already lost
// its reclaim credit to the escape walk, so the container holds the only
// reference and retaining there would leak. An `own` parameter is excluded from
// the other side — the caller transferred its reference rather than keeping
// one.
//
// Every want below was confirmed against BOTH oracles — bin/fern -interp and
// the native x86-64 backend agreed on each — never read off the self-host run.

const appendParamElemProlog = "struct Op { a: i32, b: i32 }\n" +
	"struct St { ops: Op[] }\n" +
	"function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }\n" +
	"function emitf(s: St, o: Op): St { return St { ops: s.ops.append(o) }; }\n" +
	"function (s: St) emit(o: Op): St { return St { ops: s.ops.append(o) }; }\n" +
	"function emito(s: St, own o: Op): St { return St { ops: s.ops.append(o) }; }\n"

// appendParamElemSrc reads every element back as a digit, so the answer names
// the exact element sequence rather than a sum that several wrong sequences
// share. Three appends: the first count at which the freed box is recycled.
func appendParamElemSrc(inner string) string {
	return appendParamElemProlog +
		"function main(): i32 { var t: i32 = 0; var st: St = St { ops: [] }; var j: i32 = 0; " +
		"while (j < 3) { " + inner + " j = j + 1; } " +
		"var k: i32 = 0; while (k < st.ops.len()) { t = t * 10 + st.ops[k].a; k = k + 1; } " +
		"if (__rc_underflow() != 0) { return 99; } return t; }"
}

func appendParamElemCases() []struct{ name, inner string } {
	return []struct{ name, inner string }{
		// The three rows that answered 212. A free function and a method are
		// the same defect; so is a local bound from a literal rather than a
		// producer call.
		{"freefn_local", "var o: Op = mkop(j); st = emitf(st, o);"},
		{"method_local", "var o: Op = mkop(j); st = st.emit(o);"},
		{"lit_local", "var o: Op = Op { a: j, b: j + 1 }; st = st.emit(o);"},
		// The append written in the CALLER. No parameter, so no retain may
		// fire — the local's own escape already gave the array sole ownership,
		// and a retain here would leak.
		{"inline_local", "var o: Op = mkop(j); st = St { ops: st.ops.append(o) };"},
		// A fresh temp handed to the callee: the "PCNT:" credit's own row. The
		// retain fires inside the callee, and the caller's post-call release is
		// what nets it.
		{"temp", "st = st.emit(mkop(j));"},
		// An `own` parameter — a reference the caller TRANSFERRED. Retaining it
		// would leave the array holding two claims and the caller none. (The
		// checker refuses a borrowed local at an owned position outright, so a
		// fresh argument is the only way to reach this arm.)
		{"own_param", "st = emito(st, mkop(j));"},
	}
}

// TestSelfHostAppendParamElemX86_64 — the element reads back, and the box is
// released exactly once.
//
// The answer carries the use-after-free (a recycled box shows as a wrong digit
// sequence) and `__rc_underflow()` carries the over-release; `allocs == frees`
// at `live_bytes == 0` carries the leak. All three are needed here: this defect
// had healthy counters throughout, so counts alone would have passed the broken
// compiler.
func TestSelfHostAppendParamElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range appendParamElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, appendParamElemSrc(tc.inner), []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "appendparamelem_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != 12 {
				t.Fatalf("%s read elements %03d, want 012 (99 = rc underflow; any "+
					"other digit sequence = the array holds a box the caller freed)", tc.name, exit)
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
				t.Errorf("%s: %s — the retain and its post-call release must net to "+
					"the one reference the array owns", tc.name, summary)
			}
		})
	}
}

// TestSelfHostAppendParamElemWasmIR — the wasm sibling. Exit codes only; the
// leak counters are an x86-64 self-host backend feature.
func TestSelfHostAppendParamElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping append param elem wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range appendParamElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(appendParamElemSrc(tc.inner)))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "appendparamelem_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != 12 {
				t.Errorf("append param elem wasm IR %q read elements %03d, want 012", tc.name, got)
			}
		})
	}
}
