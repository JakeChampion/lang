package e2e

import (
	"os"
	"strings"
	"testing"
)

func simdSuiteSrc(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/simd_test.fern"))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

func wantSimdSuitePassed(t *testing.T, leg, out string) {
	t.Helper()
	for _, w := range []string{"1..23", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("%s: stdout missing %q\n%s", leg, w, out)
		}
	}
}

func TestSimdModuleX86_64(t *testing.T) {
	out, code := compileAndRunX86_64(t, simdSuiteSrc(t))
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, out)
	}
	wantSimdSuitePassed(t, "x86-64", out)
}

func TestSimdModuleWasm(t *testing.T) {
	out := compileAndRunWasmCapture(t, simdSuiteSrc(t))
	wantSimdSuitePassed(t, "wasm", out)
}
