package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Both compilers print `-check` diagnostics in file and position order, not
// the order their passes find them, and underline an unknown name across its
// length (#12677).
func TestCheckRenderMatchesOracle(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct{ name, src string }{
		// The checker finds the E001 on line 3 before the E003 on line 2.
		{"position-order", "function f(): i32 {\n  let y: i32 = \"s\";\n  return undefined_name;\n}\nfunction main(): i32 { return f(); }\n"},
		{"same-line-by-column", "function main(): i32 {\n  let y: i32 = \"s\"; return nope + 1;\n}\n"},
		{"span-at-line-end", "function main(): i32 { return missing_thing\n; }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			self := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
			selfOut, _ := self.CombinedOutput()
			orc := exec.Command(oracle, "-check", path)
			orcOut, _ := orc.CombinedOutput()
			if self.ProcessState.ExitCode() != 1 || orc.ProcessState.ExitCode() != 1 {
				t.Errorf("exits %d (self-host) and %d (oracle), want 1 and 1", self.ProcessState.ExitCode(), orc.ProcessState.ExitCode())
			}
			if string(selfOut) != string(orcOut) {
				t.Errorf("self-host:\n%s\noracle:\n%s", selfOut, orcOut)
			}
		})
	}
}
