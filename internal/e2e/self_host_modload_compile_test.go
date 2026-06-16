package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// buildModloadDriverX86 builds the file-based, import-driven self-host
// driver (asm_modload_run) as an x86 host binary — the successor to the
// bundle_run `///MODULE`-marker driver. Returns the gcc/runner tooling and
// the driver binary path.
func buildModloadDriverX86(t *testing.T) (gcc string, runner []string, driverBin string) {
	t.Helper()
	gcc, runner = x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "asm_modload_run.fern"))
	if err != nil {
		t.Fatalf("modload driver: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin = buildBin(t, gcc, dir, "driver", asm)
	return gcc, runner, driverBin
}

// compileStdProgModload compiles `mainSrc` — which imports each named
// stdlib module by `./<mod>` — with the file-based driver, vendoring each
// std/<mod>.fern as a flat <mod>.fern next to a generated main.fern (plus
// the real builtins.fern). This is the successor to feeding bundle_run a
// `///MODULE <mod>` + `///MODULE main` marker bundle: the loader follows
// main's imports to the vendored modules and SKIPS each module's own
// unresolved std/ imports — exactly the set the marker bundle hand-picked.
// Returns the emitted asm and the program dir (for buildBin).
func compileStdProgModload(t *testing.T, runner []string, driverBin string, mods []string, mainSrc string) (asm string, progDir string) {
	t.Helper()
	progDir = t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	for _, m := range mods {
		src, err := os.ReadFile(filepath.Join("../../internal/stdlib/std", m+".fern"))
		if err != nil {
			t.Fatalf("read std/%s.fern: %v", m, err)
		}
		if err := os.WriteFile(filepath.Join(progDir, m+".fern"), src, 0o644); err != nil {
			t.Fatalf("write %s.fern: %v", m, err)
		}
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"))), progDir
}
