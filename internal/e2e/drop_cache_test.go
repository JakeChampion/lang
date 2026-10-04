// `drop_cache(offset, len)` end to end on every backend: posix_fadvise(2)
// with POSIX_FADV_DONTNEED on both handle types (#11248).
//
// A dropped page is not observable from userspace, so the probe pins the
// call to answers only the right syscall gives. A negative length is EINVAL
// from fadvise64, where a call that ignored its arguments (fsync, say)
// answers None, and a closed handle is EBADF, which a call that never
// reached the kernel cannot produce. Neither WASI preview can be asked
// those: the length is unsigned there, and a dropped preview-2 descriptor
// traps rather than answering. So the wasm legs assert the success path
// only, which is what a host that ignores the advice answers too.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// dropCacheSource is the probe. `prefix` is the directory its file lives in,
// "" for wasm, whose cwd is its preopen. `kernel` adds the EINVAL and EBADF
// cases a Linux kernel answers. Every failure returns its own exit code.
func dropCacheSource(prefix string, kernel bool) string {
	path := "cached.txt"
	if prefix != "" {
		path = filepath.Join(prefix, path)
	}
	writerChecks, readerChecks := "", ""
	if kernel {
		writerChecks = `match (w.drop_cache(0i64, 0i64 - 1i64)) { None => { return 13; }, Some(_) => {} }`
		readerChecks = `match (r.drop_cache(0i64, 0i64 - 1i64)) { None => { return 23; }, Some(_) => {} }
            r.close();
            match (r.drop_cache(0i64, 0i64)) { None => { return 24; }, Some(_) => {} }`
	}
	return fmt.Sprintf(`function main(): i32 {
    match (open_writer(%[1]q)) {
        Ok(w) => {
            match (w.write("cached\n")) { None => {}, Some(_) => { return 10; } }
            match (w.drop_cache(0i64, 0i64)) { None => {}, Some(_) => { return 11; } }
            match (w.drop_cache(0i64, 4i64)) { None => {}, Some(_) => { return 12; } }
            %[2]s
            w.close();
        },
        Err(_) => { return 14; }
    }
    match (open_reader(%[1]q)) {
        Ok(r) => {
            match (r.drop_cache(2i64, 0i64)) { None => {}, Some(_) => { return 21; } }
            match (r.read_chunk(6)) { Ok(c) => { if (c != "cached") { return 22; } }, Err(_) => { return 25; } }
            %[3]s
        },
        Err(_) => { return 26; }
    }
    return 0;
}
`, path, writerChecks, readerChecks)
}

// dropCacheCheckTree reads the file back through Go: advice must not change
// a byte of it.
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
		// XNU has no fadvise: every call answers Unsupported there.
		src := fmt.Sprintf(`function main(): i32 {
    match (open_writer(%q)) {
        Ok(w) => { match (w.drop_cache(0i64, 0i64)) { Some(Unsupported) => { return 0; }, _ => { return 1; } } },
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

// Preview 1's fd_advise and preview 2's descriptor.advise are two bodies.
func TestWASMPreview1DropCache(t *testing.T) {
	mod := buildPreview1Module(t, dropCacheSource("", false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dropCacheSource)", got)
	}
	dropCacheCheckTree(t, dir)
}

func TestWASMDropCache(t *testing.T) {
	p := buildComponent(t, dropCacheSource("", false))
	dir := t.TempDir()
	stdout, stderr, ec := runComponent(t, p, runOpts{workDir: dir})
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	if got := parseMainResult(t, stdout); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see dropCacheSource)\nstdout:\n%s\nstderr:\n%s",
			got, stdout, stderr)
	}
	dropCacheCheckTree(t, dir)
}
