package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const loopSplitProg = `import "std/deflate";

function text(n: i32): u8[] {
    let words: string[] = ["the ", "of ", "and ", "a ", "to ", "in ", "is ", "you ", "that ", "it "];
    let out: u8[] = [];
    let seed = 12345;
    while (out.len() < n) {
        seed = seed * 1103515245 + 12345 & 2147483647;
        let w = words[(seed >> 16) % words.len()].as_bytes();
        for i in 0..w.len() {
            out = out.append(w[i]);
        }
    }
    return out;
}

function main(): i32 {
    let input = text(20000);
    let Ok(got) = deflate.inflate(deflate.deflate(input), input.len()) else {
        return 120;
    };
    for i in 0..input.len() {
        if (got.out[i] != input[i]) {
            return 121;
        }
    }
    return got.out.len() % 97;
}
`

// On x86-64 the decode loop of std/deflate keeps values in frame slots for
// its inner copy loop, and the allocator splits them around it: a frame
// cell holds each one, and the rest of the loop keeps it in a register. The
// round trip still gives the input back on each target.
func TestSelfHostLoopSplit(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "split.fern")
	if err := os.WriteFile(src, []byte(loopSplitProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "x86.s")
	cmd := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC=deflate____codes")
	listing, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("emitting: %v\n%s", err, listing)
	}
	if !strings.Contains(string(listing), "FERN_SSA_ALLOC: deflate____codes") {
		t.Fatalf("no allocation of deflate____codes in the listing:\n%s", listing)
	}
	if !strings.Contains(string(listing), "= frame_cell") {
		t.Errorf("deflate____codes has no value split around its inner loop:\n%s", listing)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 20000%97 {
			t.Errorf("%s: exit %d, want %d", tg.target, got, 20000%97)
		}
	}
}
