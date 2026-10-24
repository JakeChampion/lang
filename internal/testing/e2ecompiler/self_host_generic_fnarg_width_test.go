package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const genericFnArgWidthProgram = `import "core/iter" as iter;

function filter[T, I: iter.Iterator[T]](it: I, keep: (T) => boolean): T[] {
    let out: T[] = [];
    let cur = it;
    let go = true;
    while (go) {
        match (cur.next()) {
            Some(t) => {
                if (keep(t.0)) { out = out.append(t.0); }
                cur = t.1;
            },
            None => { go = false; }
        }
    }
    return out;
}

function main(): i32 {
    let xs: f64[] = [5.5, 2.25, 8.5, 1.75, 4.5];
    let big = filter(iter.of(xs), (x: f64): boolean => { return x > 3.0; });
    if (big.len() != 3) { return 99; }
    return (big[0] as i32) + (big[1] as i32) + (big[2] as i32);
}
`

func TestSelfHostGenericFnArgWidthX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(genericFnArgWidthProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	native := buildAndRunCLI(t, buildLangBinForInterp(t), stdlibRoot, src, "native")
	if native == 99 {
		t.Fatalf("the oracle itself filtered wrong (exit 99) — the fixture is broken, not the self-host")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHost := buildAndRunCLI(t, buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), stdlibRoot, src, "selfhost")
	if selfHost != native {
		t.Fatalf("self-host exited %d, native %d — an f64 element read at 4 bytes answers 0 "+
			"for every value while the length stays right (#9485)", selfHost, native)
	}
}

func buildAndRunCLI(t *testing.T, cli, stdlibRoot, src, tag string) int {
	t.Helper()
	out := filepath.Join(t.TempDir(), tag)
	build := exec.Command(cli, "-target", "x86-64-linux", "-o", out, src, stdlibRoot)
	if compiled, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s compile: %v\n%s", tag, err, compiled)
	}
	run := exec.Command(out)
	if _, err := run.CombinedOutput(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		t.Fatalf("%s run: %v", tag, err)
	}
	return 0
}
