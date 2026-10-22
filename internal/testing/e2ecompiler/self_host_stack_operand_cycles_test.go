package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostStackOperandMoveCycles(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	module, err := os.ReadFile(langSrcAbs(t, "compiler/asm_ir.fern"))
	if err != nil {
		t.Fatal(err)
	}
	source := append(module, []byte(stackOperandCycleDriver)...)
	if err := os.WriteFile(filepath.Join(dir, "operand_cycles.fern"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "operand_cycles.fern", "operand_cycles")
	assembly, err := runX86_64Bin(runner, driver).Output()
	if err != nil {
		t.Fatalf("operand adapter: %v", err)
	}
	bin := buildBin(t, gcc, t.TempDir(), "operand_cycles", string(assembly))
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("operand values after parallel moves: %v\n%s", err, out)
	}
}

const stackOperandCycleDriver = `
function probe(homes: i32[]): boolean {
  let ins = ssa.SInst { kind_tag: 0, result: 0, args: [3,2,1,0], imm: 0, str: "" };
  let destinations = ["%rax", "%rsi", "%rdi", "%r8"];
  let moves = ssa_arg_moves(ins, homes, destinations, asmcore.new_state());
  if (!moves.ok) { return false; }
  write("    movq $11, %rax\n    movq $22, %rsi\n    movq $33, %rdi\n    movq $44, %r8\n    movq $55, -8(%rbp)\n");
  for text in moves.text { write(text); }
  let k = 0;
  while (k < 4) {
    let expected = 11 * (homes[k] + 1);
    if (homes[k] < 0) { expected = 55; }
    write("    cmpq $" + util.i32_to_string(expected) + ", " + destinations[k] + "\n    jne .Lbad\n");
    k = k + 1;
  }
  return true;
}
function main(): i32 {
  write(".text\n.globl _start\n_start:\n    movq %rsp, %rbp\n    subq $16, %rsp\n");
  for a in [0-2,0,1,2,3] {
    for b in [0-2,0,1,2,3] {
      for c in [0-2,0,1,2,3] {
        for d in [0-2,0,1,2,3] {
          if (!probe([a,b,c,d])) { return 1; }
        }
      }
    }
  }
  write("    movq $0, %rdi\n    movq $60, %rax\n    syscall\n.Lbad:\n    movq $1, %rdi\n    movq $60, %rax\n    syscall\n");
  return 0;
}
`
