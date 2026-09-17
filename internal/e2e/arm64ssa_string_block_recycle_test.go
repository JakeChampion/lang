package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// arm64ssa derives a block's freelist class from the size it is given, so the
// size a string is ALLOCATED at and the size it is FREED at have to be the same
// number. They were not: most producers allocated `len + 9` (rc, length, and
// the trailing NUL) while `__fern_str_dec` freed `len + 8`, and four producers
// allocated `len + 8` themselves.
//
// At most lengths the two round to the same 16-byte class and the disagreement
// cancels, which is why this survived. Exactly where `len + 9` lands ON a class
// boundary it does not: the block is pushed onto a class nothing ever asks for,
// so the freelist stops recycling entirely and the bump cursor runs away. A
// read_line loop over 8,000 lines of such a length allocated 16,002 blocks and
// reused NONE of them — `live_bytes` 128,032 against 32, growing 16 bytes a
// line with a constant live set (#9558).
//
// The affected widths are measured, not derived: a read_line loop drifts at
// L = 7, 23 and 39 and is clean at every neighbour. read_line keeps the
// newline, so the stored length is L+1 and the block request is a fixed offset
// above it; the exact offset is what the two ends used to disagree about, so
// the widths are pinned from observation rather than from arithmetic that the
// bug itself makes untrustworthy.
const arm64SSAStringBlockSrc = `function main(): i32 {
    var r: Reader = stdin();
    var n: i32 = 0;
    loop {
        match (r.read_line()) {
            Some(line) => { n = (n + line.len()) % 101; },
            None => { break; }
        }
    }
    return n % 7;
}
`

// liveBytesForWidth builds src with -backend ssa for arm64, feeds it `lines`
// lines of `width` characters, and returns the live_bytes the FERN_LEAKCHECK
// census reports at exit.
func liveBytesForWidth(t *testing.T, bin, qemu, dir string, width, lines int) int {
	t.Helper()
	name := fmt.Sprintf("recycle_%d_%d", width, lines)
	srcPath := filepath.Join(dir, name+".fern")
	binPath := filepath.Join(dir, name+".bin")
	inPath := filepath.Join(dir, name+".txt")
	if err := os.WriteFile(srcPath, []byte(arm64SSAStringBlockSrc), 0o644); err != nil {
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
	compile := exec.Command(bin, "-target", "arm64-linux", "-backend", "ssa", "-o", binPath, srcPath)
	compile.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("arm64 -backend ssa build failed: %v\n%s", err, out)
	}
	f, err := os.Open(inPath)
	if err != nil {
		t.Fatalf("open %s: %v", inPath, err)
	}
	defer f.Close()
	run := runArm64Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv()
	run.Stdin = f
	var errBuf strings.Builder
	run.Stderr = &errBuf
	// The program exits with `n % 7`, so a non-zero status is the normal path.
	_ = run.Run()
	m := leakcheckLive.FindStringSubmatch(errBuf.String())
	if m == nil {
		t.Fatalf("no leakcheck line in stderr for width %d:\n%s", width, errBuf.String())
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse live_bytes %q: %v", m[1], err)
	}
	return n
}

func TestArm64SSARecyclesStringBlocksOnAClassBoundary(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	for _, width := range []int{7, 23, 39} {
		t.Run(fmt.Sprintf("boundary_width_%d", width), func(t *testing.T) {
			small := liveBytesForWidth(t, bin, qemu, dir, width, 500)
			large := liveBytesForWidth(t, bin, qemu, dir, width, 4000)
			// 8x the lines. A recycling freelist holds the same handful of
			// bytes either way; one that has stopped holds 8x as much.
			if large > small+4096 {
				t.Errorf("width %d retains %d bytes over 500 lines and %d over 4000 — "+
					"retention grows with the input, so the freelist is not recycling.\n\n"+
					"arm64ssa derives a block's size CLASS from the byte count it is given, "+
					"so a string must be freed at the same size it was allocated at "+
					"(strBlockBytes). When the two disagree the block is pushed onto a class "+
					"nothing requests, every allocation bumps the cursor, and the heap grows "+
					"without bound while the live set stays constant (#9558).\n\n"+
					"Do not relax this bound: the two numbers should be near-identical.",
					width, small, large)
			}
		})
	}

	// A width that never sat on a boundary and was clean even with the bug, so
	// a failure here means something broader than #9558 has broken.
	t.Run("control_width_24", func(t *testing.T) {
		small := liveBytesForWidth(t, bin, qemu, dir, 24, 500)
		large := liveBytesForWidth(t, bin, qemu, dir, 24, 4000)
		if large > small+4096 {
			t.Errorf("the control width retains %d bytes over 500 lines and %d over 4000 — "+
				"this width never sat on a class boundary, so string reclaim has broken "+
				"more broadly than #9558", small, large)
		}
	})
}
