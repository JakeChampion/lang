package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The result of the recursive call in `ins` lives across the calls of the
// balance arms, and each saved register holds a value at that call. Two of
// those values are dead after the call, so the result takes the register of
// one of them and goes to no frame slot.
func TestSelfHostSSASavedRegisterLends(t *testing.T) {
	h := selfHostCLIForHost(t)
	src, err := filepath.Abs(filepath.Join("..", "..", "..", "bench", "perceus", "fern", "run_rbtree.fern"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rbtree.s")
	if combined, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	fn := functionListing(string(asm), "__fn_rbtree__ins")
	if fn == "" {
		t.Fatal("rbtree.ins not in the listing")
	}
	lines := strings.Split(fn, "\n")
	calls := 0
	for i, line := range lines {
		if strings.TrimSpace(line) != "call __fn_rbtree__ins.r" || i+1 >= len(lines) {
			continue
		}
		calls++
		next := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(next, "movq %rax, ") && strings.HasSuffix(next, "(%rbp)") {
			t.Errorf("the result of a recursive call goes to the frame: %q", next)
		}
	}
	if calls == 0 {
		t.Fatal("rbtree.ins makes no recursive call, so the test checks nothing")
	}
}

// The entry of a function writes each parameter. `w` is read after `base` is
// masked, and its instruction comes after the mask in the block. The mask
// took the register of `w`, and the select then read the mask in its place.
const paramLiveProg = `@noinline function word(buf: i64, base: i64, rd: i64, rn: i64, rm: i64, w: boolean): i64 {
    let b = base;
    if (w) {
        b = base & 0x7fffffffi64;
    }
    return b | (rm & 31i64) << 16i64 | (rn & 31i64) << 5i64 | (rd & 31i64) | buf;
}
function main(): i32 {
    if (word(0i64, 0x9bc07c00i64, 5i64, 4i64, 5i64, false) != 0x9bc57c85i64) {
        return 1;
    }
    if (word(0i64, 0x9bc07c00i64, 5i64, 4i64, 5i64, true) != 0x1bc57c85i64) {
        return 2;
    }
    return 0;
}
`

func TestSelfHostSSAParameterIsLiveFromTheEntry(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "param.fern")
	if err := os.WriteFile(src, []byte(paramLiveProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 0 {
			t.Errorf("%s: exit %d, want 0", tg.target, code)
		}
	}
}
