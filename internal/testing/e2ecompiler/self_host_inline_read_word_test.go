package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// count passes a buffer handle read out of its parameter to buf_len. Spliced
// into main, it would let main release w, and run Blk's drop, before buf_len
// reads the buffer. The drop pushes a marker byte, so the count would be 2.
const inlineReadWordProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk {
  function drop(self: Blk): void { buf_push_byte(self.h, 9); }
}
struct W { blk: Blk, tag: i32 }
function (w: W) count(): i32 { return buf_len(w.blk.h); }
@noinline function make(h: usize): W { return W { blk: Blk { h: h }, tag: 1 }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let w: W = make(h);
  let n: i32 = w.count();
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

// A tiny body that passes a word read out of memory to a call stays out of
// line, so its parameter lives until the call returns.
const inlineReadDirectWordProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk {
  function drop(self: Blk): void { buf_push_byte(self.h, 9); }
}
function count(b: Blk): i32 { return buf_len(b.h); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

const inlineOpaqueWordProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
@noinline function read_handle(b: Blk): usize { return b.h; }
function count(b: Blk): i32 { return buf_len(read_handle(b)); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

const inlineCastWordProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
function count(b: Blk): i32 { let wide: u64 = b.h as u64; return buf_len(wide as usize); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

const inlineU64GetterProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
@noinline function read_handle(b: Blk): u64 { return b.h as u64; }
function count(b: Blk): i32 { return buf_len(read_handle(b) as usize); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

const inlineU64FieldProgram = `import "core/mem";
struct Blk { h: u64 }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h as usize, 9); } }
function count(b: Blk): i32 { let wide: u64 = b.h as u64; return buf_len(wide as usize); }
@noinline function make(h: usize): Blk { return Blk { h: h as u64 }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

const inlineOpaqueConsumerProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
@noinline function read_handle(b: Blk): u64 { return b.h as u64; }
@noinline function consume(h: u64): i32 { return buf_len(h as usize); }
function count(b: Blk): i32 { return consume(read_handle(b)); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

const inlineWrappedWordProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
enum BorrowedWord { Word(usize), Empty }
@noinline function read_handle(b: Blk): BorrowedWord { return Word(b.h); }
@noinline function consume(h: BorrowedWord): i32 { match (h) { Word(v) => { return buf_len(v); }, Empty => { return -1; } } }
function count(b: Blk): i32 { return consume(read_handle(b)); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let n: i32 = count(b);
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

func TestSelfHostInlineKeepsReadWordOwner(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"nested-tiny", inlineReadWordProgram},
		{"direct-once", inlineReadDirectWordProgram},
		{"opaque-getter", inlineOpaqueWordProgram},
		{"cast-word", inlineCastWordProgram},
		{"opaque-consumer", inlineOpaqueConsumerProgram},
		{"wrapped-getter", inlineWrappedWordProgram},
		{"u64-field", inlineU64FieldProgram},
		{"u64-getter", inlineU64GetterProgram},
		{"nested-forced", strings.Replace(inlineReadWordProgram, "function (w: W) count()", "@inline function (w: W) count()", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {

			for _, target := range selfHostFusionTargets {
				for _, env := range [][]string{nil, {"FERN_SANITIZE=1"}} {
					name := target
					if env != nil {
						name += "/sanitize"
					}
					t.Run(name, func(t *testing.T) {
						bin := e2eharness.CompileSelfHostSource(t, target, tc.source, env)
						out, err := runScaleTarget(t, target, bin).CombinedOutput()
						if err != nil || !strings.HasPrefix(string(out), "ok") {
							t.Fatalf("%v:\n%s", err, out)
						}
					})
				}
			}

		})
	}
}

const inlineOpaqueReturnProgram = `import "./ssa";
import "./ssasem";
import "./seminline";
import "./semrecords";
import "./typeinfo";
function int_type(): typeinfo.Type { return typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false }; }
function word_type(): typeinfo.Type { return typeinfo.TypeI32 { width: typeinfo.width_ptr(), unsigned: true, is_char: false, polymorphic: false }; }
function byte_type(): typeinfo.Type { return typeinfo.TypeI32 { width: 8, unsigned: true, is_char: false, polymorphic: false }; }
function op(k: i32, r: i32, args: i32[], imm: i32, name: string): ssa.SInst { return ssa.SInst { kind_tag: k, result: r, args: args, imm: imm, str: name }; }
function func(name: string, params: typeinfo.Type[], types: typeinfo.Type[], blocks: ssa.SBlock[], result: typeinfo.Type, calls: ssasem.Contract[]): ssasem.Func {
 return ssasem.Func { graph: ssa.SFunc { name: name, nparams: params.len(), nvals: types.len(), entry: 0, takes_env: false, blocks: blocks }, values: types, params: params, result: result, records: semrecords.no_records(), enums: [], calls: calls, envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: false, dbg_vals: [], dbg_names: [], source_positions: ssasem.no_source() };
}
function block(ops: ssa.SInst[], r: i32): ssa.SBlock { return ssa.SBlock { id: 0, preds: [], insts: ops, term: ssa.STerm { kind_tag: 1, value: r, cond: 0, target: 0, t: 0, f: 0 } }; }
function main(): i32 {
 let arr: typeinfo.Type = typeinfo.TypeArray { elem: byte_type(), view: false };
 let contract: ssasem.Contract = ssasem.Contract { name: "external_word", params: [arr], modes: [ssasem.borrow_mode()], result: word_type() };
 let consume: ssasem.Contract = ssasem.Contract { name: "use_word", params: [word_type()], modes: [ssasem.value_mode()], result: int_type() };
 let wrap: ssasem.Contract = ssasem.Contract { name: "wrapper", params: [arr], modes: [ssasem.borrow_mode()], result: int_type() };
 let row: i32 = 0;
 while (row < 2) {
  let external: ssasem.Func = func("external_word", [], [], [], word_type(), []);
  if (row == 1) { external = func("external_word", [arr], [arr, word_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(1, 1, [], 7, "")], 1)], word_type(), []); }
  let wrapper = func("wrapper", [arr], [arr, word_type(), int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "external_word"), op(ssasem.call(), 2, [1], 0, "use_word")], 2)], int_type(), [contract, consume]);
  let entry = func("entry", [arr], [arr, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "wrapper")], 1)], int_type(), [wrap]);
  let out = seminline.inline_leaves([external, wrapper, entry], ["external_word", "wrapper", "entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
  let calls: i32 = 0;
  for b in out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "wrapper") { calls = calls + 1; } } }
  if (row == 0 && calls != 1) { print("opaque wrapper inlined"); return 77; }
  if (row == 1 && calls != 0) { print("known independent result blocked"); return 78; }
  row = row + 1;
 }
 let words: typeinfo.Type = typeinfo.TypeArray { elem: typeinfo.TypeI32 { width: 64, unsigned: true, is_char: false, polymorphic: false }, view: false };
 let index: typeinfo.Type = int_type();
 let word: typeinfo.Type = typeinfo.TypeI32 { width: 64, unsigned: true, is_char: false, polymorphic: false };
 let scan = ssasem.Contract { name: "__count_below_u64", params: [words, word], modes: [ssasem.borrow_mode(), ssasem.value_mode()], result: index };
 let use_index = ssasem.Contract { name: "use_index", params: [index], modes: [ssasem.value_mode()], result: index };
 let wrapped = ssasem.Contract { name: "scan_wrapper", params: [words], modes: [ssasem.borrow_mode()], result: index };
 let kernel = func("__count_below_u64", [], [], [], index, []);
 let wrapper = func("scan_wrapper", [words], [words, word, index, index], [block([op(ssasem.param(), 0, [], 0, ""), op(1, 1, [], 7, ""), op(ssasem.call(), 2, [0, 1], 0, "__count_below_u64"), op(ssasem.call(), 3, [2], 0, "use_index")], 3)], index, [scan, use_index]);
 let entry = func("scan_entry", [words], [words, index], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "scan_wrapper")], 1)], index, [wrapped]);
 let out = seminline.inline_leaves([kernel, wrapper, entry], ["__count_below_u64", "scan_wrapper", "scan_entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
 for b in out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "scan_wrapper") { print("copied index blocked"); return 79; } } }
 print("ok"); return 0;
}
`

func TestSelfHostInlineOpaqueReturnSummary(t *testing.T) {
	gcc, runner := e2eharness.X86_64Tooling(t)
	dir := t.TempDir()
	e2eharness.CopySelfHostDriver(t, dir, "seminline.fern")
	if err := os.WriteFile(filepath.Join(dir, "opaque-return-summary.fern"), []byte(inlineOpaqueReturnProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := e2eharness.BuildSelfHostBin(t, gcc, dir, "opaque-return-summary.fern", "opaque-return-summary")
	if out, err := e2eharness.RunX86_64Bin(runner, bin).CombinedOutput(); err != nil || !strings.HasPrefix(string(out), "ok\n") {
		t.Fatalf("opaque return summary: %v\n%s", err, out)
	}
}
