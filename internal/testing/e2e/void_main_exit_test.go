package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const voidMainExitSource = `function main(): void {
    let s: string = "abc";
    print(s + "d");
}
`

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
