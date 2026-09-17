package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// x86_64ssa derives a block's freelist class from the size it is given, so the
// size a string is ALLOCATED at and the size it is FREED at have to be the same
// number. They were not: six producers (read_line, read_dir, read_file,
// read_chunk, hostname, the io_error message) allocated `len + 9` — the rc
// word, the length word and the trailing NUL — while `__fern_str_dec` freed
// `len + 8` and three producers (string_from_bytes, __str_slice, __str_concat)
// allocated that much themselves.
//
// At most lengths the two round to the same 16-byte class and the disagreement
// cancels, which is why this survived. Where `len + 8` lands exactly ON a class
// boundary it does not: the block is pushed onto the class below the one it was
// taken from, nothing ever asks for that class again, and every allocation
// bumps the cursor instead. The live set stays constant while the heap grows
// (#9568).
//
// The affected widths are measured, not derived: a read_line loop bumps a fresh
// class per line at L = 7, 23, 39 and 55 and bumps nothing at every neighbour.
// read_line keeps the newline, so the stored length is L+1 and the block
// request is a fixed offset above it; the exact offset is what the two ends
// used to disagree about, so the widths are pinned from observation rather than
// from arithmetic the bug itself makes untrustworthy.
//
// The instrument is __heap_bump_bytes(), the arena cursor, divided by the lines
// read: bytes of fresh arena per line. A freelist that recycles gives exactly
// 0 — every line reuses the previous line's block. A stranded class gives the
// whole class size per line, which is what the boundary widths reported before
// the fix (32, 48, 64 and 80 bytes a line respectively). RSS cannot see this:
// the per-iteration Option box allocates and frees alongside the string, so it
// masks which class is leaking.
const x86SSAStringBlockSrc = `function main(): i32 {
    var r: Reader = stdin();
    var n: i32 = 0;
    var start: i64 = __heap_bump_bytes();
    var lines: i64 = 0;
    loop {
        match (r.read_line()) {
            Some(line) => { n = (n + line.len()) % 101; lines = lines + 1; },
            None => { break; }
        }
    }
    var used: i64 = __heap_bump_bytes() - start;
    if (lines == 0) { return 250; }
    var per: i64 = (used / lines) + (n % 1);
    if (per > 200) { return 200; }
    return per as i32;
}
`

// bumpedBytesPerLine builds the read_line probe for x86-64 with -backend ssa,
// feeds it `lines` lines of `width` characters, and returns the bytes of fresh
// arena the program bumped per line, which it reports as its exit status.
func bumpedBytesPerLine(t *testing.T, bin, qemu, dir string, width, lines int) int {
	t.Helper()
	name := fmt.Sprintf("bump_%d_%d", width, lines)
	srcPath := filepath.Join(dir, name+".fern")
	binPath := filepath.Join(dir, name+".bin")
	inPath := filepath.Join(dir, name+".txt")
	if err := os.WriteFile(srcPath, []byte(x86SSAStringBlockSrc), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	var in strings.Builder
	for i := 0; i < lines; i++ {
		in.WriteString(strings.Repeat("y", width))
		in.WriteByte('\n')
	}
	if err := os.WriteFile(inPath, []byte(in.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", inPath, err)
	}
	compile := exec.Command(bin, "-target", "x86-64-linux", "-backend", "ssa", "-o", binPath, srcPath)
	compile.Env = e2eharness.ChildEnv()
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("x86-64 -backend ssa build failed: %v\n%s", err, out)
	}
	f, err := os.Open(inPath)
	if err != nil {
		t.Fatalf("open %s: %v", inPath, err)
	}
	defer f.Close()
	run := runX86Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv()
	run.Stdin = f
	var errBuf strings.Builder
	run.Stderr = &errBuf
	// The program reports through its exit status, so a non-zero status is
	// the normal path; only a signal is a genuine failure.
	err = run.Run()
	ee, ok := err.(*exec.ExitError)
	if err != nil && !ok {
		t.Fatalf("run width %d: %v\n%s", width, err, errBuf.String())
	}
	code := 0
	if ee != nil {
		if ee.ExitCode() < 0 {
			t.Fatalf("width %d died on a signal: %v\n%s", width, ee, errBuf.String())
		}
		code = ee.ExitCode()
	}
	if code == 250 {
		t.Fatalf("width %d read no lines at all — the probe never ran", width)
	}
	return code
}

func TestX86_64SSARecyclesStringBlocksOnAClassBoundary(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	// Every width, boundary or not, must bump nothing per line: the block the
	// previous line freed is the block this line allocates.
	for _, width := range []int{7, 23, 39, 55} {
		t.Run(fmt.Sprintf("boundary_width_%d", width), func(t *testing.T) {
			if per := bumpedBytesPerLine(t, bin, qemu, dir, width, 20000); per != 0 {
				t.Errorf("width %d bumps %d bytes of fresh arena per line — the freelist is not recycling.\n\n"+
					"x86_64ssa derives a block's size CLASS from the byte count it is given, so a "+
					"string must be freed at the same size it was allocated at (strBlockBytes). "+
					"When the two disagree the block is pushed onto a class nothing requests, every "+
					"allocation bumps the cursor, and the heap grows without bound while the live "+
					"set stays constant (#9568).\n\n"+
					"Do not relax this to a bound: the correct value is exactly 0.", width, per)
			}
		})
	}

	// Widths either side of each boundary, which recycled even with the bug.
	// A failure here means something broader than #9568 has broken.
	for _, width := range []int{6, 8, 22, 24, 38, 40} {
		t.Run(fmt.Sprintf("control_width_%d", width), func(t *testing.T) {
			if per := bumpedBytesPerLine(t, bin, qemu, dir, width, 20000); per != 0 {
				t.Errorf("control width %d bumps %d bytes per line — this width never sat on a "+
					"class boundary, so string reclaim has broken more broadly than #9568", width, per)
			}
		})
	}
}
