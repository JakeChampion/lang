package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostImportedParseErrorX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	const lib = "pub function seven(): i32 {\n  return 7;\n}\n"
	const prog = "import \"./lib\";\n\nfunction main(): i32 {\n  return lib.seven();\n}\n"
	write := func(t *testing.T, libSrc string) string {
		progDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(progDir, "lib.fern"), []byte(libSrc), 0o644); err != nil {
			t.Fatalf("write lib: %v", err)
		}
		entry := filepath.Join(progDir, "main.fern")
		if err := os.WriteFile(entry, []byte(prog), 0o644); err != nil {
			t.Fatalf("write main: %v", err)
		}
		return entry
	}
	modes := map[string][]string{
		"build":  {"-target", "x86-64-linux", "-o", "BIN"},
		"check":  {"-check"},
		"interp": {"-interp"},
	}
	for name, flags := range modes {
		t.Run(name, func(t *testing.T) {
			entry := write(t, lib+"this is not fern\n")
			args := append([]string{}, flags...)
			for i, a := range args {
				if a == "BIN" {
					args[i] = filepath.Join(filepath.Dir(entry), "main.bin")
				}
			}
			args = append(args, entry, stdlibRoot)
			cmd := exec.Command(fernBin, args...)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code == 0 {
				t.Fatalf("a module that does not parse was accepted (exit 0):\n%s", out)
			}
			if !strings.Contains(string(out), "lib.fern:4:") || !strings.Contains(string(out), "error[P001]") {
				t.Errorf("stderr does not name the imported module's parse error:\n%s", out)
			}
		})
	}
	t.Run("clean", func(t *testing.T) {
		entry := write(t, lib)
		cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", filepath.Join(filepath.Dir(entry), "main.bin"), entry, stdlibRoot)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("a clean library was refused: %v\n%s", err, out)
		}
	})
}
