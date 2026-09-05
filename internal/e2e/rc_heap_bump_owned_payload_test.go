package e2e

import (
	"os/exec"
	"testing"
)

// A match over `read_chunk` / `read_line` / `read_file` / `read_file_bytes`
// binds a payload the runtime built fresh for the caller inside an immortal
// Option / Result box. Nothing released it: the box needed no release, the
// binding was a borrow, and the exit sweep skipped it — so every chunk of a
// streaming loop and every line of a read_line loop leaked (#8396). The
// bindings are counted owners now (consumingBindings), dropped at the bind
// site on the next iteration and by the exit sweep otherwise, with a `_`
// dropped on the spot.
//
// The runtime's own half (#8399): read_chunk stranded a short read's buffer
// below its size class on x86-64 and wasm (the string frees at length + 1 /
// length, the block was bumped at n), and leaked the buffer on EOF on every
// backend. Reading a 1 KB file with a 4 KB request exercises both — a short
// read, then None.
//
// The observable is that fresh memory does not SCALE with the payload size:
// the same rounds over a 1024-byte file must cost no more fresh bytes than
// over a 16-byte one, up to a factor that tolerates the per-call immortal
// boxes (a constant, independent of the payload). A warm-up round at each
// size keeps the first allocation of each class out of the comparison. Each
// program is self-checking and exits 0 when the shape holds: 98 = scales,
// 99 = over-release, 90s = the fixture files could not be written.
const ownedPayloadFamilySrc = `import "std/i32";
import "std/i64";
import "std/string";
function put(path: string, content: string): i32 {
    match (write_file(path, content)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
    return 0;
}
function chunks(path: string, size: i32): i32 {
    match (open_reader(path)) {
        Ok(r) => {
            var acc: i32 = 0;
            while (true) {
                match (r.read_chunk(size)) {
                    Some(c) => { acc = acc + c.len(); },
                    None => { break; }
                }
            }
            match (r.close()) { Some(_) => { return -3; }, None => {} }
            return acc;
        },
        Err(_) => { return -1; }
    }
    return -2;
}
function lines(path: string): i32 {
    match (open_reader(path)) {
        Ok(r) => {
            var acc: i32 = 0;
            while (true) {
                match (r.read_line()) {
                    Some(l) => { acc = acc + l.len(); },
                    None => { break; }
                }
            }
            match (r.close()) { Some(_) => { return -3; }, None => {} }
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
    var all: string[] = [];
    var i: i32 = 0;
    while (i < 3) {
        match (read_file(path)) { Ok(s) => { all = all.append(s); }, Err(_) => { return -1; } }
        i = i + 1;
    }
    return all[0].len() + all[2].len();
}
function rounds(path: string, size: i32, n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
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
    var narrow: string = "0123456789abcde" + "\n";
    var wide: string = "0123456789abcde".repeat(68) + "\n";
    if (put("n.txt", narrow) != 0) { return 90; }
    if (put("w.txt", wide) != 0) { return 91; }
    var warm: i32 = rounds("n.txt", 64, 1);
    var b0: i64 = __heap_bump_bytes();
    var x: i32 = rounds("n.txt", 64, 20);
    var b1: i64 = __heap_bump_bytes();
    warm = warm + rounds("w.txt", 4096, 1);
    var b2: i64 = __heap_bump_bytes();
    var y: i32 = rounds("w.txt", 4096, 20);
    var b3: i64 = __heap_bump_bytes();
    if (warm <= 0 || x <= 0 || y <= x) { return 97; }
    if ((b3 - b2) > 2 * (b1 - b0)) { return 98; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`

// The binding escapes into a container that outlives the loop — an append
// retains, so the container's reference must survive the per-iteration
// release. Reads the kept strings back after the loop: a use-after-free
// here is a wrong answer or a crash, never a byte count.
const ownedPayloadEscapeSrc = `import "std/i32";
import "std/string";
function put(path: string, content: string): i32 {
    match (write_file(path, content)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
    return 0;
}
function main(): i32 {
    if (put("e.txt", "0123456789abcdefghij") != 0) { return 90; }
    var kept: string[] = [];
    var i: i32 = 0;
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
		_, stderr, code, _ := runWasmInDir(t, c.src, nil)
		if code != 0 {
			t.Logf("stderr:\n%s", stderr)
		}
		ownedPayloadCheck(t, c.name, code)
	}
}
