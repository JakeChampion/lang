package e2ecompiler

import (
	"os/exec"
	"testing"
)

var tupleFnZeroArgCases = []struct {
	name string
	src  string
	exit int
}{
	{"tuple-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { let t: ((() => i32), i32) = (a1, 4); return t.0() + t.1; }", 7},
	{"tuple-in-array-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { let xs: ((() => i32), i32)[] = [(a1, 4)]; return xs[0].0() + xs[0].1; }", 7},
	{"tuple-two-fn-elems", "function a1(): i32 { return 3; }\nfunction a2(): i32 { return 5; }\nfunction main(): i32 { let t: ((() => i32), (() => i32)) = (a1, a2); return t.0() * 10 + t.1(); }", 35},
	{"tuple-onearg", "function a1(x: i32): i32 { return x + 3; }\nfunction main(): i32 { let t: (((i32) => i32), i32) = (a1, 4); return t.0(1) + t.1; }", 8},
	{"tuple-lambda", "function main(): i32 { let t: ((() => i32), i32) = (() => 3, 4); return t.0() + t.1; }", 7},

	{"const-in-tuple", "const K: i32 = 9;\nfunction main(): i32 { let t: (i32, i32) = (K, 4); return t.0 + t.1; }", 13},
	{"const-read-plain", "const K: i32 = 9;\nfunction main(): i32 { return K + 1; }", 10},
	{"const-and-fn-mixed", "const K: i32 = 9;\nfunction a1(): i32 { return 3; }\nfunction main(): i32 { let t: ((() => i32), i32) = (a1, K); return t.0() + t.1; }", 12},
	{"unannotated-tuple-const", "const K: i32 = 9;\nfunction main(): i32 { let t = (K, 4); return t.0 + t.1; }", 13},

	{"option-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { let o: Option[() => i32] = Some(a1); match (o) { Some(f) => { return f(); }, None => { return 0; } } return 9; }", 3},
	{"result-ok-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { let r: Result[() => i32, string] = Ok(a1); match (r) { Ok(f) => { return f(); }, Err(e) => { return 0; } } return 9; }", 3},
	{"result-err-zeroarg", "function a1(): i32 { return 3; }\nfunction main(): i32 { let r: Result[i32, () => i32] = Err(a1); match (r) { Ok(v) => { return v; }, Err(f) => { return f(); } } return 9; }", 3},
	{"option-onearg", "function a1(x: i32): i32 { return x + 3; }\nfunction main(): i32 { let o: Option[(i32) => i32] = Some(a1); match (o) { Some(f) => { return f(1); }, None => { return 0; } } return 9; }", 4},
	{"option-lambda", "function main(): i32 { let o: Option[() => i32] = Some(() => 3); match (o) { Some(f) => { return f(); }, None => { return 0; } } return 9; }", 3},
	{"option-none", "function a1(): i32 { return 3; }\nfunction main(): i32 { let o: Option[() => i32] = None; match (o) { Some(f) => { return f(); }, None => { return 5; } } return 9; }", 5},
	{"option-const-payload", "const K: i32 = 9;\nfunction main(): i32 { let o: Option[i32] = Some(K); match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 9},
	{"result-const-payload", "const K: i32 = 9;\nfunction main(): i32 { let r: Result[i32, string] = Ok(K); match (r) { Ok(v) => { return v; }, Err(e) => { return 0; } } return 9; }", 9},
	{"option-unannotated-const", "const K: i32 = 9;\nfunction main(): i32 { let o = Some(K); match (o) { Some(v) => { return v; }, None => { return 0; } } return 9; }", 9},

	{"returned-zeroarg", "function a1(): i32 { return 3; }\nfunction get(): () => i32 { return a1; }\nfunction main(): i32 { let g: () => i32 = get(); return g(); }", 3},
	{"returned-onearg", "function a1(x: i32): i32 { return x + 3; }\nfunction get(): (i32) => i32 { return a1; }\nfunction main(): i32 { let g: (i32) => i32 = get(); return g(1); }", 4},
	{"returned-lambda", "function get(): () => i32 { return () => 3; }\nfunction main(): i32 { let g: () => i32 = get(); return g(); }", 3},
	{"user-enum-zeroarg", "enum E { Wrap(() => i32), Nil }\nfunction a1(): i32 { return 3; }\nfunction main(): i32 { let e: E = Wrap(a1); match (e) { Wrap(f) => { return f(); }, Nil => { return 0; } } return 9; }", 3},
	{"user-enum-lambda", "enum E { Wrap(() => i32), Nil }\nfunction main(): i32 { let e: E = Wrap(() => 3); match (e) { Wrap(f) => { return f(); }, Nil => { return 0; } } return 9; }", 3},
	{"const-return", "const K: i32 = 9;\nfunction get(): i32 { return K; }\nfunction main(): i32 { return get() + 1; }", 10},
	{"const-enum-payload", "enum E { Wrap(i32), Nil }\nconst K: i32 = 9;\nfunction main(): i32 { let e: E = Wrap(K); match (e) { Wrap(v) => { return v; }, Nil => { return 0; } } return 9; }", 9},
}

func TestSelfHostTupleFnZeroArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnZeroArgCases {
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

func TestSelfHostTupleFnZeroArgIRWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleFnZeroArgCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
