package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A `main` that returns nothing exits 0.
//
// The natives take the exit status from the return register after calling
// main, and for a void main that register holds whatever its last call
// left there — so the status was a stable fact about the EMITTED CODE
// rather than about the program: `print("hi")` alone exited 232, and one
// `isatty` in front of it exited 248 (#9233). The wasm side had already
// decided it the other way (`SynthCliRun`: void main → `i32.const 0`),
// so the three backends disagreed about the same program.
//
// What the program DOES matters: the leftover is whatever the last call
// returned, so a main that happens to end on a zero passes either way.
// This one ended on 232 on both natives before the fix — a concatenation
// and a print, the two most ordinary things a void main can do.
const voidMainExitSource = `function main(): void {
    var s: string = "abc";
    print(s + "d");
}
`

// An i32 main still decides its own status, which is the half that was
// never broken — and the one that proves the zeroing is gated rather
// than unconditional.
const i32MainExitSource = `function main(): i32 {
    print("hi");
    return 7;
}
`

func TestX86_64VoidMainExitsZero(t *testing.T) {
	bin, runner := compileX86_64Bin(t, voidMainExitSource)
	out, code := runWithPipes(t, runX86_64Bin(runner, bin))
	if code != 0 {
		t.Errorf("void main: exit = %d, want 0\n%s", code, out)
	}
	bin2, runner2 := compileX86_64Bin(t, i32MainExitSource)
	out2, code2 := runWithPipes(t, runX86_64Bin(runner2, bin2))
	if code2 != 7 {
		t.Errorf("i32 main: exit = %d, want 7\n%s", code2, out2)
	}
}

func TestArm64VoidMainExitsZero(t *testing.T) {
	bin, qemu := compileArm64Bin(t, voidMainExitSource)
	out, code := runWithPipes(t, runArm64Bin(qemu, bin))
	if code != 0 {
		t.Errorf("void main: exit = %d, want 0\n%s", code, out)
	}
	bin2, qemu2 := compileArm64Bin(t, i32MainExitSource)
	out2, code2 := runWithPipes(t, runArm64Bin(qemu2, bin2))
	if code2 != 7 {
		t.Errorf("i32 main: exit = %d, want 7\n%s", code2, out2)
	}
}

// The interpreter has always exited 0 for a void main; the case is here
// so the three answers are pinned in one place.
func TestInterpVoidMainExitsZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(voidMainExitSource), 0o644); err != nil {
		t.Fatal(err)
	}
	lang := buildLangBinForInterp(t)
	out, code := runWithPipes(t, exec.Command(lang, "-interp", p))
	if code != 0 {
		t.Errorf("void main: exit = %d, want 0\n%s", code, out)
	}
	p2 := filepath.Join(t.TempDir(), "prog2.fern")
	if err := os.WriteFile(p2, []byte(i32MainExitSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out2, code2 := runWithPipes(t, exec.Command(lang, "-interp", p2))
	if code2 != 7 {
		t.Errorf("i32 main: exit = %d, want 7\n%s", code2, out2)
	}
}
