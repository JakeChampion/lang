package e2e

import (
	"fmt"
	"path/filepath"
	"testing"
)

func copyRangeToSource(dir string, copied bool) string {
	p := func(name string) string {
		if dir == "" {
			return name
		}
		return filepath.Join(dir, name)
	}
	want := 0
	if copied {
		want = 1
	}
	return fmt.Sprintf(`// copy moves r to w: 1 when some bytes were copied by the kernel, 0 when
// every byte was read and written, negative on a failure.
function copy(r: Reader, w: Writer): i32 {
    let copied: i32 = 0;
    let more: boolean = true;
    while (more) {
        match (r.copy_range_to(w, 65536)) {
            Ok(n) => {
                if (n == 0i64) { return copied; }
                copied = 1;
            },
            Err(e) => {
                match (e) {
                    Unsupported => { more = false; },
                    _ => { return 0 - 1; }
                }
            }
        }
    }
    while (true) {
        match (r.read_chunk(65536)) {
            Ok(c) => {
                if (c.len() == 0) { return copied; }
                match (w.write(c)) { Ok(_) => {}, Err(_) => { return 0 - 2; } }
            },
            Err(_) => { return 0 - 3; }
        }
    }
}

function main(): i32 {
    let data: string = "splice me through a pipe\n";
    let k: i32 = 0;
    while (k < 13) {
        data = data + data;
        k = k + 1;
    }
    match (write_file(%[1]q, data)) { Ok(_) => {}, Err(_) => { return 10; } }

    let Ok(r) = open_reader(%[1]q) else { return 11; };
    let Ok(w) = open_writer(%[2]q) else { return 12; };
    let how: i32 = copy(r, w);
    r.close();
    w.close();
    if (how < 0) { return 13; }
    if (how != %[3]d) { return 14; }

    let Ok(r2) = open_reader(%[1]q) else { return 20; };
    let Ok(a) = open_appender(%[2]q) else { return 21; };
    match (r2.copy_range_to(a, 4096)) {
        Ok(_) => { return 22; },
        Err(e) => {
            match (e) {
                Unsupported => {},
                _ => { return 23; }
            }
        }
    }
    match (r2.read_chunk(6)) {
        Ok(c) => { if (c != "splice") { return 24; } },
        Err(_) => { return 25; }
    }
    r2.close();
    a.close();

    let Ok(r3) = open_reader(%[1]q) else { return 30; };
    let how3: i32 = copy(r3, stdout());
    r3.close();
    if (how3 != 0) { return 31; }
    return 0;
}
`, p("data.txt"), p("copy.txt"), want)
}

func TestX86_64CopyRangeTo(t *testing.T) {
	dir := t.TempDir()
	code, _ := compileRunX86_64WithSetup(t, copyRangeToSource(dir, true), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see copyRangeToSource)", code)
	}
	spliceToCheckTree(t, dir)
}

func TestArm64CopyRangeTo(t *testing.T) {
	dir := t.TempDir()
	out, code := compileAndRunArm64(t, copyRangeToSource(dir, true))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see copyRangeToSource)", code)
	}
	spliceToCheckTree(t, dir)
	spliceToCheckStdout(t, out)
}

func TestInterpCopyRangeToUnsupported(t *testing.T) {
	dir := t.TempDir()
	if code := runInterpExit(t, copyRangeToSource(dir, false)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see copyRangeToSource)", code)
	}
	spliceToCheckTree(t, dir)
}

func TestWASMPreview1CopyRangeToUnsupported(t *testing.T) {
	mod := buildWasmCore(t, copyRangeToSource("", false))
	dir := t.TempDir()
	if got := runPreview1Module(t, mod, dir); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see copyRangeToSource)", got)
	}
	spliceToCheckTree(t, dir)
}

func TestWASMCopyRangeToUnsupported(t *testing.T) {
	dir := t.TempDir()
	out := runResultStdout(t, copyRangeToSource("", false), runOpts{workDir: dir})
	if got := parseMainResult(t, out); got != 0 {
		t.Fatalf("main = %d, want 0 — the code names the step (see copyRangeToSource)", got)
	}
	spliceToCheckTree(t, dir)
	spliceToCheckStdout(t, out)
}

func TestArm64DarwinCopyRangeToUnsupported(t *testing.T) {
	dir := t.TempDir()
	if !buildAndRunDarwin(t, dir, copyRangeToSource(dir, false)) {
		return
	}
	spliceToCheckTree(t, dir)
}
