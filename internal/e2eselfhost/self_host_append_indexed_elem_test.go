package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- An element appended by INDEX is owned by both arrays -------------------
//
// `out = out.append(pre[p])` stored pre's struct box into out without retaining
// it. pre's own release then dropped the box while out still pointed at it, and
// the next string allocation recycled the memory — every op the LICM pass
// (#8245) spliced from its prologue list came back as `invalid` once the
// self-host had compiled the pass, and the stage-2 compiler refused its own
// sources with garbage op kinds (`0x20202020`, four spaces) while the
// native-built driver ran the same code correctly.
//
// The three append forms lower the element at three sites (the `a = a.append`
// statement, the expression-position push, and the clone form); each now asks
// indexed_box_elem_escapes and retains the box. Struct and nested-array
// elements are the affected kinds; string and enum elements were already
// balanced by their own rules and are the controls here, where a second retain
// would show as a leak.
//
// Every want was confirmed against bin/fern -interp and the native x86-64
// backend, never read off the self-host run.

const appendIndexedElemProlog = "struct Op { a: i32, b: i32 }\n" +
	"struct St { ops: Op[] }\n" +
	"enum E { A(i32), B }\n" +
	"function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }\n" +
	"function three(): Op[] { var pre: Op[] = []; var i: i32 = 0; while (i < 3) { pre = pre.append(mkop(i)); i = i + 1; } return pre; }\n" +
	"function from_param(pre: Op[]): Op[] { var out: Op[] = []; var p: i32 = 0; while (p < 3) { out = out.append(pre[p]); p = p + 1; } return out; }\n"

// appendIndexedElemSrc builds the source array inside a one-trip loop body, so
// it is released at the iteration's end while the destination declared outside
// survives; the freed boxes are then recycled through string allocations before
// every element is read back as a digit, so the answer names the element
// sequence rather than a sum several wrong sequences share.
func appendIndexedElemSrc(decl, body, count, readback string) string {
	return appendIndexedElemProlog +
		"function main(): i32 { var t: i32 = 0; " + decl + " var r: i32 = 0; " +
		"while (r < 1) { " + body + " r = r + 1; } " +
		"var junk: string = \"\"; var j: i32 = 0; while (j < 64) { junk = junk + \"    \"; j = j + 1; } " +
		"var k: i32 = 0; while (k < " + count + ") { " + readback + " k = k + 1; } " +
		"if (__rc_underflow() != 0) { return 99; } return t + junk.len() / 64 - 4; }"
}

func appendIndexedElemCases() []struct{ name, src string } {
	const opsOut = "var out: Op[] = [];"
	const opDigit = "t = t * 10 + out[k].a;"
	return []struct{ name, src string }{
		// The statement form, the shape the pass's prologue splice takes.
		{"stmt_local", appendIndexedElemSrc(opsOut,
			"var pre: Op[] = three(); var p: i32 = 0; while (p < 3) { out = out.append(pre[p]); p = p + 1; }",
			"out.len()", opDigit)},
		// Expression position: a var-init per push.
		{"expr_local", appendIndexedElemSrc(opsOut,
			"var pre: Op[] = three(); var o1: Op[] = out.append(pre[0]); var o2: Op[] = o1.append(pre[1]); out = o2.append(pre[2]);",
			"out.len()", opDigit)},
		// The clone form: a struct-field receiver.
		{"clone_local", appendIndexedElemSrc("var st: St = St { ops: [] };",
			"var pre: Op[] = three(); var p: i32 = 0; while (p < 3) { st = St { ops: st.ops.append(pre[p]) }; p = p + 1; }",
			"st.ops.len()", "t = t * 10 + st.ops[k].a;")},
		// The source is a borrowed parameter: the caller's release drops it.
		{"stmt_param", appendIndexedElemSrc(opsOut,
			"var pre: Op[] = three(); out = from_param(pre);",
			"out.len()", opDigit)},
		// The second affected kind: an array element of an array of arrays.
		{"nested_arr", appendIndexedElemSrc("var out: i32[][] = [];",
			"var pre: i32[][] = []; var i: i32 = 0; while (i < 3) { pre = pre.append([i, 7]); i = i + 1; } var p: i32 = 0; while (p < 3) { out = out.append(pre[p]); p = p + 1; }",
			"out.len()", "t = t * 10 + out[k][0];")},
		// Controls: an enum and a string element were already balanced by
		// their own rules, so the leak counters catch a retain added on top.
		{"enum_local", appendIndexedElemSrc("var out: E[] = [];",
			"var pre: E[] = []; var i: i32 = 0; while (i < 3) { pre = pre.append(A(i)); i = i + 1; } var p: i32 = 0; while (p < 3) { out = out.append(pre[p]); p = p + 1; }",
			"out.len()", "match (out[k]) { A(v) => { t = t * 10 + v; }, B => { t = t * 10 + 9; } }")},
		{"str_local", appendIndexedElemSrc("var out: string[] = [];",
			"var pre: string[] = []; var i: i32 = 0; while (i < 3) { pre = pre.append(\"v\" + \"w\"); i = i + 1; } var p: i32 = 0; while (p < 3) { out = out.append(pre[p]); p = p + 1; }",
			"out.len()", "t = t * 10 + out[k].len() - 2 + k;")},
	}
}

// TestSelfHostAppendIndexedElemX86_64 — the elements read back after the
// source array died, and no release ran twice.
//
// The leak counters are read but not balanced: the destination's element walk
// is credited only to a fresh array with no element alias (arrstruct_credit_rows),
// and an index read IS an element alias, so `out` never releases what it holds
// — three boxes stay live in every row, the two controls included, which is
// the reclaim gap docs/rc-log records. A retain removed from the store shows up
// in the digits, never in the counters.
func TestSelfHostAppendIndexedElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range appendIndexedElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "appendindexed_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != 12 {
				t.Fatalf("%s read elements %03d, want 012 (99 = rc underflow; any other "+
					"sequence = the array holds a box its source array freed)", tc.name, exit)
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
			if frees > allocs {
				t.Errorf("%s: %s — more releases than allocations", tc.name, summary)
			}
		})
	}
}

// TestSelfHostAppendIndexedElemWasmIR — the wasm sibling. Exit codes only; the
// leak counters are an x86-64 self-host backend feature.
func TestSelfHostAppendIndexedElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping append indexed elem wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range appendIndexedElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "appendindexed_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != 12 {
				t.Errorf("append indexed elem wasm IR %q read elements %03d, want 012", tc.name, got)
			}
		})
	}
}
