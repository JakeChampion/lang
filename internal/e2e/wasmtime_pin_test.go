package e2e

import (
	"os"
	"regexp"
	"testing"
)

// TestWasmtimePinMatchesCI keeps the Go-side wasmtime pin honest.
//
// The version lives in two places that cannot import each other: the CI
// composite action installs it, and wasmtimePin (wasm_e2e_test.go) is what
// skipIfPreview2Missing compares the local toolchain against. If they drift,
// the skip message tells developers to install a version CI does not use —
// worse than no message, because it looks authoritative.
//
// The pin exists at all because the component-model-async ABI is not stable
// across wasmtime majors; see the comment in skipIfPreview2Missing.
func TestWasmtimePinMatchesCI(t *testing.T) {
	const action = "../../.github/actions/setup-fern/action.yml"
	src, err := os.ReadFile(action)
	if err != nil {
		t.Fatalf("read %s: %v", action, err)
	}
	// The action downloads .../releases/download/v46.0.1/wasmtime-v46.0.1-...
	re := regexp.MustCompile(`wasmtime/releases/download/v([0-9]+\.[0-9]+\.[0-9]+)/`)
	m := re.FindAllStringSubmatch(string(src), -1)
	if len(m) == 0 {
		t.Fatalf("no wasmtime release URL found in %s — has the install step changed shape? "+
			"wasmtimePin must still be checkable against whatever replaced it", action)
	}
	for _, g := range m {
		if g[1] != wasmtimePin {
			t.Errorf("%s installs wasmtime %s but wasmtimePin is %q — update whichever is stale",
				action, g[1], wasmtimePin)
		}
	}
}
