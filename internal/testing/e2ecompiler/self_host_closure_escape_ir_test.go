package e2ecompiler

import (
	"os/exec"
	"testing"
)

var closureEscapeCases = []struct {
	name string
	src  string
	exit int
}{
	{"escape-noncapture", "function mk(): (i32) => i32 { let f = (b: i32): i32 => { return b + 1; }; return f; } function main(): i32 { let g = mk(); return g(5); }", 6},
	{"escape-capture", "function adder(a: i32): (i32) => i32 { let f = (b: i32): i32 => { return a + b; }; return f; } function main(): i32 { let add10 = adder(10); return add10(6); }", 16},
	{"escape-counter", "function make_counter(): () => i32 { let x = 0; let inc = (): i32 => { x = x + 1; return x; }; return inc; } function main(): i32 { let c = make_counter(); let a = c(); let b = c(); return a + b; }", 3},
	{"escape-zero-arg", "function mk(): () => i32 { let f = (): i32 => { return 11; }; return f; } function main(): i32 { let g = mk(); return g(); }", 11},
	{"transitive-factory", "function makeAdder(): (i32) => i32 { let g = (x: i32): i32 => { return x + 1; }; return g; } function outer(): (i32) => i32 { return makeAdder(); } function main(): i32 { let f = outer(); return f(41); }", 42},
	{"nested-factory", "function outer(): (i32) => i32 { let inner = (): (i32) => i32 => { let g = (x: i32): i32 => { return x + 1; }; return g; }; return inner(); } function main(): i32 { let f = outer(); return f(41); }", 42},
	{"escape-captures-fn", "function mk(base: (i32) => i32): (i32) => i32 { let f = (x: i32): i32 => { return base(x); }; return f; } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { let g = mk(dbl); return g(21); }", 42},
	{"escape-compose-twice", "function twice(f: (i32) => i32): (i32) => i32 { let g = (x: i32): i32 => { return f(f(x)); }; return g; } function inc(x: i32): i32 { return x + 1; } function main(): i32 { let d = twice(inc); return d(40); }", 42},
	{"return-closure-array-via-var", "function mk(): ((i32) => i32)[] { let n = 5; let a = [(x: i32): i32 => { return x + n; }]; return a; } function main(): i32 { let arr = mk(); return arr[0](37); }", 42},
	{"return-closure-array-direct", "function mk(): ((i32) => i32)[] { return [(x: i32): i32 => { return x + 1; }]; } function main(): i32 { let arr = mk(); return arr[0](41); }", 42},
	{"closure-array-param", "function mk(): ((i32) => i32)[] { let n = 5; let a = [(x: i32): i32 => { return x + n; }]; return a; } function consume(fns: ((i32) => i32)[]): i32 { return fns[0](37); } function main(): i32 { let arr = mk(); return consume(arr); }", 42},
	{"bare-fnarr-param", "function apply(fns: ((i32) => i32)[], n: i32): i32 { let s = 0; let i = 0; while (i < fns.len()) { s = s + fns[i](n); i = i + 1; } return s; } function inc(n: i32): i32 { return n + 1; } function dbl(n: i32): i32 { return n * 2; } function main(): i32 { return apply([inc, dbl], 10); }", 31},
	{"closure-array-direct-index-call", "function mk(): ((i32) => i32)[] { let n = 5; let a = [(x: i32): i32 => { return x + n; }]; return a; } function main(): i32 { return mk()[0](37); }", 42},
	{"ifelse-branch-factory", "function mk(flag: boolean): (i32) => i32 { if (flag) { let f = (x: i32): i32 => { return x + 1; }; return f; } else { let g = (x: i32): i32 => { return x + 2; }; return g; } } function main(): i32 { let a = mk(true); let b = mk(false); return a(20) + b(19); }", 42},
	{"transitive-closure-array-factory", "function mk(): ((i32) => i32)[] { let n = 5; let a = [(x: i32): i32 => { return x + n; }]; return a; } function outer(): ((i32) => i32)[] { return mk(); } function main(): i32 { let arr = outer(); return arr[0](37); }", 42},
	{"direct-return", "function adder(a: i32): (i32) => i32 { return (b: i32): i32 => { return a + b; }; } function main(): i32 { let add10 = adder(10); return add10(5); }", 15},
	{"capture-in-guard", "enum Opt { Has(i32), Empty } function apply(f: () => i32): i32 { return f(); } function main(): i32 { let threshold: i32 = 5; let o: Opt = Opt.Has(7); let f = () => match (o) { Has(v) when v > threshold => 100, _ => 0 }; return apply(f); }", 100},
	{"nested-capture-transitive", "function main(): i32 { let a: i32 = 10; let outer = () => { let b: i32 = 20; let inner = () => a + b; inner() }; return outer(); }", 30},
	{"nested-capture-transitive-only", "function main(): i32 { let a: i32 = 10; let outer = () => { let inner = () => a + 1; inner() }; return outer(); }", 11},
}

func TestSelfHostClosureEscapeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range closureEscapeCases {
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

func TestSelfHostClosureEscapeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range closureEscapeCases {
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
