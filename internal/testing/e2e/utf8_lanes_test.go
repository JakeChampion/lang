package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The 16-byte steps of std/utf8 against its scalar reference, on the two
// lane backends this package runs. The self-host stdlib suite runs the same
// file on x86-64, arm64 and the interpreter.
func utf8LanesSrc(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/utf8_lanes_test.fern"))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

func wantUtf8LanesPassed(t *testing.T, leg, out string) {
	t.Helper()
	e2eharness.CheckTAPPlan(t, out)
	if !strings.Contains(out, "# pass 3") || !strings.Contains(out, "# fail 0") {
		t.Errorf("%s: want 3 passes and no failure\n%s", leg, out)
	}
}

func TestUtf8LanesX86_64(t *testing.T) {
	out, code := compileAndRunX86_64(t, utf8LanesSrc(t))
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, out)
	}
	wantUtf8LanesPassed(t, "x86-64", out)
}

func TestUtf8LanesWasm(t *testing.T) {
	wantUtf8LanesPassed(t, "wasm", compileAndRunWasmCapture(t, utf8LanesSrc(t)))
}
