package e2e

import (
	"os"
	"testing"
)

func TestAliasGrowNoOverRelease(t *testing.T) {
	srcBytes, err := os.ReadFile(langSrcAbs(t, "tests/probes/alias_grow_uaf.fern"))
	if err != nil {
		t.Fatalf("read probe: %v", err)
	}
	src := string(srcBytes)

	interpBin := buildLangBinForInterp(t)
	if want := interpExit(t, interpBin, src); want != 0 {
		t.Fatalf("interpreter oracle exits %d, want 0 — fixture drift", want)
	}

	t.Run("x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64(t, src)
		if code != 0 {
			t.Errorf("exit %d, want 0: a name was reclaimed under its owner (shared-buffer grow copied its elements without a retain)", code)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		if code != 0 {
			t.Errorf("exit %d, want 0: a name was reclaimed under its owner (shared-buffer grow copied its elements without a retain)", code)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := runWasm(t, src); code != 0 {
			t.Errorf("exit %d, want 0: a name was reclaimed under its owner (shared-buffer grow copied its elements without a retain)", code)
		}
	})
}
