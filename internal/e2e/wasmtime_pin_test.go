package e2e

import (
	"os"
	"regexp"
	"testing"
)

// TestWasmtimePinMatchesCI keeps the Go-side wasmtime pin honest.
//
// The version lives in two places that cannot import each other: mise.toml is
// what CI (jdx/mise-action), scripts/devbox and the session hook install, and
// wasmtimePin (wasm_e2e_test.go) is what skipIfPreview2Missing compares the
// local toolchain against. If they drift, the skip message tells developers to
// install a version CI does not use — worse than no message, because it looks
// authoritative.
//
// The pin exists at all because the component-model-async ABI is not stable
// across wasmtime majors; see the comment in skipIfPreview2Missing.
func TestWasmtimePinMatchesCI(t *testing.T) {
	const manifest = "../../mise.toml"
	src, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read %s: %v", manifest, err)
	}
	re := regexp.MustCompile(`(?m)^wasmtime = "([0-9]+\.[0-9]+\.[0-9]+)"`)
	m := re.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatalf("no `wasmtime = \"...\"` pin found in %s — has the toolchain manifest changed shape? "+
			"wasmtimePin must still be checkable against whatever replaced it", manifest)
	}
	if m[1] != wasmtimePin {
		t.Errorf("%s pins wasmtime %s but wasmtimePin is %q — update whichever is stale",
			manifest, m[1], wasmtimePin)
	}
}
