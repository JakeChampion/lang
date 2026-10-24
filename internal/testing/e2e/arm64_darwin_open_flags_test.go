package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func buildAndRunDarwin(t *testing.T, dir, prog string) bool {
	t.Helper()
	bin := buildFernCLI(t)
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
		return false
	}
	cmd := exec.Command(out)
	cmd.Dir = dir
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
	}
	if code := ps.ExitCode(); code != 0 {
		t.Fatalf("program exited %d, want 0 (the I/O call reported an error)", code)
	}
	return true
}

const (
	stale = "STALE-CONTENT-THAT-IS-LONG"
	fresh = "short"
)

func TestArm64DarwinWriteFileTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	prog := "function main(): i32 {\n" +
		"  match (write_file(\"" + path + "\", \"" + fresh + "\")) {\n" +
		"    Ok(v) => { return 0; },\n" +
		"    Err(e) => { return 1; }\n" +
		"  }\n}\n"
	if !buildAndRunDarwin(t, dir, prog) {
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != fresh {
		t.Errorf("write_file left %q, want %q — the file was not truncated, so XNU got "+
			"O_CREAT without O_TRUNC and the old content's tail survived", got, fresh)
	}
}

func TestArm64DarwinOpenWriterTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	prog := "function main(): i32 {\n" +
		"  match (open_writer(\"" + path + "\")) {\n" +
		"    Ok(w) => {\n" +
		"      match (w.write(\"" + fresh + "\")) {\n" +
		"        Err(e) => { return 1; },\n" +
		"        Ok(_) => {}\n" +
		"      }\n" +
		"      match (w.close()) {\n" +
		"        Err(e) => { return 2; },\n" +
		"        Ok(_) => {}\n" +
		"      }\n" +
		"      return 0;\n" +
		"    },\n" +
		"    Err(e) => { return 3; }\n" +
		"  }\n}\n"
	if !buildAndRunDarwin(t, dir, prog) {
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != fresh {
		t.Errorf("open_writer left %q, want %q — the file was not truncated", got, fresh)
	}
}

func TestArm64DarwinOpenAppenderAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	const existing = "first-line"
	const added = "second-line"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	prog := "function main(): i32 {\n" +
		"  match (open_appender(\"" + path + "\")) {\n" +
		"    Ok(w) => {\n" +
		"      match (w.write(\"" + added + "\")) {\n" +
		"        Err(e) => { return 1; },\n" +
		"        Ok(_) => {}\n" +
		"      }\n" +
		"      match (w.close()) {\n" +
		"        Err(e) => { return 2; },\n" +
		"        Ok(_) => {}\n" +
		"      }\n" +
		"      return 0;\n" +
		"    },\n" +
		"    Err(e) => { return 3; }\n" +
		"  }\n}\n"
	if !buildAndRunDarwin(t, dir, prog) {
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if want := existing + added; string(got) != want {
		t.Errorf("open_appender left %q, want %q — a %q result means the open TRUNCATED "+
			"the file instead of appending (XNU read Linux's O_APPEND as O_TRUNC)",
			got, want, added)
	}
}
