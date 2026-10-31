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
		{"parent-owner", inlineParentOwnerProgram},
		{"parent-wide-owner", inlineParentWideOwnerProgram},
		{"parent-closure-owner", inlineParentClosureOwnerProgram},
		{"unused-finalizer-argument", inlineUnusedFinalizerArgumentProgram},
		{"unused-record-owner", inlineUnusedRecordWordOwnerProgram},
		{"unused-tuple-owner", inlineUnusedTupleWordOwnerProgram},
		{"managed-last-read", inlineManagedLastReadProgram},
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

const inlineParentOwnerProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
struct Owner { h: usize, keep: Blk }
function count(o: Owner): i32 { return buf_len(o.h); }
@noinline function make(h: usize): Owner { return Owner { h: h, keep: Blk { h: h } }; }
function main(): i32 {
 let h: usize = buf_new(8);
 buf_push_byte(h, 1);
 let o: Owner = make(h);
 let n: i32 = count(o);
 buf_free(h);
 if (n != 1) { return n; }
 print("ok");
 return 0;
}
`

const inlineManagedLastReadProgram = `@noinline function make_bytes(): u8[] {
 let xs: u8[] = [];
 for i in 0..8 { xs = xs.append((i + 1) as u8); }
 return xs;
}
@inline function read_last(bs: [u8]): u32 {
 return bs[0] as u32 | bs[1] as u32 << 8 | bs[2] as u32 << 16 | bs[3] as u32 << 24;
}
@noinline function exercise(): u32 {
 let xs: u8[] = make_bytes();
 return read_last(xs[0:4]);
}
function main(): i32 {
 if (exercise() != 67305985u32) { return 86; }
 print("ok"); return 0;
}
`

const inlineParentWideOwnerProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
struct Owner { h: u64, keep: Blk }
function count(o: Owner): i32 { return buf_len(o.h as usize); }
@noinline function make(h: usize): Owner { return Owner { h: h as u64, keep: Blk { h: h } }; }
function main(): i32 {
 let h: usize = buf_new(8);
 buf_push_byte(h, 1);
 let o: Owner = make(h);
 let n: i32 = count(o);
 buf_free(h);
 if (n != 1) { return n; }
 print("ok");
 return 0;
}
`

