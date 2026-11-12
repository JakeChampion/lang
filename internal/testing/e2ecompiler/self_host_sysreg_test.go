package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sysregArm64Src = `function main(): i32 {
  unsafe {
    let el = __sysreg_read("currentel");
    __sysreg_write("vbar_el1", 0x80000i64);
    let v = __sysreg_read("s3_0_c12_c0_0");
    __sysreg_write("daif", el);
    return (v + el) as i32;
  }
}
`

const sysregX86Src = `function main(): i32 {
  unsafe {
    let cr3 = __sysreg_read("cr3");
    __sysreg_write("cr3", cr3);
    let efer = __sysreg_read("efer");
    __sysreg_write("msr_c0000100", efer);
    return (efer >> 8i64 & 1i64) as i32;
  }
}
`

// sysregRows reads the arm64 register table of compiler/sysreg.fern, so the
// A read of the time-stamp counter is rdtsc, because a VMM can give a stale
// value for rdmsr of the same register. A write stays wrmsr.
func TestSelfHostSysregTscIsRdtsc(t *testing.T) {
	cli := buildSelfHostCLI(t)
	text := emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, "function main(): i32 {\n  unsafe {\n    __sysreg_write(\"tsc\", 5i64);\n    return __sysreg_read(\"tsc\") as i32;\n  }\n}\n")
	wantInOrder(t, "x86-64-kernel", text, []string{"    movl $16, %ecx\n    wrmsr\n", "    rdtsc\n    shlq $32, %rdx\n    orq %rdx, %rax\n"})
	if strings.Contains(text, "rdmsr") {
		t.Errorf("the image reads the time-stamp counter with rdmsr")
	}
}

// test assembles every row that the compiler accepts.
func sysregRows(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, "compiler/sysreg.fern"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`Arm64Reg \{ name: "([a-z0-9_]+)",.* access: (READ|WRITE|BOTH) \}`)
	rows := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(src), -1) {
		rows[m[1]] = m[2]
	}
	if len(rows) < 40 {
		t.Fatalf("read only %d rows of the arm64 register table", len(rows))
	}
	return rows
}

func TestSelfHostSysregArm64RowsMatchGas(t *testing.T) {
	var cases []string
	for name, access := range sysregRows(t) {
		if access != "WRITE" {
			cases = append(cases, "mrs x9, "+name)
		}
		if access != "READ" {
			cases = append(cases, "msr "+name+", x9")
		}
	}
	cases = append(cases, "mrs x3, s3_0_c12_c0_0", "msr s2_7_c15_c15_7, x3", "msr daifset, #2", "msr daifclr, #2", "msr daifset, #15", "msr daifclr, #1")
	compareArm64Cases(t, cases)

	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	for _, c := range []string{"msr currentel, x9", "mrs x9, icc_eoir1_el1", "mrs x9, s4_0_c0_c0_0", "mrs x9, s3_8_c0_c0_0", "mrs x9, s3_0_c16_c0_0", "msr daifset, #16", "msr daifset, x9"} {
		if len(refusalsFor(t, bin, runner, ".text\n_start:\n    "+c+"\n")) == 0 {
			t.Errorf("%q: the self-host assembler accepts it", c)
		}
	}
}

func TestSelfHostSysregX86MovesMatchGas(t *testing.T) {
	compareFormCases(t, []string{
		"movq %cr0, %rax", "movq %cr2, %rbx", "movq %cr3, %r9", "movq %cr4, %rdx", "movq %cr8, %rax", "movq %cr8, %r15",
		"movq %rax, %cr0", "movq %r10, %cr3", "movq %rcx, %cr4", "movq %r8, %cr8",
		"rdmsr", "wrmsr",
	})
}

