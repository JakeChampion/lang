package e2eselfhost

import (
	"strings"
	"testing"
)

// untypedLiteralFoldCases are arithmetic on unsuffixed literals whose type is
// the context's: a const's declared type, or a binding's. The AST folder runs
// before the checker, so it must not fold at i32 what runs at another width
// (#10758). Each const case compares the folded form with the same expression
// computed at run time, as TestConstFoldMatchesRuntimeAtDeclaredWidth does in
// internal/e2e; each program exits 0 when they agree.
var untypedLiteralFoldCases = []struct {
	name, ty, expr, seed string
}{
	{"i32_add_overflow_then_halve", "i32", "(@ + 1) / 2", "2147483647"},
	{"i32_shift_count_over_width", "i32", "(@ << 33) & 255", "1"},
	{"u8_add_wraps_at_8_bits", "u8", "(@ + 1) / 2", "255"},
	{"u32_subtract_wraps", "u32", "@ - 1", "0"},
	{"i32_multiply_wraps", "i32", "@ * 100000", "100000"},
	{"u32_divide_is_unsigned", "u32", "(@ - 2) / 2", "0"},
	{"u32_shift_right_is_logical", "u32", "(@ - 1) >> 1", "0"},
	{"i32_shift_right_is_arithmetic", "i32", "(@ - 8) >> 33", "0"},
	{"i64_shift_count_masks_to_63", "i64", "@ << 64", "1"},
}

func untypedLiteralFoldSource(ty, expr, seed string) string {
	return "const K: " + ty + " = " + strings.ReplaceAll(expr, "@", seed) + ";\n" +
		"function main(): i32 {\n" +
		"    var s: " + ty + " = " + seed + ";\n" +
		"    var r: " + ty + " = " + strings.ReplaceAll(expr, "@", "s") + ";\n" +
		"    if (K == r) { return 0; }\n" +
		"    return 7;\n}\n"
}

// A binding's declared type is the same context without a const: 256 / 2 at
// u8 is 0, and at i32 it is 128.
const untypedLiteralBindingSrc = `function main(): i32 {
    var r: u8 = (255 + 1) / 2;
    var w: u32 = 0 - 1;
    if (w != (4294967295 as u32)) { return 2; }
    return r as i32;
}
`

func untypedLiteralFoldPrograms() map[string]string {
	progs := map[string]string{"binding": untypedLiteralBindingSrc}
	for _, c := range untypedLiteralFoldCases {
		progs[c.name] = untypedLiteralFoldSource(c.ty, c.expr, c.seed)
	}
	return progs
}

func TestSelfHostUntypedLiteralFoldX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	interp := buildLangBinForInterp(t)
	for name, src := range untypedLiteralFoldPrograms() {
		t.Run(name, func(t *testing.T) {
			if got := interpExit(t, interp, src); got != 0 {
				t.Fatalf("interpreter = %d, want 0", got)
			}
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); code != 0 {
				t.Errorf("exit %d, want 0: the folded value differs from the runtime one", code)
			}
		})
	}
}

func TestSelfHostUntypedLiteralFoldArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for name, src := range untypedLiteralFoldPrograms() {
		t.Run(name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src)); code != 0 {
				t.Errorf("arm64: exit %d, want 0: the folded value differs from the runtime one", code)
			}
		})
	}
}

func TestSelfHostUntypedLiteralFoldWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for name, src := range untypedLiteralFoldPrograms() {
		t.Run(name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", src)); code != 0 {
				t.Errorf("wasm: exit %d, want 0: the folded value differs from the runtime one", code)
			}
		})
	}
}
