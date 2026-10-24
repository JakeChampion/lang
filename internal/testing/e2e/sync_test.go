package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func syncSource(prefix string, wasm bool, chardev bool) string {
	p := func(name string) string {
		if prefix == "" {
			return name
		}
		return filepath.Join(prefix, name)
	}
	syncfsOnFile := `match (w.syncfs()) { Ok(_) => {}, Err(_) => { return 12; } }`
	chardevBlock := `
    match (open_reader("/dev/zero")) {
        Ok(f) => {
            match (f.fsync())     { Ok(_) => { return 31; }, Err(_) => {} }
            match (f.fdatasync()) { Ok(_) => { return 32; }, Err(_) => {} }
            match (f.syncfs())    { Ok(_) => {}, Err(_) => { return 33; } }
            f.close();
        },
        Err(_) => { return 34; }
    }`
	closedWriter := `match (w.fsync())     { Ok(_) => { return 14; }, Err(_) => {} }
            match (w.fdatasync()) { Ok(_) => { return 15; }, Err(_) => {} }`
	closedReader := `match (r.syncfs())    { Ok(_) => { return 22; }, Err(_) => {} }`
	if !chardev {
		chardevBlock = ""
	}
	if wasm {
		syncfsOnFile = `match (w.syncfs()) { Ok(_) => { return 12; }, Err(_) => {} }`
		chardevBlock = ""
		closedWriter = "// a dropped preview-2 descriptor traps rather than answering EBADF"
		closedReader = closedWriter
	}

	return fmt.Sprintf(`function main(): i32 {
    // A Writer flushed through every method, then read back by the Go side.
    match (open_writer(%[1]q)) {
        Ok(w) => {
            match (w.write("durable\n")) { Ok(_) => {}, Err(_) => { return 10; } }
            match (w.fsync())     { Ok(_) => {}, Err(_) => { return 11; } }
            %[3]s
            match (w.fdatasync()) { Ok(_) => {}, Err(_) => { return 13; } }
            w.close();
            %[6]s
        },
        Err(_) => { return 16; }
    }

    // The same three on a Reader: one op each, two method names, so a
    // backend that wired only the Writer half is caught here.
    match (open_reader(%[1]q)) {
        Ok(r) => {
            match (r.fsync())     { Ok(_) => {}, Err(_) => { return 20; } }
            match (r.fdatasync()) { Ok(_) => {}, Err(_) => { return 21; } }
            r.close();
            %[7]s
        },
        Err(_) => { return 23; }
    }
%[4]s

    // The whole-machine flush. It returns nothing and cannot fail, so the
    // only thing to assert is that the program is still running afterwards —
    // a bad syscall number here is a fault, not a bad answer.
    %[2]s
    match (write_file(%[5]q, "after\n")) { Ok(_) => {}, Err(_) => { return 40; } }
    return 0;
}
`, p("durable.txt"), syncCall(wasm), syncfsOnFile, chardevBlock, p("after.txt"),
		closedWriter, closedReader)
}

func syncCall(wasm bool) string {
	if wasm {
		return "// sync() is E066-refused on this target; see TestWASMSyncRefused."
	}
	return "sync();"
}

func syncCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "durable.txt"))
	if err != nil {
		t.Fatalf("read durable.txt: %v", err)
	}
	if string(got) != "durable\n" {
		t.Errorf("durable.txt = %q, want %q", got, "durable\n")
	}
	after, err := os.ReadFile(filepath.Join(dir, "after.txt"))
	if err != nil {
		t.Fatalf("read after.txt: %v — the program did not survive sync()", err)
	}
	if string(after) != "after\n" {
		t.Errorf("after.txt = %q, want %q", after, "after\n")
	}
}

func TestX86_64Sync(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, syncSource(dir, false, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)\n%s", code, out)
	}
	syncCheckTree(t, dir)
}

func TestArm64Sync(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, syncSource(dir, false, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)\n%s", code, out)
	}
	syncCheckTree(t, dir)
}

func TestInterpSync(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, syncSource(dir, false, runtime.GOOS == "linux")); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see syncSource)", code)
	}
	syncCheckTree(t, dir)
}

func TestWASMPreview1Sync(t *testing.T) {
	mod := buildWasmCore(t, syncSource("", true, false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see syncSource)", got)
	}
	syncCheckTree(t, dir)
}

func TestWASMSync(t *testing.T) {
	dir := t.TempDir()
	out := runResultStdout(t, syncSource("", true, false), runOpts{workDir: dir})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see syncSource)\nstdout:\n%s", got, out)
	}
	syncCheckTree(t, dir)
}

func TestWASMSyncRefused(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    sync();
    return 0;
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted sync(); it has no machine to flush", target)
			continue
		}
		if vs[0].Builtin != "sync" || vs[0].Capability != "fssync" {
			t.Errorf("%s refused %q on %q, want sync on fssync", target, vs[0].Builtin, vs[0].Capability)
		}
	}
	for _, target := range []string{"arm64-linux", "arm64-darwin", "x86-64-linux"} {
		if vs := platforms.Enforce(prog, target); len(vs) != 0 {
			t.Errorf("%s refused sync(): %+v", target, vs)
		}
	}
}
