package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// readFileUtf8Program pins read_file's UTF-8 validation (D9, #5714) against
// std/utf8.is_valid_utf8, the language-level reference, on the backend's
// own runtime: every 1- and 2-byte sequence, every 3- and 4-byte lead with
// every first continuation and the second and third continuations at each
// edge of their range, the truncation of each length, and a multibyte or
// invalid byte at every offset across an eight-byte ASCII word.
//
// The runtime helper behind read_file, `__fern_utf8_valid`, is one Fern
// source (internal/fernrt) lowered per target, so this is the same body on
// every backend; what the test proves per backend is that body's lowering.
//
// Prints read-file-utf8-agrees on success, or FAIL and the step that
// disagreed; the exit code says the same for the harnesses that report it.
const readFileUtf8Program = `
import "std/utf8" as utf8;

// 1 when read_file accepts the bytes as text, 0 when it reports
// InvalidUtf8, 2 on any other error.
function accepted(bytes: u8[]): i32 {
    var s: string = string_from_bytes_unchecked(bytes);
    var wrote: i32 = match (write_file("probe.bin", s)) { Ok(_) => 1, Err(_) => 0 };
    if (wrote == 0) { return fail(2); }
    return match (read_file("probe.bin")) {
        Ok(_) => 1,
        Err(e) => match (e) { InvalidUtf8(_) => 0, _ => 2 }
    };
}

function agree(bytes: u8[]): boolean {
    var want: i32 = 0;
    if (utf8.is_valid_utf8(string_from_bytes_unchecked(bytes))) { want = 1; }
    return accepted(bytes) == want;
}

function fail(step: i32): i32 {
    write("FAIL " + step.to_string());
    return step;
}

function main(): i32 {
    var a: i32 = 0;
    while (a < 256) {
        if (!agree([a as u8])) { return fail(1); }
        a = a + 1;
    }
    var p: i32 = 0;
    while (p < 256) {
        var q: i32 = 0;
        while (q < 256) {
            if (!agree([p as u8, q as u8])) { return fail(2); }
            q = q + 1;
        }
        p = p + 1;
    }
    var edges: i32[] = [127, 128, 191, 192];
    var lead: i32 = 224;
    while (lead < 256) {
        var c1: i32 = 126;
        while (c1 < 194) {
            if (!agree([lead as u8, c1 as u8])) { return fail(3); }
            var i: i32 = 0;
            while (i < edges.len()) {
                var c2: i32 = edges[i];
                if (!agree([lead as u8, c1 as u8, c2 as u8])) { return fail(4); }
                var j: i32 = 0;
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
    var off: i32 = 0;
    while (off < 24) {
        var good: u8[] = [];
        var bad: u8[] = [];
        var k: i32 = 0;
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
    write("read-file-utf8-agrees");
    return 0;
}
`

// compileNativeModloadInDir is compileX86_64InDir / compileArm64InDir with
// the program loaded through modload, so its std/ imports resolve. Runs the
// binary in a fresh temp dir and returns its output and exit code.
func compileNativeModloadInDir(t *testing.T, target string, src string) (string, int) {
	t.Helper()
	return compileNativeModloadInDirAt(t, target, src, t.TempDir())
}

func compileNativeModloadInDirAt(t *testing.T, target string, src string, dir string) (string, int) {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	var cmd *exec.Cmd
	switch target {
	case "x86-64":
		gcc, runner := x86_64Tooling(t)
		asm, err := x86_64.Emit(prog, info)
		if err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		if len(runner) == 0 {
			cmd = exec.Command(binPath)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
		}
	case "arm64":
		gcc, qemu := arm64Tooling(t)
		asm, err := arm64codegen.Emit(prog, info)
		if err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		cmd = runArm64Bin(qemu, binPath)
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
	out, code := compileNativeModloadInDir(t, "x86-64", readFileUtf8Program)
	checkReadFileUtf8Agrees(t, out, code)
}

func TestArm64ReadFileUtf8AgreesWithStdUtf8(t *testing.T) {
	out, code := compileNativeModloadInDir(t, "arm64", readFileUtf8Program)
	checkReadFileUtf8Agrees(t, out, code)
}

// The component harness does not surface main's return, so this leg reads
// the marker alone.
func TestWASMReadFileUtf8AgreesWithStdUtf8(t *testing.T) {
	stdout, stderr, _, _ := runWasmInDir(t, readFileUtf8Program, nil)
	if !strings.Contains(stdout, "read-file-utf8-agrees") {
		t.Fatalf("stdout %q stderr %q; want read-file-utf8-agrees", stdout, stderr)
	}
}