const inlineOpaqueReturnProgram = `import "./ssa";
import "./ssasem";
import "./seminline";
import "./semrecords";
import "./typeinfo";
import "./ssabytes";
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
 let bytes: typeinfo.Type = typeinfo.TypeArray { elem: byte_type(), view: false };
 let get_bytes = ssasem.Contract { name: "external_bytes", params: [bytes], modes: [ssasem.borrow_mode()], result: bytes };
 let use_bytes = ssasem.Contract { name: "use_bytes", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
 let wrap_bytes = ssasem.Contract { name: "bytes_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
 let opaque_bytes = func("external_bytes", [], [], [], bytes, []);
 let bytes_wrapper = func("bytes_wrapper", [bytes], [bytes, bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "external_bytes"), op(ssasem.call(), 2, [1], 0, "use_bytes")], 2)], int_type(), [get_bytes, use_bytes]);
 let bytes_entry = func("bytes_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "bytes_wrapper")], 1)], int_type(), [wrap_bytes]);
 let bytes_out = seminline.inline_leaves([opaque_bytes, bytes_wrapper, bytes_entry], ["external_bytes", "bytes_wrapper", "bytes_entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
 for b in bytes_out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "bytes_wrapper") { print("managed bytes blocked"); return 80; } } }
 let loaded: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: true, is_char: false, polymorphic: false };
 let load_name: string = ssabytes.word_name(4);
 let load = ssasem.Contract { name: load_name, params: [bytes, int_type()], modes: [ssasem.borrow_mode(), ssasem.value_mode()], result: loaded };
 let use_loaded = ssasem.Contract { name: "use_loaded", params: [loaded], modes: [ssasem.value_mode()], result: int_type() };
 let wrap_loaded = ssasem.Contract { name: "word_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
 let word_load = func(load_name, [], [], [], loaded, []);
 let word_wrapper = func("word_wrapper", [bytes], [bytes, int_type(), loaded, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(1, 1, [], 0, ""), op(ssasem.call(), 2, [0, 1], 0, load_name), op(ssasem.call(), 3, [2], 0, "use_loaded")], 3)], int_type(), [load, use_loaded]);
 let word_entry = func("word_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "word_wrapper")], 1)], int_type(), [wrap_loaded]);
 let word_out = seminline.inline_leaves([word_load, word_wrapper, word_entry], [load_name, "word_wrapper", "word_entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
 for b in word_out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "word_wrapper") { print("copied byte word blocked"); return 81; } } }
 let row_load: i32 = 0;
 while (row_load < 6) {
  let owner: typeinfo.Type = bytes;
  let raw_name: string = "__raw_arr_ptr";
  if (row_load == 2) { owner = typeinfo.TypeString { tag: 0 }; raw_name = "__raw_data"; }
  if (row_load == 5) { owner = typeinfo.TypeArray { elem: word_type(), view: false }; }
  let mode: i32 = ssasem.borrow_mode();
  let load_ops: ssa.SInst[] = [op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, raw_name), op(1, 2, [], 0, ""), op(9, 3, [1, 2], 0, "+"), op(ssasem.call(), 4, [3], 0, "__load_i32")];
  let load_types: typeinfo.Type[] = [owner, word_type(), int_type(), word_type(), int_type()];
  let load_ret: i32 = 4;
  if (row_load == 1) {
   owner = word_type();
   mode = ssasem.value_mode();
   load_ops = [op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "__load_i32")];
   load_types = [word_type(), int_type()];
   load_ret = 1;
  }
  let raw = ssasem.Contract { name: raw_name, params: [owner], modes: [ssasem.borrow_mode()], result: word_type() };
  let read = ssasem.Contract { name: "__load_i32", params: [word_type()], modes: [ssasem.value_mode()], result: int_type() };
  let get = ssasem.Contract { name: "load_value", params: [owner], modes: [mode], result: int_type() };
  let consume_load = ssasem.Contract { name: "use_loaded", params: [int_type()], modes: [ssasem.value_mode()], result: int_type() };
  let wrap = ssasem.Contract { name: "load_wrapper", params: [owner], modes: [mode], result: int_type() };
  let getter = func("load_value", [owner], load_types, [block(load_ops, load_ret)], int_type(), [raw, read]);
  if (row_load == 3) { getter = ssasem.Func { ...getter, shadows: ["__raw_arr_ptr"] }; }
  if (row_load == 4) { getter = ssasem.Func { ...getter, shadows: ["__load_i32"] }; }
  let wrapper = func("load_wrapper", [owner], [owner, int_type(), int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "load_value"), op(ssasem.call(), 2, [1], 0, "use_loaded")], 2)], int_type(), [get, consume_load]);
  let entry = func("load_entry", [owner], [owner, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "load_wrapper")], 1)], int_type(), [wrap]);
  let out = seminline.inline_leaves([getter, wrapper, entry], ["load_value", "load_wrapper", "load_entry"], [[mode], [mode], [mode]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
  let calls: i32 = 0;
  for b in out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "load_wrapper") { calls = calls + 1; } } }
  if ((row_load == 0 || row_load == 2) && calls != 0) { print("managed numeric load blocked"); return 82; }
  if ((row_load == 1 || row_load == 3 || row_load == 4 || row_load == 5) && calls != 1) { print("unknown raw load escaped"); return 83; }
  row_load = row_load + 1;
 }
 let shadowed_scan_wrapper = ssasem.Func { ...wrapper, shadows: ["__count_below_u64"] };
 let shadowed_out = seminline.inline_leaves([kernel, shadowed_scan_wrapper, entry], ["__count_below_u64", "scan_wrapper", "scan_entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
 let shadowed_calls: i32 = 0;
 for b in shadowed_out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "scan_wrapper") { shadowed_calls = shadowed_calls + 1; } } }
 if (shadowed_calls != 1) { print("shadowed builtin escaped"); return 84; }
 let wide: typeinfo.Type = typeinfo.TypeI32 { width: 64, unsigned: true, is_char: false, polymorphic: false };
 let wide_get = ssasem.Contract { name: "external_u64", params: [bytes], modes: [ssasem.borrow_mode()], result: wide };
 let shadow_consume = ssasem.Contract { name: "__clz64", params: [wide], modes: [ssasem.value_mode()], result: int_type() };
 let shadow_wrap = ssasem.Contract { name: "shadow_consumer_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
 let wide_getter = func("external_u64", [], [], [], wide, []);
 let consumer_wrapper = func("shadow_consumer_wrapper", [bytes], [bytes, wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "external_u64"), op(ssasem.call(), 2, [1], 0, "__clz64")], 2)], int_type(), [wide_get, shadow_consume]);
 consumer_wrapper = ssasem.Func { ...consumer_wrapper, shadows: ["__clz64"] };
 let consumer_entry = func("shadow_consumer_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "shadow_consumer_wrapper")], 1)], int_type(), [shadow_wrap]);
 let consumer_out = seminline.inline_leaves([wide_getter, consumer_wrapper, consumer_entry], ["external_u64", "shadow_consumer_wrapper", "shadow_consumer_entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, true, false], [false, true, true], [false, true, false], [false, false, false], [false, true, false], false, seminline.own_roots(3));
 let consumer_calls: i32 = 0;
 for b in consumer_out.funcs[2].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "shadow_consumer_wrapper") { consumer_calls = consumer_calls + 1; } } }
 if (consumer_calls != 1) { print("shadowed scalar consumer escaped"); return 85; }
 let consumer_case: i32 = 1;
 while (consumer_case < 2) {
  let data = func("copy_value", [wide], [wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.cast(), 1, [0], 0, "")], 1)], int_type(), []);
  if (consumer_case == 1) {
   let ints = typeinfo.TypeArray { elem: int_type(), view: false };
   data = func("copy_value", [wide], [wide, ints, int_type(), int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.cast(), 1, [0], 0, ""), op(1, 2, [], 0, ""), op(ssasem.array_get(), 3, [1, 2], 0, "")], 3)], int_type(), []);
  }
  let data_contract = ssasem.Contract { name: "copy_value", params: [wide], modes: [ssasem.value_mode()], result: int_type() };
  let wrap_contract = ssasem.Contract { name: "copy_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
  let wrapped = func("copy_wrapper", [bytes], [bytes, wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "external_u64"), op(ssasem.call(), 2, [1], 0, "copy_value")], 2)], int_type(), [wide_get, data_contract]);
  let entry = func("copy_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "copy_wrapper")], 1)], int_type(), [wrap_contract]);
  let out = seminline.inline_leaves([wide_getter, data, wrapped, entry], ["external_u64", "copy_value", "copy_wrapper", "copy_entry"], [[], [ssasem.value_mode()], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, false, true, false], [false, false, true, true], [false, false, true, false], [false, false, false, false], [false, false, true, false], false, seminline.own_roots(4));
  let calls: i32 = 0;
  for b in out.funcs[3].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "copy_wrapper") { calls = calls + 1; } } }
  if (calls != consumer_case) { print("copied consumer admission"); return 86; }
  consumer_case = consumer_case + 1;
 }
 let indirect_case: i32 = 0;
 while (indirect_case < 3) {
  let environment = typeinfo.TypeArray { elem: int_type(), view: false };
  let callback_contract = ssasem.Contract { name: "callback_value", params: [environment, bytes], modes: [ssasem.borrow_mode(), ssasem.borrow_mode()], result: wide };
  let callback = func("callback_value", [environment, bytes], [environment, bytes, wide], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.param(), 1, [], 1, ""), op(1, 2, [], 7, "")], 2)], wide, []);
  if (indirect_case == 2) {
   callback = func("callback_value", [environment, bytes], [environment, bytes, wide], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.param(), 1, [], 1, ""), op(ssasem.call(), 2, [1], 0, "external_u64")], 2)], wide, [wide_get]);
  }
  let fn_type = ssasem.closure_type(callback_contract);
  let captures: i32[] = if (indirect_case == 1) { [0] } else { [] };
  let use_contract = ssasem.Contract { name: "use_indirect_word", params: [wide], modes: [ssasem.value_mode()], result: int_type() };
  let wrap_contract = ssasem.Contract { name: "indirect_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
  let wrapper = func("indirect_wrapper", [bytes], [bytes, fn_type, wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.closure_new(), 1, captures, 0, "callback_value"), op(ssasem.call_value(), 2, [1, 0], 0, ""), op(ssasem.call(), 3, [2], 0, "use_indirect_word")], 3)], int_type(), [callback_contract, use_contract]);
  let entry = func("indirect_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "indirect_wrapper")], 1)], int_type(), [wrap_contract]);
  let out = seminline.inline_leaves([wide_getter, callback, wrapper, entry], ["external_u64", "callback_value", "indirect_wrapper", "indirect_entry"], [[], [ssasem.borrow_mode(), ssasem.borrow_mode()], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, false, true, false], [false, false, true, true], [false, false, true, false], [false, false, false, false], [false, false, true, false], false, seminline.own_roots(4));
  let calls: i32 = 0;
  for b in out.funcs[3].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "indirect_wrapper") { calls = calls + 1; } } }
  if (calls != if (indirect_case == 0) { 0 } else { 1 }) { print("known indirect return summary"); return 87; }
  indirect_case = indirect_case + 1;
 }
 let dependency_case: i32 = 1;
 while (dependency_case < 2) {
  let values = typeinfo.TypeArray { elem: wide, view: false };
  let get_contract = ssasem.Contract { name: "external_values", params: [bytes], modes: [ssasem.borrow_mode()], result: values };
  let external = func("external_values", [], [], [], values, []);
  let select_contract = ssasem.Contract { name: "select_value", params: [values], modes: [ssasem.borrow_mode()], result: wide };
  let selected = func("select_value", [values], [values, wide], [block([op(ssasem.param(), 0, [], 0, ""), op(1, 1, [], 7, "")], 1)], wide, []);
  if (dependency_case == 1) {
   selected = func("select_value", [values], [values, int_type(), wide], [block([op(ssasem.param(), 0, [], 0, ""), op(1, 1, [], 0, ""), op(ssasem.array_get(), 2, [0, 1], 0, "")], 2)], wide, []);
  }
  let use_contract = ssasem.Contract { name: "use_selected", params: [wide], modes: [ssasem.value_mode()], result: int_type() };
  let wrap_contract = ssasem.Contract { name: "selected_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
  let wrapper = func("selected_wrapper", [bytes], [bytes, values, wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "external_values"), op(ssasem.call(), 2, [1], 0, "select_value"), op(ssasem.call(), 3, [2], 0, "use_selected")], 3)], int_type(), [get_contract, select_contract, use_contract]);
  let entry = func("selected_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "selected_wrapper")], 1)], int_type(), [wrap_contract]);
  let out = seminline.inline_leaves([external, selected, wrapper, entry], ["external_values", "select_value", "selected_wrapper", "selected_entry"], [[], [ssasem.borrow_mode()], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, false, true, false], [false, false, true, true], [false, false, true, false], [false, false, false, false], [false, false, true, false], false, seminline.own_roots(4));
  let calls: i32 = 0;
  for b in out.funcs[3].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "selected_wrapper") { calls = calls + 1; } } }
  if (calls != dependency_case) { print("return argument dependency"); return 88; }
  dependency_case = dependency_case + 1;
 }
 let actual_contract = ssasem.Contract { name: "clashing_consumer", params: [wide], modes: [ssasem.value_mode()], result: int_type() };
 let actual = func("clashing_consumer", [], [], [], int_type(), []);
 let method = func("clashing_consumer", [wide], [wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.cast(), 1, [0], 0, "")], 1)], int_type(), []);
 let alias_contract = ssasem.Contract { name: "alias_wrapper", params: [bytes], modes: [ssasem.borrow_mode()], result: int_type() };
 let alias_wrapper = func("alias_wrapper", [bytes], [bytes, wide, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "external_u64"), op(ssasem.call(), 2, [1], 0, "clashing_consumer")], 2)], int_type(), [wide_get, actual_contract]);
 let alias_entry = func("alias_entry", [bytes], [bytes, int_type()], [block([op(ssasem.param(), 0, [], 0, ""), op(ssasem.call(), 1, [0], 0, "alias_wrapper")], 1)], int_type(), [alias_contract]);
 let alias_out = seminline.inline_leaves([wide_getter, actual, method, alias_wrapper, alias_entry], ["external_u64", "clashing_consumer", "Other.clashing_consumer", "alias_wrapper", "alias_entry"], [[], [], [ssasem.value_mode()], [ssasem.borrow_mode()], [ssasem.borrow_mode()]], [false, false, false, true, false], [false, false, false, true, true], [false, false, false, true, false], [false, false, false, false, false], [false, false, false, true, false], false, seminline.own_roots(5));
 let alias_calls: i32 = 0;
 for b in alias_out.funcs[4].graph.blocks { for ins in b.insts { if (ins.kind_tag == ssasem.call() && ins.str == "alias_wrapper") { alias_calls = alias_calls + 1; } } }
 if (alias_calls != 1) { print("qualified consumer alias"); return 89; }
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

const inlineParentClosureOwnerProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
struct Owner { h: u64, keep: () => usize }
function count(o: Owner): i32 { return buf_len(o.h as usize); }
@noinline function make(h: usize): Owner { let keep: Blk = Blk { h: h }; return Owner { h: h as u64, keep: (): usize => { return keep.h; } }; }
function main(): i32 {
 let h: usize = buf_new(8);
 buf_push_byte(h, 1);
 let o: Owner = make(h);
 let n: i32 = count(o);
 buf_free(h);
 if (n != 1) { return n; }
 print("ok");
 return 0;
}
`

