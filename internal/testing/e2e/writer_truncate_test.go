package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func writerTruncateSource(dir string, perms bool, closed bool) string {
	at := func(name string) string {
		if dir == "" {
			return `"` + name + `"`
		}
		return `"` + filepath.Join(dir, name) + `"`
	}
	locked := ""
	if perms {
		locked = `    match (open_exclusive(` + at("locked.txt") + `)) {
        Ok(w) => {
            match (w.write("0123456789")) { Err(e) => { return 9; }, Ok(_) => {} }
            match (chmod(` + at("locked.txt") + `, 292)) { Ok(_) => { }, Err(e) => { return 10; } }
            match (w.truncate(3 as i64)) { Err(e) => { return 11; }, Ok(_) => {} }
            w.close();
        },
        Err(e) => { return 12; }
    }
`
	}
	shut := ""
	if closed {
		shut = `    match (open_writer(` + at("closed.txt") + `)) {
        Ok(w) => {
            match (w.write("abc")) { Err(e) => { return 13; }, Ok(_) => {} }
            w.close();
            match (w.truncate(0 as i64)) { Err(e) => { }, Ok(_) => { return 14; } }
        },
        Err(e) => { return 15; }
    }
`
	}
	return `function main(): i32 {
    match (open_writer(` + at("grow.txt") + `)) {
        Ok(w) => {
            match (w.write("hello world")) { Err(e) => { return 2; }, Ok(_) => {} }
            match (w.truncate(5 as i64)) { Err(e) => { return 3; }, Ok(_) => {} }
            match (w.truncate(9 as i64)) { Err(e) => { return 4; }, Ok(_) => {} }
            match (w.close()) { Err(e) => { return 5; }, Ok(_) => {} }
        },
        Err(e) => { return 6; }
    }
    match (open_writer(` + at("neg.txt") + `)) {
        Ok(w) => {
            match (w.truncate((0 as i64) - (1 as i64))) {
                Err(e) => { },
                Ok(_) => { return 7; }
            }
            w.close();
        },
        Err(e) => { return 8; }
    }
` + locked + shut + `    return 0;
}
`
}

func writerTruncateCheckTree(t *testing.T, dir string, perms bool, closed bool) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "grow.txt"))
	if err != nil {
		t.Fatalf("read grow.txt: %v", err)
	}
	if want := "hello\x00\x00\x00\x00"; string(got) != want {
		t.Errorf("grow.txt = %q, want %q — the shrink kept the tail or the grow did not hole", got, want)
	}
	fi, err := os.Stat(filepath.Join(dir, "neg.txt"))
	if err != nil {
		t.Fatalf("stat neg.txt: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("neg.txt is %d bytes — a refused resize changed the file", fi.Size())
	}
	if closed {
		shut, err := os.ReadFile(filepath.Join(dir, "closed.txt"))
		if err != nil {
			t.Fatalf("read closed.txt: %v", err)
		}
		if string(shut) != "abc" {
			t.Errorf("closed.txt = %q, want %q — the resize on a closed handle went through", shut, "abc")
		}
	}
	if !perms {
		return
	}
	locked, err := os.ReadFile(filepath.Join(dir, "locked.txt"))
	if err != nil {
		t.Fatalf("read locked.txt: %v", err)
	}
	if string(locked) != "012" {
		t.Errorf("locked.txt = %q, want %q — the resize did not go through the descriptor", locked, "012")
	}
}

func TestX86_64WriterTruncate(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, writerTruncateSource(dir, true, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)", code)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

func TestArm64WriterTruncate(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, writerTruncateSource(dir, true, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)\n%s", code, out)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

func TestInterpWriterTruncate(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, writerTruncateSource(dir, true, true)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see writerTruncateSource)", code)
	}
	writerTruncateCheckTree(t, dir, true, true)
}

func TestWASMPreview1WriterTruncate(t *testing.T) {
	mod := buildWasmCore(t, writerTruncateSource("", false, false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see writerTruncateSource)", got)
	}
	writerTruncateCheckTree(t, dir, false, false)
}

func TestWASMWriterTruncate(t *testing.T) {
	dir := t.TempDir()
	out := runResultStdout(t, writerTruncateSource("", false, false), runOpts{workDir: dir})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see writerTruncateSource)\nstdout:\n%s", got, out)
	}
	writerTruncateCheckTree(t, dir, false, false)
}