func emitKernelAsm(t *testing.T, cli *selfHostCLI, target, ram, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	asmPath := filepath.Join(dir, "kernel.s")
	if msg, err := cli.compileCmd(target, asmPath, path, "-ram", ram, "-emit", "asm").CombinedOutput(); err != nil {
		t.Fatalf("%s: emit: %v\n%s", target, err, msg)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := cli.compileCmd(target, filepath.Join(dir, "kernel.elf"), path, "-ram", ram).CombinedOutput(); err != nil {
		t.Fatalf("%s: build: %v\n%s", target, err, msg)
	}
	return string(asm)
}

func wantInOrder(t *testing.T, target, text string, wants []string) {
	t.Helper()
	last := 0
	for _, want := range wants {
		i := strings.Index(text[last:], want)
		if i < 0 {
			t.Fatalf("%s: no %q after offset %d in:\n%s", target, want, last, text)
		}
		last += i + len(want)
	}
}

func TestSelfHostSysregOps(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wantInOrder(t, "arm64-kernel", emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, sysregArm64Src), []string{
		"    mrs x0, currentel\n",
		"    msr vbar_el1, x9\n    isb\n",
		"    mrs x0, s3_0_c12_c0_0\n",
		"    msr daif, x9\n    isb\n",
	})
	wantInOrder(t, "x86-64-kernel", emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, sysregX86Src), []string{
		"    movq %cr3, %rax\n",
		"    movq %rax, %cr3\n",
		"    movl $3221225600, %ecx\n    rdmsr\n    shlq $32, %rdx\n    orq %rdx, %rax\n",
		"    movq %rax, %rdx\n    shrq $32, %rdx\n    movl $3221225728, %ecx\n    wrmsr\n",
	})
}

func TestSelfHostSysregNeedsAKernel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(sysregArm64Src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		msg, err := cli.compileCmd(target, filepath.Join(dir, "out-"+target), src).CombinedOutput()
		for _, op := range []string{"__sysreg_read", "__sysreg_write"} {
			if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), op) {
				t.Errorf("%s: err %v; want E066 naming %s in:\n%s", target, err, op, msg)
			}
		}
	}
}

func TestSelfHostSysregOfAnotherISA(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for _, c := range []struct{ target, ram, reg, isa string }{
		{"arm64-kernel", kernelRAM, "cr3", "arm64"},
		{"x86-64-kernel", kernelX86RAM, "vbar_el1", "x86-64"},
	} {
		src := filepath.Join(dir, c.isa+".fern")
		prog := "function main(): i32 {\n  unsafe {\n    return __sysreg_read(\"" + c.reg + "\") as i32;\n  }\n}\n"
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		msg, err := cli.compileCmd(c.target, filepath.Join(dir, c.isa+".elf"), src, "-ram", c.ram).CombinedOutput()
		want := "the system register " + c.reg + " does not exist on " + c.isa
		if err == nil || !strings.Contains(string(msg), want) {
			t.Errorf("%s: err %v; want %q in:\n%s", c.target, err, want, msg)
		}
	}
}

func TestSelfHostSysregNameIsALiteral(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"not a literal":         "let r = \"vbar_el1\";\n    return __sysreg_read(r) as i32;",
		"unknown register":      "return __sysreg_read(\"vbar_el9\") as i32;",
		"write a read-only one": "__sysreg_write(\"currentel\", 0i64);\n    return 0;",
		"read a write-only one": "return __sysreg_read(\"icc_eoir1_el1\") as i32;",
	} {
		src := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".fern")
		prog := "function main(): i32 {\n  unsafe {\n    " + body + "\n  }\n}\n"
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		msg, err := cli.compileCmd("arm64-kernel", filepath.Join(dir, "out.elf"), src, "-ram", kernelRAM).CombinedOutput()
		if err == nil || !strings.Contains(string(msg), "E038") || !strings.Contains(string(msg), "names a system register") {
			t.Errorf("%s: err %v; want E038 in:\n%s", name, err, msg)
		}
	}
	const variable = "function main(): i32 {\n  unsafe {\n    let r = \"vbar_el1\";\n    return __sysreg_read(r) as i32;\n  }\n}\n"
	if got := goCheckerCodes(t, dir, variable); len(got) != 1 || got[0] != "E038" {
		t.Errorf("Go checker codes = %v, want [E038]", got)
	}
}
