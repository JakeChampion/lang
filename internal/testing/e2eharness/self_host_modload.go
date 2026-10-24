package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/stdlib"
)

func WriteSelfHostModloadProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	CopySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "drivers/irverifyprovided.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "flatten.fern", "modloader.fern", "fern_toml.fern", "builtins.fern", "drivers/asm_modload_run.fern", "treeshake.fern", "drivers/rundriver.fern")
	return dir
}

func WriteSelfHostModloadProjectTyped(t *testing.T) string {
	t.Helper()
	dir := WriteSelfHostModloadProject(t)
	CopyStdlibTree(t, dir)
	return dir
}

func CopyStdlibTree(t *testing.T, dst string) {
	t.Helper()
	src := SelfHostStdlibRoot(t)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if stdlib.ModuleName(path) == "" {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy stdlib tree: %v", err)
	}
}

func RunDriverFile(t *testing.T, runner []string, bin, entry string, extraArgs ...string) []byte {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin, append([]string{entry}, extraArgs...)...)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("run driver on %s: %v\n%s", entry, err, stderr)
	}
	return out
}
