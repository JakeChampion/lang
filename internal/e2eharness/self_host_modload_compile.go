// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_modload_compile_test.go.
package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

// BuildModloadDriverX86 builds the file-based, import-driven self-host
// driver (asm_modload_run) as an x86 host binary — the successor to the
// bundle_run `///MODULE`-marker driver. Returns the gcc/runner tooling and
// the driver binary path.
func BuildModloadDriverX86(t *testing.T) (gcc string, runner []string, driverBin string) {
	t.Helper()
	gcc, runner = X86_64Tooling(t)
	dir := WriteSelfHostModloadProject(t)
	// Route through the content-addressed asm cache: dozens of tests build this
	// same ~35k-line driver, and recompiling it per test is both the suite's
	// dominant time cost and a RAM/disk spike that, accumulated across a shard,
	// has been exhausting the GitHub-hosted runner (the mid-run `exit 143`
	// reclaims). cachedSelfHostAsm runs the identical modload.Load → constfold →
	// checker.Check → x86_64.Emit pipeline, keyed by source-set hash, so this is
	// behaviour-identical and only deduplicates the repeated compile.
	driverBin = BuildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")
	return gcc, runner, driverBin
}

// BuildModloadArm64DriverX86 builds the arm64-emitting file-based driver as
// an x86 HOST binary — it runs on x86 and emits aarch64 asm, the file-based
// successor to bundle_run_arm64. Since the #4398 fold this is the SAME merged
// asm_modload_run driver BuildModloadDriverX86 builds (one shared cached
// build); callers select arm64 by passing "-target", "arm64-linux" through their
// compile*/RunDriverFile invocation. Returns the x86 gcc/runner (for
// executing the driver) and the driver binary path. The caller links the
// emitted arm64 asm with the aarch64 cross toolchain.
func BuildModloadArm64DriverX86(t *testing.T) (x86gcc string, x86runner []string, driverBin string) {
	t.Helper()
	x86gcc, x86runner = X86_64Tooling(t)
	dir := WriteSelfHostModloadProject(t)
	return x86gcc, x86runner, BuildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "arm64driver")
}

// CompileStdProgModload compiles `mainSrc` — which imports each named
// stdlib module by `./<mod>` — with the file-based driver, vendoring each
// std/<mod>.fern as a flat <mod>.fern next to a generated main.fern (plus
// the real builtins.fern). This is the successor to feeding bundle_run a
// `///MODULE <mod>` + `///MODULE main` marker bundle: the loader follows
// main's imports to the vendored modules and SKIPS each module's own
// unresolved std/ imports — exactly the set the marker bundle hand-picked.
// Returns the emitted asm and the program dir (for BuildBin).
func CompileStdProgModload(t *testing.T, runner []string, driverBin string, mods []string, mainSrc string, extraArgs ...string) (asm string, progDir string) {
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
	return string(RunDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), extraArgs...)), progDir
}

// CompileFilesModload compiles a program from an explicit set of module
// files (`files` maps a `<name>.fern` filename to its source, and MUST
// include "main.fern" as the entry) with the file-based driver. The
// general counterpart of CompileStdProgModload / CompileSourceModload for
// programs built from specific self-host modules (e.g. lexer + parser).
// Returns the emitted asm and the program dir.
func CompileFilesModload(t *testing.T, runner []string, driverBin string, files map[string]string, extraArgs ...string) (asm string, progDir string) {
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
	return string(RunDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), extraArgs...)), progDir
}

// CompileSourceModload compiles `entrySrc` and its FULL transitive stdlib
// closure (resolved by the real Go modload, exactly like selfHostBundleFor)
// with the file-based driver: each resolved module is written as a flat
// <base>.fern next to main.fern, and the loader's basename fallback
// resolves their std/-qualified imports. The closure counterpart of
// CompileStdProgModload, for programs whose imports pull in more than a
// hand-picked module or two. Returns the emitted asm and the program dir.
func CompileSourceModload(t *testing.T, runner []string, driverBin, entrySrc string, extraArgs ...string) (asm string, progDir string) {
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
	return string(RunDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), extraArgs...)), progDir
}
