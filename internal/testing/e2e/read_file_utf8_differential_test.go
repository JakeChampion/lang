package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// readFileUtf8Program pins read_file's UTF-8 validation (D9, #5714) against
// std/utf8.is_valid_utf8, the language-level reference, on the backend's
// own runtime: every 1- and 2-byte sequence, every 3- and 4-byte lead with
// every first continuation and the second and third continuations at each
// edge of their range, the truncation of each length, and a multibyte or
// invalid byte at every offset across an eight-byte ASCII word.
//
// What the test proves per target is the lowering of the runtime helper
// behind read_file.
//
// Prints read-file-utf8-agrees on success, or FAIL and the step that
// disagreed; the exit code, or main's result on wasm, says the same.
const readFileUtf8Program = `
import "std/utf8" as utf8;

// 1 when read_file accepts the bytes as text, 0 when it reports
// InvalidUtf8, 2 on any other error.
function accepted(bytes: u8[]): i32 {
    let s: string = string_from_bytes_unchecked(bytes);
    let wrote: i32 = match (write_file("probe.bin", s)) { Ok(_) => 1, Err(_) => 0 };
    if (wrote == 0) { return fail(2); }
    return match (read_file("probe.bin")) {
        Ok(_) => 1,
        Err(e) => match (e) { InvalidUtf8(_) => 0, _ => 2 }
    };
}

function agree(bytes: u8[]): boolean {
    let want: i32 = 0;
    if (utf8.is_valid_utf8(string_from_bytes_unchecked(bytes))) { want = 1; }
    return accepted(bytes) == want;
}

function fail(step: i32): i32 {
    write("FAIL " + step.to_string() + "\n");
    return step;
}

function main(): i32 {
    let a: i32 = 0;
    while (a < 256) {
        if (!agree([a as u8])) { return fail(1); }
        a = a + 1;
    }
    let p: i32 = 0;
    while (p < 256) {
        let q: i32 = 0;
        while (q < 256) {
            if (!agree([p as u8, q as u8])) { return fail(2); }
            q = q + 1;
        }
        p = p + 1;
    }
    let edges: i32[] = [127, 128, 191, 192];
    let lead: i32 = 224;
    while (lead < 256) {
        let c1: i32 = 126;
        while (c1 < 194) {
            if (!agree([lead as u8, c1 as u8])) { return fail(3); }
            let i: i32 = 0;
            while (i < edges.len()) {
                let c2: i32 = edges[i];
                if (!agree([lead as u8, c1 as u8, c2 as u8])) { return fail(4); }
                let j: i32 = 0;
                while (j < edges.len()) {
                    if (!agree([lead as u8, c1 as u8, c2 as u8, edges[j] as u8])) { return fail(5); }
                    j = j + 1;
                }
                i = i + 1;
            }
            c1 = c1 + 1;
        }
        lead = lead + 1;
    }
    let off: i32 = 0;
    while (off < 24) {
        let good: u8[] = [];
        let bad: u8[] = [];
        let k: i32 = 0;
        while (k < off) {
            good = good.append(97 as u8);
            bad = bad.append(97 as u8);
            k = k + 1;
        }
        good = good.append(195 as u8);
        good = good.append(169 as u8);
        bad = bad.append(255 as u8);
        k = 0;
        while (k < 20) {
            good = good.append(98 as u8);
            bad = bad.append(98 as u8);
            k = k + 1;
        }
        if (!agree(good)) { return fail(6); }
        if (!agree(bad)) { return fail(7); }
        off = off + 1;
    }
    write("read-file-utf8-agrees\n");
    return 0;
}
`

// compileRunInDir compiles a program that imports from std/ for target
// ("x86-64" or "arm64"), runs the binary in a fresh temp dir and returns its
// output and exit code.
func compileRunInDir(t *testing.T, target string, src string) (string, int) {
	t.Helper()
	return compileRunInDirAt(t, target, src, t.TempDir())
}

func compileRunInDirAt(t *testing.T, target string, src string, dir string) (string, int) {
	t.Helper()
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	var cmd *exec.Cmd
	switch target {
	case "x86-64":
		_, runner := x86_64Tooling(t)
		cmd = e2eharness.RunX86_64Bin(runner, e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, srcPath, nil))
	case "arm64":
		_, qemu := arm64Tooling(t)
		cmd = runArm64Bin(qemu, e2eharness.CompileSelfHostFile(t, e2eharness.TargetArm64Linux, srcPath, nil))
	default:
		t.Fatalf("unknown target %q", target)
	}
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func checkReadFileUtf8Agrees(t *testing.T, out string, code int) {
	t.Helper()
	if code != 0 || !strings.Contains(out, "read-file-utf8-agrees") {
		t.Fatalf("exit %d, output %q; want exit 0 and read-file-utf8-agrees", code, out)
	}
}

func TestX86_64ReadFileUtf8AgreesWithStdUtf8(t *testing.T) {
	out, code := compileRunInDir(t, "x86-64", readFileUtf8Program)
	checkReadFileUtf8Agrees(t, out, code)
}

func TestArm64ReadFileUtf8AgreesWithStdUtf8(t *testing.T) {
	out, code := compileRunInDir(t, "arm64", readFileUtf8Program)
	checkReadFileUtf8Agrees(t, out, code)
}

func TestWASMReadFileUtf8AgreesWithStdUtf8(t *testing.T) {
	stdout, stderr, _, _ := runWasmInDir(t, readFileUtf8Program, nil)
	if got := parseMainResult(t, stdout); got != 0 || !strings.Contains(stdout, "read-file-utf8-agrees") {
		t.Fatalf("main = %d, stdout %q stderr %q; want 0 and read-file-utf8-agrees", got, stdout, stderr)
	}
}
