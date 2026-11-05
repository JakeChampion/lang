package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func writerSetTimesSource(dir string, closed bool) string {
	file, moved := "original", "moved"
	if dir != "" {
		file, moved = filepath.Join(dir, file), filepath.Join(dir, moved)
	}
	shut := ""
	if closed {
		shut = `unsafe { w.close_unchecked(); }
        match (w.set_times(1, 0, 1, 0, 0)) { Ok(_) => { return 20; }, Err(_) => {} }`
	} else {
		shut = `w.close();`
	}
	return fmt.Sprintf(`function main(): i32 {
    match (open_writer(%[1]q)) {
      Err(_) => { return 1; },
      Ok(w) => {
        match (w.write("kept")) { Err(_) => { return 2; }, Ok(_) => {} }
        match (rename(%[1]q, %[2]q)) { Err(_) => { return 3; }, Ok(_) => {} }
        match (write_file(%[1]q, "new")) { Err(_) => { return 4; }, Ok(_) => {} }
        match (set_file_times(%[1]q, 1600000000, 11, 1600000001, 22, 0)) { Err(_) => { return 5; }, Ok(_) => {} }
        match (w.set_times(1650000000, 123456789, 1650000001, 987654321, 0)) { Err(_) => { return 6; }, Ok(_) => {} }
        match (w.stat()) {
          Err(_) => { return 7; },
          Ok(s) => {
            if (s.atime != 1650000000i64 || s.atime_nsec != 123456789i64 || s.mtime != 1650000001i64 || s.mtime_nsec != 987654321i64) { return 8; }
          }
        }
        match (w.set_times(1, 2, 1700000000, 333444555, 2)) { Err(_) => { return 9; }, Ok(_) => {} }
        match (w.set_times(1750000000, 555666777, 1, 2, 4)) { Err(_) => { return 10; }, Ok(_) => {} }
        match (w.set_times(1, 2, 1, 2, 6)) { Err(_) => { return 11; }, Ok(_) => {} }
        match (w.stat()) {
          Err(_) => { return 12; },
          Ok(s) => {
            if (s.atime != 1750000000i64 || s.atime_nsec != 555666777i64 || s.mtime != 1700000000i64 || s.mtime_nsec != 333444555i64) { return 13; }
          }
        }
        match (stat(%[1]q)) {
          Err(_) => { return 14; },
          Ok(s) => {
            if (s.atime != 1600000000i64 || s.atime_nsec != 11i64 || s.mtime != 1600000001i64 || s.mtime_nsec != 22i64) { return 15; }
          }
        }
        let before: i64 = now_ns();
        match (w.set_times(1, 2, 1, 2, 24)) { Err(_) => { return 16; }, Ok(_) => {} }
        let after: i64 = now_ns();
        match (w.stat()) {
          Err(_) => { return 17; },
          Ok(s) => {
            let a: i64 = s.atime * 1000000000i64 + s.atime_nsec;
            let m: i64 = s.mtime * 1000000000i64 + s.mtime_nsec;
            if (a < before || a > after || m < before || m > after) { return 18; }
          }
        }
        %[3]s
      }
    }
    return 0;
}
`, file, moved, shut)
}

func checkWriterSetTimesContents(t *testing.T, dir string) {
	t.Helper()
	for name, want := range map[string]string{"original": "new", "moved": "kept"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s contents=%q, want %q, error=%v", name, got, want, err)
		}
	}
}

func TestArm64DarwinWriterSetTimes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Darwin ARM64")
	}
	dir := t.TempDir()
	source, binary := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main")
	if err := os.WriteFile(source, []byte(writerSetTimesSource(dir, true)), 0o600); err != nil {
		t.Fatal(err)
	}
	fern := buildLangBinForInterp(t)
	if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	checkWriterSetTimesContents(t, dir)
}

func TestX86_64WriterSetTimes(t *testing.T) {
	dir := t.TempDir()
	if code, out := compileRunX86_64WithSetup(t, writerSetTimesSource(dir, true), nil); code != 0 {
		t.Fatalf("exit=%d, want 0\n%s", code, out)
	}
	checkWriterSetTimesContents(t, dir)
}

func TestArm64WriterSetTimes(t *testing.T) {
	dir := t.TempDir()
	if out, code := compileAndRunArm64(t, writerSetTimesSource(dir, true)); code != 0 {
		t.Fatalf("exit=%d, want 0\n%s", code, out)
	}
	checkWriterSetTimesContents(t, dir)
}

func TestInterpWriterSetTimes(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, writerSetTimesSource(dir, true)); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	checkWriterSetTimesContents(t, dir)
}

func TestSelfHostInterpWriterSetTimes(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(source, []byte(writerSetTimesSource(dir, true)), 0o600); err != nil {
		t.Fatal(err)
	}
	fern := e2eharness.SelfHostCLI(t)
	cmd := exec.Command(fern, "-interp", source, e2eharness.SelfHostStdlibRoot(t))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host interpreter: %v\n%s", err, out)
	}
	checkWriterSetTimesContents(t, dir)
}

func TestWASMPreview1WriterSetTimes(t *testing.T) {
	module := buildWasmCore(t, writerSetTimesSource("", false))
	dir := t.TempDir()
	if code := runPreview1Module(t, module, dir); code != 0 {
		t.Fatalf("main=%d, want 0", code)
	}
	checkWriterSetTimesContents(t, dir)
}

func TestWASMWriterSetTimes(t *testing.T) {
	dir := t.TempDir()
	out := runResultStdout(t, writerSetTimesSource("", false), runOpts{workDir: dir})
	if code := parseMainResult(t, out); code != 0 {
		t.Fatalf("main=%d, want 0\n%s", code, out)
	}
	checkWriterSetTimesContents(t, dir)
}
