package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FERN_DEBUG_SSA_ALLOC=name prints the register form and the homes of the values
// of that function on each native target, and nothing for another name.
func TestSelfHostSSAAllocDump(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "dump.fern")
	prog := "@noinline function twice(x: i32): i32 {\n    return x * 2;\n}\n\nfunction main(): i32 {\n    return twice(4) - 8;\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		for name, want := range map[string]bool{"twice": true, "absent": false, "": false} {
			cmd := exec.Command(h.cli, "-target", tg.target, "-o", filepath.Join(dir, "dump-"+tg.target), src, h.stdlib)
			cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC=twice", "FERN_DEBUG_SSA_ALLOC="+name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", tg.target, err, out)
			}
			got := strings.Contains(string(out), "FERN_SSA_ALLOC: twice\n") && strings.Contains(string(out), " reg ")
			if got != want {
				t.Errorf("%s: FERN_DEBUG_SSA_ALLOC=%s printed the dump of twice: %v, want %v\n%s", tg.target, name, got, want, out)
			}
		}
	}
}
