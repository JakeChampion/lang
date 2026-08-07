package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// Arm64 large-frame codegen: a function whose locals exceed 4095 bytes needs
// frame offsets / a stack-allocation immediate past the 12-bit add/sub range.
// frameLoad / frameStore / the prologue's `sub sp, sp, #N` materialise the
// offset / size via movz(+movk) + a register-operand add/sub rather than
// panicking ("multi-step materialisation not implemented"). This builds an
// 800-i64-locals function (a ~6400-byte frame, well past 4095), sums them, and
// checks the total IN PROGRAM (so the verdict survives the 8-bit exit code) —
// exercising both the high-offset frameLoad/Store and the prologue emitSpSub.
func arm64LargeFrameSrc() string {
	var b strings.Builder
	b.WriteString("function big(): i32 {\n")
	const n = 800
	for i := 0; i < n; i++ {
		b.WriteString("    var v" + strconv.Itoa(i) + ": i64 = " + strconv.Itoa(i) + "i64;\n")
	}
	b.WriteString("    var total: i64 = 0i64;\n")
	for i := 0; i < n; i++ {
		b.WriteString("    total = total + v" + strconv.Itoa(i) + ";\n")
	}
	b.WriteString("    if (total != 319600i64) { return 1; }\n") // sum 0..799
	b.WriteString("    return 0;\n}\n")
	b.WriteString("function main(): i32 { return big(); }\n")
	return b.String()
}

func TestArm64LargeFrame(t *testing.T) {
	src := arm64LargeFrameSrc()
	if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
		t.Errorf("arm64 large-frame free-on: code=%d (1=wrong sum => bad frame offset)", code)
	}
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("arm64 large-frame free-off: code=%d", code)
	}
}
