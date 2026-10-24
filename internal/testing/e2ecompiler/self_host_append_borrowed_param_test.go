package e2ecompiler

import (
	"os/exec"
	"testing"
)

var selfHostAppendBorrowedCases = []struct {
	name string
	src  string
	exit int
}{
	{"bare-param", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    a = grow(a, 1);\n    a = grow(a, 2);\n    let before: i32 = a.len();\n    let c: i32[] = grow(a, 3);\n    let after: i32 = a.len();\n    return before * 10 + after;\n}", 22},
	{"transitive", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction via(xs: i32[], x: i32): i32[] {\n    return grow(xs, x);\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    a = via(a, 1);\n    a = via(a, 2);\n    let before: i32 = a.len();\n    let c: i32[] = via(a, 3);\n    let after: i32 = a.len();\n    return before * 10 + after;\n}", 22},
	{"return-borrow", "function tail(acc: i32[], x: i32): i32[] {\n    return acc.append(x);\n}\nfunction caller(acc: i32[]): i32 {\n    let r = tail(acc, 9);\n    return r.len() * 10 + acc.len();\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    a = a.append(1);\n    a = a.append(2);\n    return caller(a) + 10;\n}", 42},
	{"recursive-acc", "function walk(acc: i32[], n: i32): i32[] {\n    if (n == 0) { return acc; }\n    return walk(acc.append(n), n - 1);\n}\nfunction main(): i32 {\n    let out = walk([], 40);\n    return out.len() + 2;\n}", 42},
	{"struct-field-regress", "struct Box { xs: i32[] }\nfunction (b: Box) push(x: i32): Box {\n    let ys: i32[] = b.xs.append(x);\n    return Box { xs: ys };\n}\nfunction main(): i32 {\n    let a: Box = Box { xs: [] };\n    a = a.push(1);\n    a = a.push(2);\n    let before: i32 = a.xs.len();\n    let c: Box = a.push(3);\n    let after: i32 = a.xs.len();\n    return before * 10 + after;\n}", 22},
	{"selfreassign-chain", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    let i: i32 = 0;\n    while (i < 30) { a = grow(a, i); i = i + 1; }\n    return a.len() + 12;\n}", 42},
	{"binding-aliases-param", "function leaf(p: i32[]): i32[] {\n    return p.append(3);\n}\nfunction bind(p: i32[]): i32[] {\n    let q: i32[] = p.append(99);\n    q = leaf(q);\n    return q;\n}\nfunction main(): i32 {\n    let a: i32[] = [];\n    a = a.append(1);\n    a = a.append(2);\n    a = a.append(3);\n    a = bind(a);\n    return a.len() * 10 + __rc_underflow_count();\n}", 50},
	{"chained-tx-underflow", "struct TxR2 { out: i32[], next: i32 }\nfunction inner(buf: i32[], p: i32, out: i32[]): TxR2 {\n    out = out.append(buf[p]);\n    return TxR2 { out: out, next: p + 1 };\n}\nfunction outer(buf: i32[], p: i32, out: i32[]): TxR2 {\n    out = out.append(buf[p]);\n    let ne: TxR2 = inner(buf, p + 1, out);\n    out = ne.out;\n    return inner(buf, ne.next, out);\n}\nfunction main(): i32 {\n    let buf: i32[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];\n    let none: i32[] = [];\n    let p: i32 = 0;\n    let i: i32 = 0;\n    while (i < 4) {\n        let skip: TxR2 = outer(buf, p, none);\n        p = skip.next;\n        i = i + 1;\n    }\n    return p * 5 + (__rc_underflow_count() % 5);\n}", 60},
}

func TestSelfHostAppendBorrowedParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostAppendBorrowedCases {
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

func TestSelfHostAppendBorrowedParamArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostAppendBorrowedCases {
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
