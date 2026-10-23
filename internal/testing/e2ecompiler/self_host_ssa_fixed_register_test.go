package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSSARegisterConstraints(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const src = `import "./ssa"; import "./ssafixed"; import "./util";
function inst(kind: i32, result: i32, args: i32[], imm: i32): ssa.SInst {
  return ssa.SInst { kind_tag: kind, result: result, args: args, imm: imm, str: "add" };
}
function graph(): ssa.SFunc {
  let code = [inst(1, 0, [], 40), inst(1, 1, [], 2), inst(9, 2, [0, 1], 32)];
  let term = ssa.STerm { kind_tag: 1, cond: 0, target: 0, t: 0, f: 0, value: 2 };
  let block = ssa.SBlock { id: 0, insts: code, term: term, preds: [] };
  return ssa.SFunc { name: "case", nparams: 0, nvals: 3, blocks: [block], entry: 0, takes_env: false };
}
function pressure_graph(): ssa.SFunc {
  let f = graph();
  let code = [inst(1, 0, [], 1), inst(1, 1, [], 10), inst(1, 2, [], 30)];
  let i = 3;
  while (i < 10) { code = code.append(inst(41, -1, [1, 1], 0)); i = i + 1; }
  code = code.append(inst(41, -1, [0, 1], 0));
  code = code.append(inst(41, -1, [2, 1], 0));
  code = code.append(inst(9, 3, [1, 2], 32));
  let term = ssa.STerm { ...f.blocks[0].term, value: 3 };
  return ssa.SFunc { ...f, nvals: 4, blocks: [ssa.SBlock { ...f.blocks[0], insts: code, term: term }] };
}
function address(home: i32): i32 {
  if (home >= 0) { return home; }
  return 16 + ssa.spill_slot(home);
}
// Execute the assigned homes, overwriting the fixed register at the named
// positions before operands are read. Spilled values use separate memory.
function execute(f: ssa.SFunc, a: ssa.Alloc, first: i32, masks: i32[]): i32 {
  let memory = __alloc_i32(32);
  let pos = 0;
  for ins in f.blocks[0].insts {
    if (masks[pos] != 0) { memory = memory.with(first, -999); }
    let value = ins.imm;
    if (ins.kind_tag == 9) {
      value = memory[address(a.reg_of[ins.args[0]])] + memory[address(a.reg_of[ins.args[1]])];
    }
    if (ins.result >= 0) { memory = memory.with(address(a.reg_of[ins.result]), value); }
    pos = pos + 1;
  }
  if (masks[pos] != 0) { memory = memory.with(first, -999); }
  return memory[address(a.reg_of[f.blocks[0].term.value])];
}
function main(): i32 {
  let f = graph();
  let av = args();
  if (av.len() > 1) {
    let fixed = ssafixed.table(0, 1, []);
    if (av[1] == "register-range") { fixed = ssafixed.table(1, 1, [0, 0, 0, 0]); }
    if (av[1] == "mask-range") { fixed = ssafixed.table(0, 1, [2, 0, 0, 0]); }
    let invalid = ssa.regalloc_linear_fixed(f, 1, 0, 0, false, [], [], [], 0, 0, fixed, false);
    print(util.i32_to_string(invalid.reg_of.len()));
    return 1;
  }
  let count = 0;
  let nregs = 1;
  while (nregs <= 3) {
    let saved = 0;
    while (saved <= 1) {
      let pattern = 0;
      while (pattern < 16) {
        let masks = [pattern & 1, (pattern >> 1) & 1, (pattern >> 2) & 1, (pattern >> 3) & 1];
        let fixed = ssafixed.table(nregs - 1, 1, masks);
        let pref = [nregs - 1, nregs - 1, nregs - 1];
        let a = ssa.regalloc_linear_fixed(f, nregs, saved, 0, false, [], pref, [], 0, 0, fixed, false);
        if (execute(f, a, nregs - 1, masks) != 42) {
          eprint("bad assigned execution: registers=" + util.i32_to_string(nregs) + " pattern=" + util.i32_to_string(pattern));
          return 2;
        }
        count = count + 1;
        pattern = pattern + 1;
      }
      saved = saved + 1;
    }
    nregs = nregs + 1;
  }
  // The low-weight value in register 1 dies before its clobber. A new,
  // longer-lived value must not acquire that register by evicting it.
  let pressure = pressure_graph();
  let masks = __alloc_i32(14).with(11, 1);
  let fixed = ssafixed.table(1, 1, masks);
  let a = ssa.regalloc_linear_fixed(pressure, 2, 0, 0, false, [], [1, 0, 1, 0], [], 0, 0, fixed, false);
  if (execute(pressure, a, 1, masks) != 40) { return 3; }
  count = count + 1;
  print("fixed-register allocation: " + util.i32_to_string(count) + " executions agree");
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "fixed_registers.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "fixed_registers.fern", "fixed_registers")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil || !strings.Contains(string(out), "97 executions agree") {
		t.Fatalf("assigned execution: %v\n%s", err, out)
	}
	for mode, diagnostic := range map[string]string{
		"positions":      "incorrect position count",
		"register-range": "range outside caller registers",
		"mask-range":     "mask outside register range",
	} {
		t.Run(mode, func(t *testing.T) {
			out, err := runX86_64Bin(runner, bin, mode).CombinedOutput()
			if err == nil || !strings.Contains(string(out), diagnostic) {
				t.Fatalf("want refusal %q, got %v\n%s", diagnostic, err, out)
			}
		})
	}
}
