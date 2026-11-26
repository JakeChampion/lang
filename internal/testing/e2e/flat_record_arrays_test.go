package e2e

import (
	"os"
	"testing"
)

// An array of a record of integer fields keeps the fields of each element in
// its own buffer. The probe compares each array operation with the same work
// on columns of i32, and its exit code names the first case that differs.
func TestFlatRecordArrays(t *testing.T) {
	srcBytes, err := os.ReadFile(langSrcAbs(t, "tests/probes/flat_record_arrays.fern"))
	if err != nil {
		t.Fatalf("read probe: %v", err)
	}
	src := string(srcBytes)

	interpBin := buildLangBinForInterp(t)
	if want := interpExit(t, interpBin, src); want != 0 {
		t.Fatalf("interpreter oracle exits %d, want 0: fixture drift", want)
	}

	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, src); code != 0 {
			t.Errorf("exit %d, want 0: case %d of the probe differs from its columns", code, code)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, src); code != 0 {
			t.Errorf("exit %d, want 0: case %d of the probe differs from its columns", code, code)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := runWasm(t, src); code != 0 {
			t.Errorf("exit %d, want 0: case %d of the probe differs from its columns", code, code)
		}
	})
}
