package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

// A method that shares its name with a `@norc` function keeps its own
// attributes: the formatter finds a mark's function by position, not by name.
const fmtMarkPositionSrc = `struct Frames {
  base: usize,
}

@norc
function count(base: usize): usize {
  return base;
}

@provide("log")
function log(p: usize, n: i32): void {}

pub function (f: Frames) count(): usize {
  return count(f.base);
}

pub function (f: Frames) log(): usize {
  return f.base;
}
`

func TestSelfHostFmtKeepsMarksOnTheirFunction(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	prog, err := goparser.Parse(fmtMarkPositionSrc)
	if err != nil {
		t.Fatal(err)
	}
	if want := goprinter.Format(prog); want != fmtMarkPositionSrc {
		t.Fatalf("the Go printer rewrites the source:\n%s", want)
	}
	path := filepath.Join(dir, "mark_position.fern")
	if err := os.WriteFile(path, []byte(fmtMarkPositionSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(fernBin, "-fmt", path).Output()
	if err != nil {
		t.Fatalf("self-host -fmt: %v", err)
	}
	if got := string(out); got != fmtMarkPositionSrc {
		t.Errorf("the methods took the marks of the functions with their names:\n--- in ---\n%s\n--- out ---\n%s", fmtMarkPositionSrc, got)
	}
}
