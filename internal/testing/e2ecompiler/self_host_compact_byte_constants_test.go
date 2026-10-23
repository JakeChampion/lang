package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Asset size must affect payload bytes, not the number of AST/SSA/IR values.
// The same driver checks the semantic boundary and the FIP allocation marker.
func TestSelfHostPackedBytesGraph(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	const source = `import "./lexer";
import "./parser";
import "./checker";
import "./embed";
import "./semsource";
import "./ssasem";
import "./ssa";
import "./ssaunits";
import "./ssarc";
import "./irtables";
import "./suspend";
import "./typeinfo";
import "./ir";
import "./irfipverify";
import "./byteconst";
import "./util";

function refused(f: ssasem.Func, block: i32, at: i32, ins: ssa.SInst): boolean {
  let b = f.graph.blocks[block];
  b = ssa.SBlock { ...b, insts: b.insts.with(at, ins) };
  let graph = ssa.SFunc { ...f.graph, blocks: f.graph.blocks.with(block, b) };
  return !ssasem.analyze(ssasem.Func { ...f, graph: graph }).ok;
}

function check(n: i32): i32 {
  let bs: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) { bs = bs.with(i, (i % 256) as u8); i = i + 1; }
  let assets = embed.Assets { root: "", names: ["blob"], contents: [bs], present: true };
  let mod = parser.parse_module(lexer.tokenize("function f(spare: i32): u8[] { return __fern_asset_bytes(\"blob\"); }"));
  let folded = embed.fold_assets(mod, assets);
  if (folded.errs.len() != 0) { return 1; }
  mod = parser.module_with_builtins_typed(checker.annotate_module(folded.module));
  let p = semsource.build_module(mod).decls[0];
  if (!p.ok) { print(p.why); return 2; }
  if (p.func.graph.nvals > 8 || !ssasem.analyze(p.func).ok) { return 3; }
  let found: i32 = 0;
  let bi: i32 = 0;
  for b in p.func.graph.blocks {
    let at: i32 = 0;
    for ins in b.insts {
      if (ins.kind_tag == ssasem.bytes_new()) {
        found = found + 1;
        if (ins.imm != n || !byteconst.valid(ins.str, n) || ins.args.len() != 0) { return 4; }
        if (!refused(p.func, bi, at, ssa.SInst { ...ins, imm: n + 1 })) { return 5; }
        if (!refused(p.func, bi, at, ssa.SInst { ...ins, args: [0] })) { return 6; }
        for text in ["256", "-1", "01", "1,", ",1", "1,,2", "a"] {
          if (!refused(p.func, bi, at, ssa.SInst { ...ins, imm: 1, str: text })) { return 7; }
        }
        let integer: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
        let byte: typeinfo.Type = typeinfo.TypeI32 { width: 8, unsigned: true, is_char: false, polymorphic: false };
        let bad_types: typeinfo.Type[] = [integer, typeinfo.TypeArray { elem: integer, view: false }, typeinfo.TypeArray { elem: byte, view: true }];
        for ty in bad_types {
          let wrong = ssasem.Func { ...p.func, result: ty, values: p.func.values.with(ins.result, ty) };
          let verdict = ssasem.analyze(wrong);
          if (verdict.ok || verdict.why != "constant byte array type") { print(verdict.why); return 8; }
        }
      }
      at = at + 1;
    }
    bi = bi + 1;
  }
  if (found != 1) { return 9; }
  let plan = ssaunits.plan(p.func, p.modes, ssaunits.no_view());
  if (!plan.ok) { print(plan.why); return 10; }
  let no_names: string[] = [];
  let lowered = ssarc.lower(p.func, p.modes, plan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index(no_names), ssaunits.no_view(), suspend.none());
  if (!lowered.ok) { print(lowered.why); return 11; }
  if (lowered.ops.len() > 32) { return 12; }
  return 0;
}

function main(): i32 {
  for n in [0, 1, 256, 1000000] {
    let code = check(n);
    if (code != 0) { print(util.i32_to_string(n)); return code; }
  }
  if (irfipverify.fresh_alloc_what(ir.op_array_literal_copy(8)) != "array literal") { return 13; }
  if (irfipverify.fresh_alloc_what(ir.op_arr_slice(8)) != "") { return 14; }
  let words = "0,127,128,255";
  if (byteconst.words(byteconst.values(words)) != words) { return 15; }
  if (byteconst.words(byteconst.values("0xff,-1,0b101")) != "255,255,5") { return 16; }
  let source: string = "";
  match (read_file(args()[1])) { Ok(text) => { source = text; }, Err(_) => { return 17; } }
  let mod = parser.module_with_builtins_typed(checker.annotate_module(parser.parse_module(lexer.tokenize(source))));
  let p = semsource.build_module(mod).decls[0];
  if (!p.ok || p.func.graph.nvals > 8) { return 18; }
  let found: i32 = 0;
  for b in p.func.graph.blocks {
    for ins in b.insts {
      if (ins.kind_tag == ssasem.bytes_new() && ins.imm == 10001 && ins.args.len() == 0) { found = found + 1; }
    }
  }
  if (found != 1) { return 19; }
  return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "packed_bytes.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "packed_bytes.fern", "packed-bytes")
	literal := filepath.Join(dir, "ordinary.fern")
	if err := os.WriteFile(literal, []byte("function f(): u8[] { return ["+strings.Repeat("17u8,", 10000)+"17u8]; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runX86_64Bin(runner, bin, literal).CombinedOutput(); err != nil {
		t.Fatalf("packed byte graph: %v\n%s", err, out)
	}
}

func TestSelfHostCompactByteConstantsExecution(t *testing.T) {
	cli := buildRuntimeCLI(t)
	assets := t.TempDir()
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for name, bytes := range map[string][]byte{"empty": {}, "one": {255}, "all": all} {
		if err := os.WriteFile(filepath.Join(assets, name), bytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cli.flags = []string{"-embed", assets}
	const program = `struct Box { bytes: u8[] }
const DATA: u8[] = __fern_asset_bytes("all");
const BOX: Box = Box { bytes: [0u8, 128u8, 255u8] };
@noinline function digit(n: i32): u8 { print("digit"); return n as u8; }
function main(): i32 {
  if (__fern_asset_bytes("empty").len() != 0 || __fern_asset_bytes("one")[0] != 255u8) { return 1; }
  let bytes: u8[] = __fern_asset_bytes("all");
  let i: i32 = 0;
  while (i < 256) { if (bytes[i] != i as u8 || DATA[i] != i as u8) { return 2; } i = i + 1; }
  let changed = bytes.with(0, 99u8).append(17u8);
  if (bytes[0] != 0u8 || DATA[0] != 0u8 || changed[0] != 99u8 || changed.len() != 257) { return 3; }
  let nested: u8[][] = [BOX.bytes, [4u8, 5u8]];
  let alias = nested[0].with(2, 7u8);
  if (alias[2] != 7u8 || BOX.bytes[2] != 255u8 || nested[0][2] != 255u8) { return 4; }
  let mixed: u8[] = [digit(1), 128u8, digit(255)];
  if (mixed[0] != 1u8 || mixed[1] != 128u8 || mixed[2] != 255u8) { return 5; }
  return 0;
}
`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := buildLangBinForInterp(t)
	for name, cmd := range map[string]*exec.Cmd{
		"oracle":    exec.Command(oracle, "-interp", "-embed", assets, path),
		"self-host": runX86_64Bin(cli.runner, cli.bin, "-interp", "-embed", assets, path, cli.stdlib),
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil || string(out) != "digit\ndigit\n" {
				t.Fatalf("interpreter: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
		t.Run(target+"/raw-floor", func(t *testing.T) {
			stderr, code := cli.exitOf(t, `@noinline function fresh(value: i32): u8[] {
  let bytes: u8[] = [0u8, 128u8, 255u8];
  __raw_store8((bytes as usize) + 8, 0, value);
  return bytes;
}
function main(): i32 {
  let a = fresh(7); let b = fresh(9);
  if (a[0] != 7u8 || b[0] != 9u8 || a[1] != 128u8 || b[2] != 255u8) { return 1; }
  return 0;
}`, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("raw-floor literal: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
