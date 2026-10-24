package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostUnionVariantFieldIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	lib := `pub struct Row { cells: string[] }
pub struct Blank {  }
pub type Line = Row | Blank;

pub function mk_row(cells: string[]): Line { return Row { cells: cells }; }
pub function mk_blank(): Line { return Blank {  }; }
`
	if err := os.WriteFile(filepath.Join(dir, "uvrows.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}

	entrySrc := `import "./uvrows";

function count(l: uvrows.Line): i32 {
    match (l) {
        uvrows.Row(r) => { return r.cells.len(); },
        uvrows.Blank(_) => { return 0; },
    }
    return -1;
}

function main(): i32 {
    let a: uvrows.Line = uvrows.mk_row(["x", "y", "z"]);
    let b: uvrows.Line = uvrows.mk_blank();
    return count(a) * 10 + count(b);
}
`
	entry := filepath.Join(dir, "uvmain.fern")
	if err := os.WriteFile(entry, []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	_, want := runFixtureInterp(t, entry, "")
	if want != 30 {
		t.Fatalf("native oracle = %d, want 30 (sanity: count(a)=3 cells, count(b)=0)", want)
	}

	if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
		t.Errorf("decide = %q, want \"ir\" (qualified imported-union match still bails)", strings.TrimSpace(out))
	}

	asm, _ := runDriver(entry, root)
	if len(asm) == 0 {
		t.Fatal("driver emitted 0 bytes")
	}
	if strings.Contains(asm, "__fn_i32__len") {
		t.Error("emitted asm references undefined __fn_i32__len (payload string[] field mis-dispatched as i32)")
	}
	bin := buildBin(t, gcc, dir, "uvmain_bin", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("self-host run = %d, want %d (native oracle)", code, want)
	}
}
