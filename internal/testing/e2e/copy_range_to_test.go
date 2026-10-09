// `r.copy_range_to(w, max)` end to end: copy_file_range(2) between two files
// on Linux, and `Unsupported` wherever the kernel cannot copy that pair, which
// promises nothing moved so a read_chunk / write fallback carries on. The
// probe checks three shapes:
//
//   - file to file, which the kernel copies on the Linux backends; the copy
//     is read back through Go's own read.
//   - a writer opened for APPEND, which copy_file_range(2) refuses (EBADF):
//     Unsupported, and the next read_chunk still sees the first byte.
//   - file to stdout, a pipe in these runners, which it also refuses, so the
//     fallback carries every byte.
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
                match (w.write(c)) { None => {}, Some(_) => { return 0 - 2; } }
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

    let r: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 11; } };
    let w: Writer = match (open_writer(%[2]q)) { Ok(h) => h, Err(_) => { return 12; } };
    let how: i32 = copy(r, w);
    r.close();
    w.close();
    if (how < 0) { return 13; }
    if (how != %[3]d) { return 14; }

    let r2: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 20; } };
    let a: Writer = match (open_appender(%[2]q)) { Ok(h) => h, Err(_) => { return 21; } };
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

    let r3: Reader = match (open_reader(%[1]q)) { Ok(h) => h, Err(_) => { return 30; } };
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

// The interpreter refuses every call and the fallback carries the bytes.
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
