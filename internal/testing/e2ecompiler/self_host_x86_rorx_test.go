package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func TestSelfHostX86GasRorx(t *testing.T) {
	runX86GasWasmSelfTest(t, "x86_gas_rorx", x86GasRorxMain)
}

const x86GasRorxMain = `
function main(): i32 {
    let src: string = "    .text\n_start:\n    rorxl $6, %ebx, %r10d\n    rorxq $13, %r9, %rax\n    rorxl $2, -80(%rbp), %eax\n    rorxq $7, 8(%r12,%r13,4), %r15\n    rorxl $25, %r8d, %r8d\n    rorxq $63, -264(%rbp), %rdi\n";
    let a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 90; }
    let exp: i32[] = [196, 99, 123, 240, 211, 6, 196, 195, 251, 240, 193, 13, 196, 227, 123, 240, 69, 176, 2, 196, 3, 251, 240, 124, 172, 8, 7, 196, 67, 123, 240, 192, 25, 196, 227, 251, 240, 189, 248, 254, 255, 255, 63];
    if (a.text.len() != exp.len()) { return 91; }
    let i: i32 = 0;
    while (i < exp.len()) {
        if (a.text[i] as i32 != exp[i]) { return i + 1; }
        i = i + 1;
    }
    return 0;
}
`

const rotateProg = `import "std/crypto";
function main(): i32 {
    if (crypto.sha256_hex("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") { return 1; }
    if (crypto.sha512_hex("abc") != "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f") { return 2; }
    return 42;
}
`

func TestSelfHostX86RotateFromSource(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "rotate.fern")
	if err := os.WriteFile(src, []byte(rotateProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm := filepath.Join(dir, "rotate.s")
	if out, err := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", asm, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(asm)
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)\n__fn_crypto____sha256_soft_blocks:\n(.*?)\.cfi_endproc`).FindSubmatch(raw)
	if body == nil {
		t.Fatal("__fn_crypto____sha256_soft_blocks not in the listing")
	}
	for _, c := range []struct{ what, re string }{
		{"rorx from a register", `rorxl \$\d+, %\w+, %\w+`},
		{"rorx from a frame slot", `rorxl \$\d+, -?\d+\(%rbp\), %\w+`},
	} {
		if !regexp.MustCompile(c.re).Match(body[1]) {
			t.Errorf("sha256's rounds have no %s", c.what)
		}
	}
	if !regexp.MustCompile(`rorxq \$\d+, `).Match(raw) {
		t.Error("sha512's 64-bit rotates have no rorxq")
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, "rotate-"+tg.target)
		if out, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, out)
		}
		argv := append(append([]string{}, tg.runner...), bin)
		cmd := exec.Command(argv[0], argv[1:]...)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("%s: exit %d, want 42 (1 = sha256, 2 = sha512)", tg.target, code)
		}
	}
}
