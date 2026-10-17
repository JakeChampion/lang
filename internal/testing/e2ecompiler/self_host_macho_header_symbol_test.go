package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostMachOHeaderSymbol(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const source = `import "./arm64_native";
function main(): i32 {
  let source = ".text\n.globl _main\n_main:\n    adrp x0, __mh_execute_header@PAGE\n    add x0, x0, __mh_execute_header@PAGEOFF\n    ret\n.data\n.p2align 3\nheader_ptr: .quad __mh_execute_header\n";
  let tv = 4294983680i64;
  let dv = 4295000064i64;
  let elf = arm64_native.arm64_gas_link(arm64_native.arm64_gas_program(source), tv, dv);
  if (elf.unknown.len() == 0) { return 1; }
  let image = arm64_native.arm64_gas_link_macho(arm64_native.arm64_gas_program(source), tv, dv);
  if (image.unknown.len() != 0) { return 2; }
  let want = [224, 255, 255, 144, 0, 0, 0, 145, 192, 3, 95, 214];
  if (image.asm.text.len() != want.len()) { return 3; }
  let i = 0;
  while (i < want.len()) {
    if (image.asm.text[i] as i32 != want[i]) { return 4; }
    i = i + 1;
  }
  let ptr = [0, 0, 0, 0, 1, 0, 0, 0];
  if (image.data.len() != ptr.len()) { return 5; }
  i = 0;
  while (i < ptr.len()) {
    if (image.data[i] != ptr[i]) { return 6; }
    i = i + 1;
  }
  let rebases = arm64_native.arm64_gas_rebase_offs(image);
  if (rebases.len() != 1 || rebases[0] != 0) { return 7; }
  return 42;
}`
	if err := os.WriteFile(filepath.Join(dir, "macho_header.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "macho_header.fern", "macho_header")
	cmd := runX86_64Bin(runner, bin)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 42 {
		t.Fatalf("header link: %v\n%s", cmd.ProcessState, out)
	}
}
