package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func dupOntoSource(dir string, wasm bool) string {
	p := func(name string) string {
		if dir == "" {
			return name
		}
		return filepath.Join(dir, name)
	}

	readerArm := `match (r.dup_onto(7))    { Ok(_) => {}, Err(_) => { return 11; } }
            match (r.dup_onto(0 - 1)) { Ok(_) => { return 12; }, Err(_) => {} }`
	closedArm := `match (r.dup_onto(7))    { Ok(_) => { return 13; }, Err(_) => {} }`
	writerArm := `match (w.dup_onto(1))    { Ok(_) => {}, Err(_) => { return 21; } }
            print("redirected");`
	if wasm {
		readerArm = `match (r.dup_onto(7))    { Ok(_) => { return 11; }, Err(_) => {} }
            match (r.dup_onto(0 - 1)) { Ok(_) => { return 12; }, Err(_) => {} }`
		closedArm = "// a dropped preview-2 descriptor traps rather than answering EBADF"
		writerArm = `match (w.dup_onto(1))    { Ok(_) => { return 21; }, Err(_) => {} }`
	}

	return fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "hello\n")) { Ok(_) => {}, Err(_) => { return 10; } }

    // The Reader half: the same op reached through the other method name, so
    // a backend that wired only the Writer is caught here.
    match (open_reader(%[1]q)) {
        Ok(r) => {
            %[2]s
            r.close();
            %[3]s
        },
        Err(_) => { return 14; }
    }

    // The Writer half, and the one assertion about the descriptor TABLE
    // rather than about an errno.
    match (open_writer(%[4]q)) {
        Ok(w) => {
            %[5]s
            w.close();
        },
        Err(_) => { return 22; }
    }
    return 0;
}
`, p("data.txt"), readerArm, closedArm, p("out.txt"), writerArm)
}

func dupOntoCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read out.txt: %v", err)
	}
	if string(got) != "redirected\n" {
		t.Errorf("out.txt = %q, want %q — fd 1 was not replaced", got, "redirected\n")
	}
}

func TestX86_64DupOnto(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, dupOntoSource(dir, false), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)\n%s", code, out)
	}
	dupOntoCheckTree(t, dir)
}

func TestArm64DupOnto(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, dupOntoSource(dir, false))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)\n%s", code, out)
	}
	dupOntoCheckTree(t, dir)
}

func TestInterpDupOnto(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, dupOntoSource(dir, false)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dupOntoSource)", code)
	}
	dupOntoCheckTree(t, dir)
}

func TestWASMPreview1DupOntoUnsupported(t *testing.T) {
	mod := buildWasmCore(t, dupOntoSource("", true))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dupOntoSource)", got)
	}
}

func TestWASMDupOntoUnsupported(t *testing.T) {
	out := runResultStdout(t, dupOntoSource("", true), runOpts{workDir: t.TempDir()})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dupOntoSource)\nstdout:\n%s", got, out)
	}
}

func TestWASMDupOntoNotCapabilityGated(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    match (open_writer("x")) {
        Ok(w) => { match (w.dup_onto(1)) { Ok(_) => {}, Err(_) => {} } },
        Err(_) => {}
    }
    return 0;
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
}
