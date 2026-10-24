package e2e

import (
	"os"
	"regexp"
	"testing"
)

func TestWasmtimePinMatchesCI(t *testing.T) {
	const manifest = "../../../mise.toml"
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
