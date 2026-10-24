package e2ecompiler

import (
	"os/exec"
	"testing"
)

var tupleFnIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"returned-capturing", "function mk(): ((i32) => i32, i32) { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return t; } function main(): i32 { let t = mk(); return t.0(37); }", 42},
	{"returned-nocapture", "function mk(): ((i32) => i32, i32) { let t = ((x: i32): i32 => { return x + 1; }, 1); return t; } function main(): i32 { let t = mk(); return t.0(41); }", 42},
	{"local-tuple", "function main(): i32 { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return t.0(37); }", 42},
	{"named-fn-local", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { let t = (dbl, 1); return t.0(21); }", 42},
	{"named-fn-returned", "function dbl(x: i32): i32 { return x * 2; } function mk(): ((i32) => i32, i32) { return (dbl, 1); } function main(): i32 { let t = mk(); return t.0(21); }", 42},
	{"two-closures", "function mk(): ((i32) => i32, (i32) => i32) { let n = 1; let m = 2; let t = ((x: i32): i32 => { return x + n; }, (x: i32): i32 => { return x + m; }); return t; } function main(): i32 { let t = mk(); return t.0(19) + t.1(20); }", 42},
	{"destructure-call", "function mk(): ((i32) => i32, i32) { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 5); return t; } function main(): i32 { let (f, k) = mk(); return f(32) + k; }", 42},
	{"scalar-tuple-regress", "function mk(): (string, i32) { return (\"hello\", 37); } function main(): i32 { let t = mk(); return t.0.len() + t.1; }", 42},
	{"tuple-fn-param", "function callit(t: ((i32) => i32, i32)): i32 { return t.0(37); } function main(): i32 { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return callit(t); }", 42},
	{"option-clo-payload-local", "function main(): i32 { let n = 5; let o = Some((x: i32): i32 => { return x + n; }); match (o) { Some(f) => { return f(37); }, None => { return 0; } } }", 42},
	{"option-fn-payload-annotated", "function main(): i32 { let n = 5; let o: Option[(i32) => i32] = Some((x: i32): i32 => { return x + n; }); match (o) { Some(f) => { return f(37); }, None => { return 0; } } }", 42},
	{"option-zeroarg-fnname-payload", "function a1(): i32 { return 3; } function main(): i32 { let o: Option[() => i32] = Some(a1); match (o) { Some(f) => { return f(); }, None => { return 0; } } }", 3},
	{"option-onearg-fnname-payload", "function inc(x: i32): i32 { return x + 1; } function main(): i32 { let o: Option[(i32) => i32] = Some(inc); match (o) { Some(f) => { return f(41); }, None => { return 0; } } }", 42},
	{"enum-fn-payload-regress", "enum Op { Apply((i32) => i32), Nop } function main(): i32 { let n = 5; let o = Op.Apply((x: i32): i32 => { return x + n; }); match (o) { Apply(f) => { return f(37); }, Nop => { return 0; } } }", 42},
	{"nested-tuple-clo-via-binding", "function main(): i32 { let n = 5; let t = (((x: i32): i32 => { return x + n; }, 1), 2); let inner = t.0; return inner.0(37); }", 42},
	{"nested-tuple-scalar-via-binding", "function main(): i32 { let t = ((7, 1), 2); let inner = t.0; return inner.0 + 35; }", 42},
	{"scalar-call-elem", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { let u = (add(1, 2), 4); return u.0 + u.1 + 35; }", 42},
	{"clo-elem-call-in-tuple", "function main(): i32 { let k = 4; let t = ((x: i32): i32 => { return x + k; }, k); let u = (t.0(3), t.1); return u.0 + u.1 + 31; }", 42},
	{"loop-tuple-clo-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 1000) { let k = i % 7; let t = ((x: i32): i32 => { return x + k; }, k); let u = (t.0(3), t.1); acc = (acc + u.0 + u.1) % 1000; i = i + 1; } return acc % 256; }", 226},
	{"direct-chain-call", "function main(): i32 { let n = 5; let t = (((x: i32): i32 => { return x + n; }, 1), 2); return t.0.0(37); }", 42},
	{"direct-chain-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 500) { let k = i % 5; let t = (((x: i32): i32 => { return x + k; }, k), i % 3); acc = (acc + t.0.0(2) + t.1) % 1000; i = i + 1; } return acc % 256; }", 243},
	{"arrtuple-elem-binding-call", "function main(): i32 { let k = 4; let a = [((x: i32): i32 => { return x + k; }, k)]; let t = a[0]; return t.0(3) + t.1 + 31; }", 42},
	{"arrtuple-elem-inline-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 300) { let k = i % 6; let a = [((x: i32): i32 => { return x + k; }, k), ((x: i32): i32 => { return x * 2 + k; }, k + 1)]; let j = 0; while (j < a.len()) { acc = (acc + a[j].0(2) + a[j].1) % 1000; j = j + 1; } i = i + 1; } return acc % 256; }", 100},
	{"string-capture-tuple-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 200) { let s = \"ab\" + \"c\"; let t = ((x: i32): i32 => { return x + s.len(); }, i % 4); acc = (acc + t.0(2) + t.1) % 1000; i = i + 1; } return acc % 256; }", 44},
}

func TestSelfHostTupleFnIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTupleFnIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
