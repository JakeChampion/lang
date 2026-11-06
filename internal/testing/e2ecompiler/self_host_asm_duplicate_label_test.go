package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GNU as refuses a second definition of a label, and so does each in-process
// assembler. A text, a data and a bss label each count.
func TestSelfHostAssemblersRefuseDuplicateLabels(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_refusals_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_refusals_run.fern", "asm_refusals_run")
	cases := []struct{ isa, name, src string }{
		{"x86", "text", ".text\nf:\n    ret\nf:\n    ret\n"},
		{"x86", "data", ".data\nd:\n    .quad 1\nd:\n    .quad 2\n.text\nf:\n    ret\n"},
		{"x86", "bss", ".bss\nb:\n    .skip 8\nb:\n    .skip 8\n.text\nf:\n    ret\n"},
		{"arm64", "text", ".text\nf:\n    ret\nf:\n    ret\n"},
		{"arm64", "data", ".data\nd:\n    .quad 1\nd:\n    .quad 2\n.text\nf:\n    ret\n"},
		{"arm64", "bss", ".bss\nb:\n    .skip 8\nb:\n    .skip 8\n.text\nf:\n    ret\n"},
	}
	for _, c := range cases {
		t.Run(c.isa+"/"+c.name, func(t *testing.T) {
			label := strings.TrimSuffix(strings.Split(c.src, "\n")[1], ":")
			out, err := runX86_64Bin(runner, driver, c.isa, mustWrite(t, dir, c.isa+"-"+c.name+".s", c.src)).Output()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if want := "label " + label + " defined twice\n"; !strings.Contains(string(out), want) {
				t.Errorf("refusals %q, want %q", out, want)
			}
			single := strings.Replace(c.src, label+":\n", "", 1)
			out, err = runX86_64Bin(runner, driver, c.isa, mustWrite(t, dir, c.isa+"-"+c.name+"-once.s", single)).Output()
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if strings.Contains(string(out), "defined twice") {
				t.Errorf("one definition of %s is refused: %q", label, out)
			}
		})
	}
}

// A program function with the name of a runtime routine would be a second
// definition of the routine's symbol, so the build stops and names it.
func TestSelfHostProgramCannotTakeARuntimeName(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(`@noinline function __fern_print_str(s: string): i32 {
  return s.len() + 7;
}

function main(): i32 {
  print("hi");
  return __fern_print_str("abc");
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "prog"), src).CombinedOutput()
			if err == nil {
				t.Fatal("the build succeeded with two definitions of __fern_print_str")
			}
			if want := "the program defines function \"__fern_print_str\", which is the name of a runtime routine; rename it"; !strings.Contains(string(out), want) {
				t.Errorf("want %q in:\n%s", want, out)
			}
		})
	}
}
