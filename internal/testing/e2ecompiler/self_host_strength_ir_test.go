package e2ecompiler

import (
	"testing"
)

var strengthIRCases = []struct {
	name string
	main string
}{
	{"mul-pow2", `function main(): i32 { let x: i32 = 5; return x * 8; }`},
	{"mul-pow2-chain", `function main(): i32 { let x: i32 = 3; return x * 4 * 2; }`},
	{"mul-zero-stack-balance", `@noinline function f(): i32 { return 3; }
@noinline function g(): i32 { return 7; }
function main(): i32 { let a: i32 = f() * 0; let b: i32 = g(); return a + b; }`},
	{"identities", `function main(): i32 { let x: i32 = 42; return (((x + 0) - 0) | 0) ^ 0; }`},
	{"and-all-bits", `function main(): i32 { let x: i32 = 42; return x & (0 - 1); }`},
	{"shift-by-zero", `function main(): i32 { let x: i32 = 42; return (x << 0) >> 0; }`},
	{"div-rem-one", `function main(): i32 { let x: i32 = 42; return (x / 1) + (x % 1); }`},
	{"signed-negative-div-rem", `function main(): i32 { let x: i32 = 0 - 9; return (0 - (x / 8)) + (0 - (x % 8)); }`},
	{"unsigned-div-pow2", `function main(): i32 { let x: u32 = 40 as u32; return (x / (8 as u32)) as i32; }`},
	{"unsigned-rem-pow2", `function main(): i32 { let x: u32 = 41 as u32; return (x % (8 as u32)) as i32; }`},
	{"unsigned-div-high-bit", `function main(): i32 { let x: u32 = 4294967288 as u32; return ((x / (8 as u32)) % (100 as u32)) as i32; }`},
	{"const-shift-count-masked", `function main(): i32 { let x: i32 = 1; return x << 33; }`},
	{"const-shift-arith-right", `function main(): i32 { let x: i32 = 0 - 16; return 0 - (x >> 34); }`},
	{"const-shift-logical-right", `function main(): i32 { let x: u32 = 4294967232 as u32; return ((x >> (3 as u32)) % (100 as u32)) as i32; }`},
	{"variable-shift-count", `function main(): i32 { let x: i32 = 1; let n: i32 = 33; return x << n; }`},
}

func TestSelfHostStrengthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range strengthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}

func TestSelfHostStrengthShiftImmediateShape(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "asm_ir_run")
	src := []byte("@noinline function shift8(x: i32): i32 { return x * 8; }\nfunction main(): i32 { return shift8(5); }\n")

	for _, tc := range []struct {
		target  string
		want    string
		unwant  []string
		extra   []string
		backend string
	}{
		{
			target:  "x86-64-linux",
			want:    `shlq \$3, %r[a-z0-9]+`,
			unwant:  []string{"imulq", "andl \\$31"},
			backend: "asm_ir.fern",
		},
		{
			target:  "arm64-linux",
			want:    `lsl x[0-9]+, x[0-9]+, #3`,
			unwant:  []string{`mul x[0-9]+, x[0-9]+, x[0-9]+`, `and x[0-9]+, x[0-9]+, #31`},
			extra:   []string{"-target", "arm64-linux"},
			backend: "asm_arm64_ir.fern",
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			asm := string(runCapture(t, gcc, runner, driverBin, src, tc.extra...))
			if !matchShape(asm, tc.want) {
				t.Errorf("%s emitted no %q for `x * 8`:\n%s", tc.backend, tc.want, asm)
			}
			for _, u := range tc.unwant {
				if matchShape(asm, u) {
					t.Errorf("%s still emits %q for `x * 8`; the strength rewrite\n"+
						"or the constant-shift-count selection is not firing:\n%s", tc.backend, u, asm)
				}
			}
		})
	}
}
