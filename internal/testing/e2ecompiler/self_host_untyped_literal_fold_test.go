package e2ecompiler

import (
	"strings"
	"testing"
)

var untypedLiteralFoldCases = []struct {
	name, ty, expr, seed, want string
}{
	{"i32_add_overflow_then_halve", "i32", "(@ + 1) / 2", "2147483647", "-1073741824"},
	{"i32_shift_count_over_width", "i32", "(@ << 33) & 255", "1", "2"},
	{"u8_add_wraps_at_8_bits", "u8", "(@ + 1) / 2", "255", "0"},
	{"u32_subtract_wraps", "u32", "@ - 1", "0", "4294967295"},
	{"i32_multiply_wraps", "i32", "@ * 100000", "100000", "1410065408"},
	{"u32_divide_is_unsigned", "u32", "(@ - 2) / 2", "0", "2147483647"},
	{"u32_shift_right_is_logical", "u32", "(@ - 1) >> 1", "0", "2147483647"},
	{"i32_shift_right_is_arithmetic", "i32", "(@ - 8) >> 33", "0", "-4"},
	{"i64_shift_count_masks_to_63", "i64", "@ << 64", "1", "1"},
}

// A constant declaration refuses these steps with E047
// (self_host_const_evaluation_test.go), so the folded side is a let.
func untypedLiteralFoldSource(ty, expr, seed, want string) string {
	return "@noinline function runtime(s: " + ty + "): " + ty + " { return " + strings.ReplaceAll(expr, "@", "s") + "; }\n" +
		"function main(): i32 {\n" +
		"    let folded: " + ty + " = " + strings.ReplaceAll(expr, "@", seed) + ";\n" +
		"    let r: " + ty + " = runtime(" + seed + ");\n" +
		"    let want: " + ty + " = " + want + ";\n" +
		"    if (folded == want && r == want) { return 0; }\n" +
		"    return 7;\n}\n"
}

const untypedLiteralBindingSrc = `function main(): i32 {
    let r: u8 = (255 + 1) / 2;
    let w: u32 = 0 - 1;
    if (w != (4294967295 as u32)) { return 2; }
    return r as i32;
}
`

func untypedLiteralFoldPrograms() map[string]string {
	progs := map[string]string{"binding": untypedLiteralBindingSrc}
	for _, c := range untypedLiteralFoldCases {
		progs[c.name] = untypedLiteralFoldSource(c.ty, c.expr, c.seed, c.want)
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
