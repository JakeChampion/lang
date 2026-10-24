package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var guardedDrivers = []string{
	"drivers/asm_ir_run.fern",
	"drivers/wasm_ir_run.fern",
}

func TestSelfHostDriversRunTheRcUnderflowGuard(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	for _, name := range guardedDrivers {
		path := filepath.Join(root, "compiler", name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(src), "util.rc_underflow_guard(") {
			t.Errorf("%s: `main` must return util.rc_underflow_guard(<driver>, run()) — "+
				"without it a driver that over-releases its own heap reports success (#6021)", name)
		}
	}

	utilSrc, err := os.ReadFile(filepath.Join(root, "compiler", "util.fern"))
	if err != nil {
		t.Fatalf("read util.fern: %v", err)
	}
	if !strings.Contains(string(utilSrc), "__rc_underflow_count()") {
		t.Error("util.rc_underflow_guard must read __rc_underflow_count() — the detector it reports is the point")
	}
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
