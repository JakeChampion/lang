package e2ecompiler

import (
	"testing"
)

const appendParamElemProlog = "struct Op { a: i32, b: i32 }\n" +
	"struct St { ops: Op[] }\n" +
	"function mkop(i: i32): Op { return Op { a: i, b: i + 1 }; }\n" +
	"function emitf(s: St, o: Op): St { return St { ops: s.ops.append(o) }; }\n" +
	"function (s: St) emit(o: Op): St { return St { ops: s.ops.append(o) }; }\n" +
	"function emito(s: St, own o: Op): St { return St { ops: s.ops.append(o) }; }\n"

func appendParamElemSrc(inner string) string {
	return appendParamElemProlog +
		"function main(): i32 { let t: i32 = 0; let st: St = St { ops: [] }; let j: i32 = 0; " +
		"while (j < 3) { " + inner + " j = j + 1; } " +
		"let k: i32 = 0; while (k < st.ops.len()) { t = t * 10 + st.ops[k].a; k = k + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return t; }"
}

func appendParamElemCases() []struct{ name, inner string } {
	return []struct{ name, inner string }{
		{"freefn_local", "let o: Op = mkop(j); st = emitf(st, o);"},
		{"method_local", "let o: Op = mkop(j); st = st.emit(o);"},
		{"lit_local", "let o: Op = Op { a: j, b: j + 1 }; st = st.emit(o);"},
		{"inline_local", "let o: Op = mkop(j); st = St { ops: st.ops.append(o) };"},
		{"temp", "st = st.emit(mkop(j));"},
		{"own_param", "st = emito(st, mkop(j));"},
	}
}

func TestSelfHostAppendParamElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostAppendParamElemWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range appendParamElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := appendParamElemSrc(tc.inner)
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != 12 {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, 12, stderr)
				}
			}
		})
	}
}
