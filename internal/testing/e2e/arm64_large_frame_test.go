package e2e

import (
	"strconv"
	"strings"
	"testing"
)

func arm64LargeFrameSrc() string {
	var b strings.Builder
	b.WriteString("function big(): i32 {\n")
	const n = 800
	for i := 0; i < n; i++ {
		b.WriteString("    let v" + strconv.Itoa(i) + ": i64 = " + strconv.Itoa(i) + "i64;\n")
	}
	b.WriteString("    let total: i64 = 0i64;\n")
	for i := 0; i < n; i++ {
		b.WriteString("    total = total + v" + strconv.Itoa(i) + ";\n")
	}
	b.WriteString("    if (total != 319600i64) { return 1; }\n")
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
