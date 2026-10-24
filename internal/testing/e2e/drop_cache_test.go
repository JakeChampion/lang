package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func dropCacheSource(prefix string, kernel bool) string {
	path := "cached.txt"
	if prefix != "" {
		path = filepath.Join(prefix, path)
	}
	writerChecks, readerChecks := "", ""
	if kernel {
		writerChecks = `match (w.drop_cache(0i64, 0i64 - 1i64)) { Ok(_) => { return 13; }, Err(_) => {} }`
		readerChecks = `match (r.drop_cache(0i64, 0i64 - 1i64)) { Ok(_) => { return 23; }, Err(_) => {} }
            r.close();
            match (r.drop_cache(0i64, 0i64)) { Ok(_) => { return 24; }, Err(_) => {} }`
	}
	return fmt.Sprintf(`function main(): i32 {
    match (open_writer(%[1]q)) {
        Ok(w) => {
            match (w.write("cached\n")) { Ok(_) => {}, Err(_) => { return 10; } }
            match (w.drop_cache(0i64, 0i64)) { Ok(_) => {}, Err(_) => { return 11; } }
            match (w.drop_cache(0i64, 4i64)) { Ok(_) => {}, Err(_) => { return 12; } }
            %[2]s
            w.close();
        },
        Err(_) => { return 14; }
    }
    match (open_reader(%[1]q)) {
        Ok(r) => {
            match (r.drop_cache(2i64, 0i64)) { Ok(_) => {}, Err(_) => { return 21; } }
            match (r.read_chunk(6)) { Ok(c) => { if (c != "cached") { return 22; } }, Err(_) => { return 25; } }
            %[3]s
        },
        Err(_) => { return 26; }
    }
    return 0;
}
`, path, writerChecks, readerChecks)
}

func dropCacheCheckTree(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "cached.txt"))
	if err != nil {
		t.Fatalf("read cached.txt: %v", err)
	}
	if string(got) != "cached\n" {
		t.Errorf("cached.txt = %q, want %q", got, "cached\n")
	}
}

func TestInterpDropCache(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS != "linux" {
		src := fmt.Sprintf(`function main(): i32 {
    match (open_writer(%q)) {
        Ok(w) => { match (w.drop_cache(0i64, 0i64)) { Err(Unsupported()) => { return 0; }, _ => { return 1; } } },
        Err(_) => { return 2; }
    }
}
`, filepath.Join(dir, "cached.txt"))
		if code := runInterpExit(t, src); code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		return
	}
	if code := runInterpExit(t, dropCacheSource(dir, true)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dropCacheSource)", code)
	}
	dropCacheCheckTree(t, dir)
}

func TestX86_64DropCache(t *testing.T) {
	dir := t.TempDir()
	code, out := compileRunX86_64WithSetup(t, dropCacheSource(dir, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dropCacheSource)\n%s", code, out)
	}
	dropCacheCheckTree(t, dir)
}

func TestArm64DropCache(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, dropCacheSource(dir, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see dropCacheSource)\n%s", code, out)
	}
	dropCacheCheckTree(t, dir)
}

func TestWASMPreview1DropCache(t *testing.T) {
	mod := buildWasmCore(t, dropCacheSource("", false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dropCacheSource)", got)
	}
	dropCacheCheckTree(t, dir)
}

func TestWASMDropCache(t *testing.T) {
	dir := t.TempDir()
	out := runResultStdout(t, dropCacheSource("", false), runOpts{workDir: dir})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dropCacheSource)\nstdout:\n%s", got, out)
	}
	dropCacheCheckTree(t, dir)
}
