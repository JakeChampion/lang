package e2e

import (
	"os"
	"path/filepath"
	"strings"
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
	// Route through the content-addressed asm cache: dozens of tests build this
	// same ~35k-line driver, and recompiling it per test is both the suite's
	// dominant time cost and a RAM/disk spike that, accumulated across a shard,
	// has been exhausting the GitHub-hosted runner (the mid-run `exit 143`
	// reclaims). cachedSelfHostAsm runs the identical modload.Load → constfold →
	// checker.Check → x86_64.Emit pipeline, keyed by source-set hash, so this is
	// behaviour-identical and only deduplicates the repeated compile.
	driverBin = buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")
	return gcc, runner, driverBin
}

// buildModloadArm64DriverX86 builds the arm64-emitting file-based driver
// (asm_arm64_modload_run) as an x86 HOST binary — it runs on x86 and emits
// aarch64 asm, the file-based successor to bundle_run_arm64. Returns the
// x86 gcc/runner (for executing the driver) and the driver binary path.
// The caller links the emitted arm64 asm with the aarch64 cross toolchain.
func buildModloadArm64DriverX86(t *testing.T) (x86gcc string, x86runner []string, driverBin string) {
	t.Helper()
	x86gcc, x86runner = x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	prog, _, err := modload.Load(filepath.Join(dir, "asm_arm64_modload_run.fern"))
	if err != nil {
		t.Fatalf("modload arm64 driver: %v", err)
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
	return x86gcc, x86runner, buildBin(t, x86gcc, dir, "arm64driver", asm)
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

// compileFilesModload compiles a program from an explicit set of module
// files (`files` maps a `<name>.fern` filename to its source, and MUST
// include "main.fern" as the entry) with the file-based driver. The
// general counterpart of compileStdProgModload / compileSourceModload for
// programs built from specific self-host modules (e.g. lexer + parser).
// Returns the emitted asm and the program dir.
func compileFilesModload(t *testing.T, runner []string, driverBin string, files map[string]string) (asm string, progDir string) {
	t.Helper()
	progDir = t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"))), progDir
}

// compileSourceModload compiles `entrySrc` and its FULL transitive stdlib
// closure (resolved by the real Go modload, exactly like selfHostBundleFor)
// with the file-based driver: each resolved module is written as a flat
// <base>.fern next to main.fern, and the loader's basename fallback
// resolves their std/-qualified imports. The closure counterpart of
// compileStdProgModload, for programs whose imports pull in more than a
// hand-picked module or two. Returns the emitted asm and the program dir.
func compileSourceModload(t *testing.T, runner []string, driverBin, entrySrc string) (asm string, progDir string) {
	t.Helper()
	const entryPath = "/__fern_source__/main.fern"
	_, srcs, err := modload.LoadSource(entrySrc)
	if err != nil {
		t.Fatalf("modload.LoadSource: %v", err)
	}
	progDir = t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	seen := map[string]string{}
	for p, src := range srcs {
		if p == entryPath {
			continue
		}
		b := strings.TrimSuffix(filepath.Base(p), ".fern")
		if prev, ok := seen[b]; ok {
			t.Fatalf("module-name collision: %q and %q both map to %q", prev, p, b)
		}
		seen[b] = p
		if err := os.WriteFile(filepath.Join(progDir, b+".fern"), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s.fern: %v", b, err)
		}
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"))), progDir
}
