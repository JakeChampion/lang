package e2e

import (
	"os/exec"
	"testing"
)

const ownedPayloadFamilySrc = `import "std/i32";
import "std/i64";
import "std/io";
import "std/string";
function put(path: string, content: string): i32 {
    match (write_file(path, content)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
    return 0;
}
function chunks(path: string, size: i32): i32 {
    match (open_reader(path)) {
        Ok(r) => {
            let acc: i32 = 0;
            while (true) {
                match (r.read_chunk(size)) {
                    Ok(c) => { if (c.len() == 0) { break; } acc = acc + c.len(); },
                    Err(e) => { break; }
                }
            }
            match (r.close()) { Err(_) => { return -3; }, Ok(_) => {} }
            return acc;
        },
        Err(_) => { return -1; }
    }
    return -2;
}
function lines(path: string): i32 {
    match (open_reader(path)) {
        Ok(r) => {
            let acc: i32 = 0;
            while (true) {
                match (io.read_line(r)) {
                    Ok(Some(l)) => { acc = acc + l.len(); },
                    _ => { break; }
                }
            }
            match (r.close()) { Err(_) => { return -3; }, Ok(_) => {} }
            return acc;
        },
        Err(_) => { return -1; }
    }
    return -2;
}
function file(path: string): i32 {
    match (read_file(path)) { Ok(s) => { return s.len(); }, Err(_) => { return -1; } }
    return -2;
}
function filebytes(path: string): i32 {
    match (read_file_bytes(path)) { Ok(b) => { return b.len(); }, Err(_) => { return -1; } }
    return -2;
}
function take(path: string): string {
    match (read_file(path)) { Ok(s) => { return s; }, Err(_) => { return ""; } }
    return "";
}
function discard(path: string): i32 {
    match (read_file(path)) { Ok(_) => { return 1; }, Err(_) => { return 0; } }
    return 0;
}
function iflet(path: string): i32 {
    if let Ok(s) = read_file(path) { return s.len(); } else { return -1; }
}
function kept(path: string): i32 {
    let all: string[] = [];
    let i: i32 = 0;
    while (i < 3) {
        match (read_file(path)) { Ok(s) => { all = all.append(s); }, Err(_) => { return -1; } }
        i = i + 1;
    }
    return all[0].len() + all[2].len();
}
function rounds(path: string, size: i32, n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        acc = acc + chunks(path, size) + lines(path) + file(path) + filebytes(path)
            + take(path).len() + discard(path) + iflet(path) + kept(path);
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    // Both files hold ONE line, so read_line runs once per round at either
    // size and only the payload differs.
    let narrow: string = "0123456789abcde" + "\n";
    let wide: string = "0123456789abcde".repeat(68) + "\n";
    if (put("n.txt", narrow) != 0) { return 90; }
    if (put("w.txt", wide) != 0) { return 91; }
    let warm: i32 = rounds("n.txt", 64, 1);
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds("n.txt", 64, 20);
    let b1: i64 = __heap_bump_bytes();
    warm = warm + rounds("w.txt", 4096, 1);
    let b2: i64 = __heap_bump_bytes();
    let y: i32 = rounds("w.txt", 4096, 20);
    let b3: i64 = __heap_bump_bytes();
    if (warm <= 0 || x <= 0 || y <= x) { return 97; }
    if ((b3 - b2) > 2 * (b1 - b0)) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

const ownedPayloadEscapeSrc = `import "std/i32";
import "std/string";
function put(path: string, content: string): i32 {
    match (write_file(path, content)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
    return 0;
}
function main(): i32 {
    if (put("e.txt", "0123456789abcdefghij") != 0) { return 90; }
    let kept: string[] = [];
    let i: i32 = 0;
    while (i < 20) {
        match (read_file("e.txt")) {
            Ok(s) => { kept = kept.append(s); },
            Err(_) => { return 91; }
        }
        i = i + 1;
    }
    if (kept.len() != 20) { return 92; }
    if (kept[0] != "0123456789abcdefghij") { return 93; }
    if (kept[19].len() != 20) { return 94; }
    return __rc_underflow_count();
}`

func ownedPayloadCheck(t *testing.T, name string, code int) {
	t.Helper()
	if code != 0 {
		t.Errorf("%s: code=%d (98=fresh bytes scale with the payload, 99=over-release, 97=value, 9x=fixture)", name, code)
	}
}

func runX86_64FreeOnInDir(t *testing.T, src string) int {
	t.Helper()
	binPath, runner := compileX86_64FreeOn(t, src)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	cmd.Dir = t.TempDir()
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Logf("output:\n%s", out)
		return code
	}
	return 0
}

func runArm64FreeOnInDir(t *testing.T, src string) int {
	t.Helper()
	binPath, qemu := compileArm64FreeOn(t, src)
	cmd := runArm64Bin(qemu, binPath)
	cmd.Dir = t.TempDir()
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Logf("output:\n%s", out)
		return code
	}
	return 0
}

func TestX86_64OwnedPayloadMatchReclaim(t *testing.T) {
	ownedPayloadCheck(t, "family", runX86_64FreeOnInDir(t, ownedPayloadFamilySrc))
	ownedPayloadCheck(t, "escape", runX86_64FreeOnInDir(t, ownedPayloadEscapeSrc))
}

func TestArm64OwnedPayloadMatchReclaim(t *testing.T) {
	ownedPayloadCheck(t, "family", runArm64FreeOnInDir(t, ownedPayloadFamilySrc))
	ownedPayloadCheck(t, "escape", runArm64FreeOnInDir(t, ownedPayloadEscapeSrc))
}

func TestWASMOwnedPayloadMatchReclaim(t *testing.T) {
	for _, c := range []struct{ name, src string }{{"family", ownedPayloadFamilySrc}, {"escape", ownedPayloadEscapeSrc}} {
		stdout, stderr, ec, _ := runWasmInDir(t, c.src, nil)
		if ec != 0 {
			t.Fatalf("%s: wasmtime exit %d\nstderr:\n%s", c.name, ec, stderr)
		}
		ownedPayloadCheck(t, c.name, parseMainResult(t, stdout))
	}
}
