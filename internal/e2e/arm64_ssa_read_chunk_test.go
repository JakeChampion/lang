package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// `read_chunk` sizes its buffer from the REQUEST and only learns what it owns
// after the read, so the arm64ssa helper hands the rest back: the unread tail of
// a short read, and the whole buffer at end of input or on an error, where the
// result carries no bytes at all (#8698 — one 64 KiB block abandoned per EOF
// probe, and a full-size block kept for every short read a pipe produced).
//
// Each program measures itself with __heap_bump_bytes() and exits 0 when the
// arena did not grow by a chunk over eight rounds, so the observable is the
// leak itself and not a proxy for it. The three cover one exit each: the trim on
// Ok, the rewind on Ok(""), and the rewind on Err — a fix to one of them leaves
// the other two red.
var arm64SSAReadChunkGates = []struct {
	name string
	src  string
	// The exit code the unfixed helper produces, so a case that starts passing
	// for some other reason cannot be mistaken for the leak being fixed.
	broken int
}{
	{
		name:   "eof_probe_keeps_nothing",
		broken: 98,
		src: `function put(path: string, content: string): i32 {
    match (write_file(path, content)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
    return 0;
}
function main(): i32 {
    if (put("rca.txt", "0123456789abcdef") != 0) { return 91; }
    match (open_reader("rca.txt")) {
        Ok(r) => {
            var first: i32 = match (r.read_chunk(65536)) { Ok(s) => s.len(), Err(e) => 0 - 1 };
            if (first != 16) { return 92; }
            var b0: i64 = __heap_bump_bytes();
            var i: i32 = 0;
            while (i < 8) {
                match (r.read_chunk(65536)) {
                    Ok(s) => { if (s.len() != 0) { return 93; } },
                    Err(e) => { return 94; }
                }
                i = i + 1;
            }
            var b1: i64 = __heap_bump_bytes();
            match (r.close()) { Some(e) => { return 95; }, None => {} }
            if ((b1 - b0) >= 65536) { return 98; }
        },
        Err(e) => { return 96; }
    }
    return 0;
}`,
	},
	{
		name:   "short_read_keeps_only_its_bytes",
		broken: 99,
		src: `function put(path: string, content: string): i32 {
    match (write_file(path, content)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
    return 0;
}
function main(): i32 {
    if (put("rcb.txt", "0123456789abcdef") != 0) { return 91; }
    var b0: i64 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 8) {
        match (open_reader("rcb.txt")) {
            Ok(r) => {
                var got: i32 = match (r.read_chunk(65536)) { Ok(s) => s.len(), Err(e) => 0 - 1 };
                if (got != 16) { return 92; }
                match (r.close()) { Some(e) => { return 95; }, None => {} }
            },
            Err(e) => { return 96; }
        }
        i = i + 1;
    }
    var b1: i64 = __heap_bump_bytes();
    if ((b1 - b0) >= 65536) { return 99; }
    return 0;
}`,
	},
	{
		// A directory opens but reads EISDIR, the one error a program can reach
		// without a broken fd.
		name:   "read_error_keeps_nothing",
		broken: 97,
		src: `function main(): i32 {
    match (open_reader(".")) {
        Ok(r) => {
            var b0: i64 = __heap_bump_bytes();
            var i: i32 = 0;
            while (i < 8) {
                match (r.read_chunk(65536)) {
                    Ok(s) => { return 92; },
                    Err(e) => {}
                }
                i = i + 1;
            }
            var b1: i64 = __heap_bump_bytes();
            match (r.close()) { Some(e) => { return 95; }, None => {} }
            if ((b1 - b0) >= 65536) { return 97; }
        },
        Err(e) => { return 96; }
    }
    return 0;
}`,
	},
}

func TestArm64SSAReadChunkKeepsOnlyWhatItRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64 -backend ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	fern := buildFernForArm64SSA(t)

	for _, c := range arm64SSAReadChunkGates {
		t.Run(c.name, func(t *testing.T) {
			bin := compileArm64SSA(t, fern, c.src, nil)
			// The programs write their fixture into the working directory.
			code, _ := runArm64SSABin(t, qemu, bin, t.TempDir(), nil)
			if code == c.broken {
				t.Fatalf("exit=%d: read_chunk grew the arena by a chunk over eight rounds", code)
			}
			if code != 0 {
				t.Fatalf("exit=%d, want 0 (9x = the program's own fixture / value checks)", code)
			}
		})
	}
}

// FERN_LEAKCHECK printed nothing at all on this backend, so no census could see
// #8698 — or anything else the SSA-direct arm64 runtime strands. The counters
// now sit where every allocation passes: the heap guard on the bump path, the
// freelist pop in __alloc, and __free on the way back.
//
// live_bytes is what the census exists to report, so the assertion is on the
// number and not merely on the line: the abandoned buffers push it past half a
// megabyte, and the same program lands in the hundreds of bytes without them.
func TestArm64SSALeakCheckReportsTheCensus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64 -backend ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	fern := buildFernForArm64SSA(t)
	leak := append(os.Environ(), "FERN_LEAKCHECK=1")

	t.Run("no_heap_program_reports_zeros", func(t *testing.T) {
		bin := compileArm64SSA(t, fern, `function main(): i32 { return 7; }`, leak)
		code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), nil)
		if code != 7 {
			t.Fatalf("exit=%d, want 7", code)
		}
		allocs, frees, live := parseLeakCheck(t, stderr)
		if allocs != 0 || frees != 0 || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d, want all zero", allocs, frees, live)
		}
	})

	t.Run("eof_probes_leave_no_chunk_live", func(t *testing.T) {
		bin := compileArm64SSA(t, fern, arm64SSAReadChunkGates[0].src, leak)
		code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), nil)
		if code != 0 {
			t.Fatalf("exit=%d, want 0\n%s", code, stderr)
		}
		allocs, _, live := parseLeakCheck(t, stderr)
		if allocs == 0 {
			t.Errorf("allocs=0 for a program that allocates: the census is not counting")
		}
		if live >= 65536 {
			t.Errorf("live_bytes=%d, want below one 64 KiB chunk", live)
		}
	})
}

var leakCheckLine = regexp.MustCompile(`leakcheck: allocs=(-?\d+) frees=(-?\d+) live_bytes=(-?\d+)\n`)

func parseLeakCheck(t *testing.T, stderr string) (allocs, frees, live int64) {
	t.Helper()
	m := leakCheckLine.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leakcheck census on stderr:\n%s", stderr)
	}
	n := func(s string) int64 {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("census field %q: %v", s, err)
		}
		return v
	}
	return n(m[1]), n(m[2]), n(m[3])
}

func buildFernForArm64SSA(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	return bin
}

// compileArm64SSA writes src to a scratch file and builds it through
// `-target arm64-linux -backend ssa`. A refusal is a failure, not a skip: every
// construct in these programs is inside the backend's subset.
func compileArm64SSA(t *testing.T, fern, src string, env []string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "main.bin")
	cmd := exec.Command(fern, "-target", "arm64-linux", "-backend", "ssa", "-o", out, srcPath)
	cmd.Env = env
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, o)
	}
	return out
}

// runArm64SSABin runs the binary in `dir` and returns its exit code and stderr —
// stderr on the SUCCESS path too, which is where the census lands.
func runArm64SSABin(t *testing.T, qemu, bin, dir string, env []string) (int, string) {
	t.Helper()
	cmd := runArm64Bin(qemu, bin)
	cmd.Dir = dir
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("run: %v", err)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}