const inlineUnusedFinalizerArgumentProgram = `import "core/mem";
struct Source { h: usize }
impl mem.Drop for Source { function drop(self: Source): void { buf_push_byte(self.h, 9); } }
struct Borrowed { h: usize, out: usize }
impl mem.Drop for Borrowed { function drop(self: Borrowed): void { for i in 0..buf_len(self.h) { buf_push_byte(self.out, 1); } } }
@noinline function borrowed(s: Source, out: usize): Borrowed { return Borrowed { h: s.h, out: out }; }
@noinline function ignore(b: Borrowed): i32 { return 0; }
@noinline function make_source(h: usize): Source { return Source { h: h }; }
function wrap(s: Source, out: usize): i32 { return ignore(borrowed(s, out)); }
function main(): i32 {
 let h: usize = buf_new(8);
 let out: usize = buf_new(8);
 buf_push_byte(h, 1);
 let s: Source = make_source(h);
 let n: i32 = wrap(s, out);
 let got: i32 = buf_len(out);
 buf_free(h);
 buf_free(out);
 if (n != 0 || got != 1) { return got; }
 print("ok");
 return 0;
}
`

const inlineUnusedRecordWordOwnerProgram = `import "core/mem";
struct Blk { h: usize }
struct Holder { b: Blk }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
@noinline function read_handle(b: Blk): u64 { return b.h as u64; }
@noinline function consume(h: u64): i32 { return buf_len(h as usize); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function count(b: Blk): i32 { let h: u64 = read_handle(b); let unused: Holder = Holder { b: b }; return consume(h); }
function main(): i32 {
  let h: usize = buf_new(8); buf_push_byte(h, 1);
  let b: Blk = make(h); let n: i32 = count(b); buf_free(h);
  if (n != 1) { return n; } print("ok"); return 0;
}
`

const inlineUnusedTupleWordOwnerProgram = `import "core/mem";
struct Blk { h: usize }
struct Holder { b: Blk }
impl mem.Drop for Blk { function drop(self: Blk): void { buf_push_byte(self.h, 9); } }
@noinline function read_handle(b: Blk): u64 { return b.h as u64; }
@noinline function consume(h: u64): i32 { return buf_len(h as usize); }
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function count(b: Blk): i32 { let h: u64 = read_handle(b); let unused: (Blk, i32) = (b, 0); return consume(h); }
function main(): i32 {
  let h: usize = buf_new(8); buf_push_byte(h, 1);
  let b: Blk = make(h); let n: i32 = count(b); buf_free(h);
  if (n != 1) { return n; } print("ok"); return 0;
}
`
