package e2eharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func BuildModloadDriverX86(t *testing.T) (gcc string, runner []string, driverBin string) {
	t.Helper()
	gcc, runner = X86_64Tooling(t)
	dir := WriteSelfHostModloadProject(t)
	driverBin = BuildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "driver")
	return gcc, runner, driverBin
}

func BuildModloadArm64DriverX86(t *testing.T) (x86gcc string, x86runner []string, driverBin string) {
	t.Helper()
	x86gcc, x86runner = X86_64Tooling(t)
	dir := WriteSelfHostModloadProject(t)
	return x86gcc, x86runner, BuildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "arm64driver")
}

func CompileFilesModload(t *testing.T, runner []string, driverBin string, files map[string]string, extraArgs ...string) (asm string, progDir string) {
	t.Helper()
	progDir = t.TempDir()
	bsrc, err := os.ReadFile(RepoPath("compiler", "builtins.fern"))
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
	fillSelfHostImports(t, progDir, files)
	return string(RunDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), extraArgs...)), progDir
}

func fillSelfHostImports(t *testing.T, progDir string, files map[string]string) {
	t.Helper()
	have := map[string]bool{"builtins.fern": true}
	stdlib := false
	var queue []string
	for name, src := range files {
		have[name] = true
		queue = append(queue, src)
	}
	for len(queue) > 0 {
		src := queue[0]
		queue = queue[1:]
		for _, m := range fernImportRe.FindAllStringSubmatch(src, -1) {
			if isExternalFernImport(m[1]) {
				stdlib = stdlib || strings.HasPrefix(m[1], "core/") || strings.HasPrefix(m[1], "std/")
				continue
			}
			name := strings.TrimPrefix(m[1], "./") + ".fern"
			if have[name] {
				continue
			}
			sh, err := os.ReadFile(filepath.Join(selfHostSrcDir(), name))
			if err != nil {
				continue
			}
			have[name] = true
			if err := os.WriteFile(filepath.Join(progDir, name), sh, 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			queue = append(queue, string(sh))
		}
	}
	if stdlib {
		CopyStdlibTree(t, progDir)
	}
}

func CompileSourceModload(t *testing.T, runner []string, driverBin, entrySrc string, extraArgs ...string) (asm string, progDir string) {
	t.Helper()
	progDir = WriteSourceModloadProject(t, entrySrc)
	return string(RunDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), extraArgs...)), progDir
}

func WriteSourceModloadProject(t *testing.T, entrySrc string) (progDir string) {
	t.Helper()
	_, srcs, err := modload.LoadSource(entrySrc)
	if err != nil {
		t.Fatalf("modload.LoadSource: %v", err)
	}
	progDir = t.TempDir()
	bsrc, err := os.ReadFile(RepoPath("compiler", "builtins.fern"))
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	seen := map[string]string{}
	for p, src := range srcs {
		if p == modload.SourceEntry {
			continue
		}
		b := strings.TrimSuffix(filepath.Base(p), ".fern")
		if prev, ok := seen[b]; ok {
			t.Fatalf("module-name collision: %q and %q both map to %q", prev, p, b)
		}
		seen[b] = p
		name := b + ".fern"
		if strings.HasPrefix(p, "stdlib://") {
			name = strings.TrimPrefix(p, "stdlib://")
		}
		path := filepath.Join(progDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return progDir
}
