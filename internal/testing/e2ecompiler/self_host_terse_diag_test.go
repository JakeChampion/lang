package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostTerseDiagnostics(t *testing.T) {
	t.Setenv("FERN_DIAG_FORMAT", "")
	s := buildLSPServers(t)
	dir := t.TempDir()
	name := "bad\nname.fern"
	src := "function main(): i32 {\n  let count = 1;\n  let count = 2;\n  return kount;\n}\n"
	writeTree(t, dir, map[string]string{name: src})
	path := filepath.Join(dir, name)
	for _, compiler := range []struct {
		name, bin string
		stdlib    bool
	}{
		{"selfhost", s.driver, true},
		{"go-oracle", e2eharness.BuildLangBinForInterp(t), false},
	} {
		for _, mode := range []string{"flag", "environment"} {
			t.Run(compiler.name+"/"+mode, func(t *testing.T) {
				args := []string{"-color", "always", "-check", path}
				if compiler.stdlib {
					args = append(args, s.stdlib)
				}
				if mode == "flag" {
					args = append([]string{"-q"}, args...)
				}
				cmd := exec.Command(compiler.bin, args...)
				if mode == "environment" {
					cmd.Env = append(os.Environ(), "FERN_DIAG_FORMAT=terse")
				}
				out, accepted := checkVerdict(cmd)
				lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
				if accepted || len(lines) != 2 || !strings.Contains(out, "error[E013]") || !strings.Contains(out, "error[E001]") || !strings.Contains(out, "bad\\nname.fern:") || strings.Contains(out, "\x1b[") || strings.Contains(out, "note:") || strings.Contains(out, "help:") || strings.Contains(out, "let count") {
					t.Fatalf("expected two plain diagnostic headers, got:\n%s", out)
				}
			})
		}
		t.Run(compiler.name+"/normal", func(t *testing.T) {
			args := []string{"-color", "never", "-check", path}
			if compiler.stdlib {
				args = append(args, s.stdlib)
			}
			out, accepted := checkVerdict(exec.Command(compiler.bin, args...))
			if accepted || !strings.Contains(out, "note:") || !strings.Contains(out, "help:") || !strings.Contains(out, "let count") {
				t.Fatalf("normal diagnostics lost context:\n%s", out)
			}
		})
	}
}
