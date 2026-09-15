package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const physicalRCHelpers = `
function inst(kind: i32, value: i32, args: i32[], imm: i32): ssa.SInst {
    return ssa.SInst { kind_tag: kind, result: value, args: args, imm: imm, str: "" };
}
function ret(value: i32): ssa.STerm { return ssa.STerm { kind_tag: 1, value: value, cond: 0, target: 0, t: 0, f: 0 }; }
// Explicit caller contract for ABI tests. The caller here is AST-lowered, so
// its supplies are written out rather than taken from the AST's escape
// inference, which would test a different contract than the callee's.
function caller(template: irlower.LowerResult, mode: i32): irlower.LowerResult {
    var ops: ir.Op[] = [ir.op_const_i32(7), ir.op_arr_make(1, 32), ir.op_store_local(0),
        ir.op_const_i32(0), ir.op_store_local(2), ir.op_block(0), ir.op_loop(0),
        ir.op_load_local(2), ir.op_const_i32(32), ir.op_bin("ge_s", 0, false), ir.op_brif(1)];
    if (mode == ssaunits.counted_mode()) {
        ops = ops.append(ir.op_load_local(0));
        ops = ops.append(ir.op_call_direct("__fern_rc_inc", 1));
        ops = ops.append(ir.op_drop());
    }
    ops = ops.append(ir.op_load_local(0));
    ops = ops.append(ir.op_call_direct("produce", 1));
    ops = ops.append(ir.op_store_local(1));
    // Heap churn and both caller/callee values checked before each release.
    ops = ops.append(ir.op_const_i32(91));
    ops = ops.append(ir.op_arr_make(1, 32));
    ops = ops.append(ir.op_call_direct("__fern_rc_dec", 1));
    ops = ops.append(ir.op_drop());
    var slot: i32 = 0;
    while (slot < 2) {
        ops = ops.append(ir.op_load_local(slot));
        ops = ops.append(ir.op_const_i32(0));
        ops = ops.append(ir.op_arr_get(32));
        ops = ops.append(ir.op_const_i32(7));
        ops = ops.append(ir.op_bin("ne", 0, false));
        ops = ops.append(ir.op_if(0));
        ops = ops.append(ir.op_const_i32(2));
        ops = ops.append(ir.op_return());
        ops = ops.append(ir.op_end());
        slot = slot + 1;
    }
    ops = ops.append(ir.op_load_local(1));
    ops = ops.append(ir.op_call_direct("__fern_rc_dec", 1));
    ops = ops.append(ir.op_drop());
    ops = ops.append(ir.op_load_local(2));
    ops = ops.append(ir.op_const_i32(1));
    ops = ops.append(ir.op_bin("add", 0, false));
    ops = ops.append(ir.op_store_local(2));
    ops = ops.append(ir.op_br(0));
    ops = ops.append(ir.op_end());
    ops = ops.append(ir.op_end());
    ops = ops.append(ir.op_load_local(0));
    ops = ops.append(ir.op_call_direct("__fern_rc_dec", 1));
    ops = ops.append(ir.op_drop());
    ops = ops.append(ir.op_call_direct("__fern_rc_underflow_get", 0));
    ops = ops.append(ir.op_return());
    return irlower.LowerResult { ...template, ops: ops, n_locals: 3,
        n_params: 0, arr_slots: [0, 1], str_slots: [], i64_slots: [], f64_slots: [] };
}
function fixture(): ssasem.Func {
    var i: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false };
    var row: typeinfo.Type = typeinfo.TypeArray { elem: i };
    var rows: typeinfo.Type = typeinfo.TypeArray { elem: row };
    var pair: typeinfo.Type = typeinfo.TypeTuple { elements: [rows, rows] };
    var types: typeinfo.Type[] = [i, row, rows, i, row];
    var ops: ssa.SInst[] = [inst(1, 0, [], 7), inst(ssasem.array_new(), 1, [0], 0),
        inst(ssasem.array_new(), 2, [1], 0), inst(1, 3, [], 0), inst(ssasem.array_get(), 4, [2, 3], 0)];
    var result: i32 = 4;
    var params: typeinfo.Type[] = [];
    var blocks: ssa.SBlock[] = [];
    FIXTURE
    var graph = ssa.SFunc { name: "produce", nparams: params.len(), nvals: types.len(), entry: 7,
        takes_env: false, blocks: [ssa.SBlock { id: 7, preds: [], insts: ops, term: ret(result) }] };
    if (blocks.len() > 0) { graph = ssa.SFunc { ...graph, blocks: blocks }; }
    return ssasem.Func { graph: graph, values: types, params: params, result: row, records: [], enums: [], calls: [] };
}
function main(): i32 {
    var f = fixture();
    var modes: i32[] = MODES;
    var p = ssaunits.plan(f, modes);
    var lowered = ssarc.lower(f, modes, p, irlower.struct_tab_empty());
    if (!lowered.ok) { eprint(lowered.why); return 1; }
    var options = args();
    if (options.len() > 3 && options[3] == "omit-retains") {
        var broken: ir.Op[] = [];
        var removed: boolean = false;
        for op in lowered.ops {
            // rc_inc returns its input. Keeping the load and following drop
            // preserves stack shape while deliberately omitting the count.
            if (op.str != "__fern_rc_inc") { broken = broken.append(op); }
            else { removed = true; }
        }
        if (!removed) { eprint("no physical retain to omit"); return 4; }
        eprint("omitted physical retains\n");
        lowered = irlower.LowerResult { ...lowered, ops: broken };
    }
    var src: string = "function produce(): i32[] { return [7]; } @noinline function exercise(): i32 { var j = 0; while (j < 32) { var xs = produce(); var churn = [91, 92, 93]; if (xs.len() != 1 || xs[0] != 7 || churn[0] != 91) { return 2; } j = j + 1; } return 0; } function main(): i32 { var result = exercise(); if (result != 0) { return result; } if (__rc_underflow_count() != 0) { return 99; } return 0; }";
    var mod = parser.parse_module(lexer.tokenize(src));
    var tab = irlower.struct_tab(mod.structs);
    var base = ircore.wp_fn_sigs(mod.funcs, tab);
    var g = ircore.lower_gated(mod, tab, base, [], false);
    if (!g.ok) { return 3; }
    var cache: irlower.LowerResult[] = [];
    var at: i32 = 0;
    for fd in mod.funcs {
        if (fd.name == "produce") { cache = cache.append(lowered); }
        else if (fd.name == "exercise" && modes.len() > 0 && modes[0] != 1) { cache = cache.append(caller(g.cache[at], modes[0])); }
        else { cache = cache.append(g.cache[at]); }
        at = at + 1;
    }
    var av = args();
    if (av[1] == "x86-64-linux") {
        print(asm_ir.emit_module_ir_unit_flat(mod, true, false, "", [], mod.funcs, tab, 0, 0 - 1, cache, base));
    } else if (av[1] == "arm64-linux") {
        strbuf_reset();
        var state = asmcore.new_state();
        state = asmcore.EmitState { ...state, struct_decls: tab, funcs: mod.funcs };
        state = asm_arm64_ir.emit_body(mod, state, false, cache, base);
        state = asm_arm64_ir.emit_ir_runtime(state, false);
        print(strbuf_take());
    } else { print(wasm_ir.emit_ir_module_mode(mod, cache, 0, base)); }
    return 0;
}
`

func physicalRCSource(setup, modes string) string {
	if modes == "" {
		modes = "[]"
	}
	source := strings.Replace(physicalRCHelpers, "FIXTURE", setup, 1)
	source = strings.Replace(source, "MODES", modes, 1)
	if modes == "[1]" || modes == "[1, 1]" {
		parameters, arguments, expected := "flag: boolean", "j % 2 == 0", "7 + j % 2"
		if modes == "[1, 1]" {
			parameters, arguments, expected = "flag: boolean, second: boolean", "j % 2 == 0, (j / 2) % 2 == 0", "7 + j % 2 + 2 * ((j / 2) % 2)"
		}
		source = strings.Replace(source, "function produce(): i32[]", "function produce("+parameters+"): i32[]", 1)
		source = strings.Replace(source, "var xs = produce();", "var xs = produce("+arguments+");", 1)
		source = strings.Replace(source, "xs[0] != 7", "xs[0] != "+expected, 1)
	} else if modes != "[]" {
		parameter := "seed: i32[]"
		if modes == "[3]" {
			parameter = "own " + parameter
		}
		source = strings.Replace(source, "function produce(): i32[] { return [7]; }", "function produce("+parameter+"): i32[] { return seed; }", 1)
		source = strings.Replace(source, "var j = 0; while", "var seed = [7]; var before = seed; var j = 0; while", 1)
		source = strings.Replace(source, "var xs = produce();", "var xs = produce(seed);", 1)
		source = strings.Replace(source, "churn[0] != 91", "churn[0] != 91 || before[0] != 7 || seed[0] != 7", 1)
	}
	source = strings.Replace(source, `var src: string = "function produce`, `var src: string = "@noinline function produce`, 1)
	return `import "./ssarc"; import "./ssasem"; import "./ssaunits"; import "./ssa";
import "./typeinfo"; import "./semrecords"; import "./parser"; import "./lexer"; import "./irlower"; import "./ir"; import "./util";
import "./ircore"; import "./asmcore"; import "./asm_ir"; import "./asm_arm64_ir"; import "./wasm_ir";
` + source
}

func TestSelfHostSSAPhysicalRC(t *testing.T) {
	testPhysicalRC(t, false)
}

func TestSelfHostSSAPhysicalRCIRArm64(t *testing.T) {
	testPhysicalRC(t, true)
}

func TestSelfHostSSAPhysicalRCRejects(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	source := strings.Split(physicalRCSource("", ""), "function main(): i32 {")[0] + `
function refused(r: irlower.LowerResult, why: string): boolean {
    return !r.ok && r.why == why && r.ops.len() == 0 && r.n_locals == 0 && r.n_params == 0;
}
function has_sub(all: string, needle: string): boolean {
    var i: i32 = 0;
    while (i + needle.len() <= all.len()) {
        if (slice_unchecked(all, i, i + needle.len()) == needle) { return true; }
        i = i + 1;
    }
    return false;
}
// Whether the array construction one lowered graph emits stores eight-byte
// elements, in the integer form wasm loads an i64 with when asked for it and
// the float form otherwise. The register backends give every element eight
// bytes and read neither flag.
function made_wide(r: irlower.LowerResult, integer: boolean): boolean {
    for o in r.ops {
        if (o.kind_tag == ir.kind_id("arr_make")) { return o.width == 64 && o.unsigned == integer; }
    }
    return false;
}
// The width conversions one lowered graph emits, concatenated — "" when it
// emits none. A contract the planner refuses comes back as its reason instead,
// so one helper covers both what an operation is allowed to be and what it
// lowers to.
function masks(r: irlower.LowerResult): string {
    if (!r.ok) { return "lower:" + r.why; }
    var out: string = "";
    for o in r.ops {
        if (o.kind_tag == ir.kind_id("int_cast")) { out = out + o.str; }
        if (o.kind_tag == ir.kind_id("int_extend")) { out = out + "extend"; }
        if (o.kind_tag == ir.kind_id("int_wrap")) { out = out + "wrap"; }
    }
    return out;
}
function binary_masks(op: string, t: typeinfo.Type, result: typeinfo.Type): string {
    var g = ssa.SFunc { name: "bin", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: 9, result: 2, args: [0, 1], imm: 0, str: op }], term: ret(2) }] };
    var f = ssasem.Func { graph: g, values: [t, t, result], params: [t, t], result: result,
        records: [], enums: [], calls: [] };
    var p = ssaunits.plan(f, [1, 1]);
    if (!p.ok) { return "plan:" + p.why; }
    return masks(ssarc.lower(f, [1, 1], p, irlower.struct_tab_empty()));
}
function wide_binary(t: typeinfo.Type, result: typeinfo.Type, op: string): ssasem.Func {
    var g = ssa.SFunc { name: "wbin", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: 9, result: 2, args: [0, 1], imm: 0, str: op }], term: ret(2) }] };
    return ssasem.Func { graph: g, values: [t, t, result], params: [t, t], result: result,
        records: [], enums: [], calls: [] };
}
function cast_masks(from: typeinfo.Type, to: typeinfo.Type): string {
    var g = ssa.SFunc { name: "cast", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: ssasem.cast(), result: 1, args: [0], imm: 0, str: "" }], term: ret(1) }] };
    var f = ssasem.Func { graph: g, values: [from, to], params: [from], result: to,
        records: [], enums: [], calls: [] };
    var p = ssaunits.plan(f, [1]);
    if (!p.ok) { return "plan:" + p.why; }
    return masks(ssarc.lower(f, [1], p, irlower.struct_tab_empty()));
}
function main(): i32 {
    var f = fixture();
    var p = ssaunits.plan(f, []);
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, ok: false }, irlower.struct_tab_empty()), "missing successful unit plan")) { return 1; }
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, steps: [] }, irlower.struct_tab_empty()), "missing or duplicate entry step")) { return 2; }
    var b = f.graph.blocks[0];
    var first = ssa.SBlock { ...b, insts: b.insts.append(inst(2, 5, [], 0)), term: ssa.STerm { kind_tag: 2, target: 27, value: 0, cond: 0, t: 0, f: 0 } };
    // Two blocks entering each other with neither dominating: a cycle no
    // single loop label can head.
    first = ssa.SBlock { ...first, term: ssa.STerm { kind_tag: 3, target: 0, value: 0, cond: 5, t: 27, f: 37 } };
    var left = ssa.SBlock { id: 27, preds: [7, 37], insts: [], term: ssa.STerm { kind_tag: 2, target: 37, value: 0, cond: 0, t: 0, f: 0 } };
    var right = ssa.SBlock { id: 37, preds: [7, 27], insts: [], term: ssa.STerm { kind_tag: 3, target: 0, value: 0, cond: 5, t: 27, f: 47 } };
    var last = ssa.SBlock { id: 47, preds: [37], insts: [], term: b.term };
    var graph = ssa.SFunc { ...f.graph, nvals: 6, blocks: [first, left, right, last] };
    var cfg = ssasem.Func { ...f, graph: graph, values: f.values.append(typeinfo.TypeBool { tag: 0 }) };
    var cp = ssaunits.plan(cfg, []);
    if (!cp.ok) { eprint(cp.why); return 3; }
    if (!refused(ssarc.lower(cfg, [], cp, irlower.struct_tab_empty()), "physical RC needs reducible graph")) { return 4; }
    // An array of a 64-bit element: its ops carry the eight-byte stride, so it
    // is a value here like any other array.
    var wide: typeinfo.Type = typeinfo.TypeArray { elem: typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false } };
    var g = ssa.SFunc { name: "unsupported", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    var typed = ssasem.Func { graph: g, values: [wide], params: [wide], result: wide, records: [], enums: [], calls: [] };
    var plan = ssaunits.plan(typed, [2]);
    if (!plan.ok) { eprint(plan.why); return 5; }
    if (!ssarc.lower(typed, [2], plan, irlower.struct_tab_empty()).ok) { return 6; }
    // A record instance with type arguments is named by more than its
    // declaration, and a schema field this vocabulary cannot WALK refuses the
    // whole schema; a plain record with an array field lowers.
    var wideType: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [wide] };
    var wideSchema = semrecords.Record { ty: wideType, fields: [semrecords.Field { name: "xs", ty: f.result }] };
    var recordType: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [] };
    var schema = semrecords.Record { ty: recordType, fields: [semrecords.Field { name: "xs", ty: f.result }] };
    var recordGraph = ssa.SFunc { name: "record", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.record_new(), 1, [0], 0)], term: ret(1) }] };
    var genericFunc = ssasem.Func { graph: recordGraph, values: [f.result, wideType], params: [f.result], result: wideType, records: [wideSchema], enums: [], calls: [] };
    var genericPlan = ssaunits.plan(genericFunc, [2]);
    if (!genericPlan.ok) { eprint(genericPlan.why); return 7; }
    if (!refused(ssarc.lower(genericFunc, [2], genericPlan, irlower.struct_tab_empty()), "unsupported physical RC value type")) { return 8; }
    // A wide array is not such a field. The walk visits only the REFERENCE
    // fields, and an array of scalars has no element to visit, so it needs its
    // own box released and nothing more.
    var wideField = semrecords.Record { ty: recordType, fields: [semrecords.Field { name: "xs", ty: f.result },
        semrecords.Field { name: "ns", ty: wide }] };
    var wideFieldFunc = ssasem.Func { graph: g, values: [recordType], params: [recordType], result: recordType, records: [wideField], enums: [], calls: [] };
    var wideFieldPlan = ssaunits.plan(wideFieldFunc, [2]);
    if (!wideFieldPlan.ok) { eprint(wideFieldPlan.why); return 9; }
    if (!ssarc.lower(wideFieldFunc, [2], wideFieldPlan, irlower.struct_tab_empty()).ok) { return 10; }
    var recordFunc = ssasem.Func { graph: recordGraph, values: [f.result, recordType], params: [f.result], result: recordType, records: [schema], enums: [], calls: [] };
    var recordPlan = ssaunits.plan(recordFunc, [2]);
    if (!recordPlan.ok) { eprint(recordPlan.why); return 11; }
    if (!ssarc.lower(recordFunc, [2], recordPlan, irlower.struct_tab_empty()).ok) { return 12; }
    // A variant field this vocabulary cannot walk refuses the enum, and it
    // refuses on ANY variant, not only the one this graph builds; a wide
    // payload is walkable for the same reason a wide record field is.
    var shapeType: typeinfo.Type = typeinfo.TypeUnion { name: "Shape", args: [] };
    var enumGraph = ssa.SFunc { name: "enum", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), ssa.SInst { kind_tag: ssasem.variant_new(), result: 1, args: [0], imm: 0, str: "W" }], term: ret(1) }] };
    var wideEnum = semrecords.Enum { ty: shapeType, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] },
        semrecords.Variant { name: "N", fields: [semrecords.Field { name: "__ev", ty: wideType }] }], layout: semrecords.layout_variant() };
    var wideEnumFunc = ssasem.Func { graph: enumGraph, values: [f.result, shapeType], params: [f.result], result: shapeType, records: [wideSchema], enums: [wideEnum], calls: [] };
    var wideEnumPlan = ssaunits.plan(wideEnumFunc, [2]);
    if (!wideEnumPlan.ok) { eprint(wideEnumPlan.why); return 13; }
    if (!refused(ssarc.lower(wideEnumFunc, [2], wideEnumPlan, irlower.struct_tab_empty()), "unsupported physical RC variant field type")) { return 14; }
    var walkableEnum = semrecords.Enum { ...wideEnum, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] },
        semrecords.Variant { name: "N", fields: [semrecords.Field { name: "__ev", ty: wide }] }] };
    var walkableEnumFunc = ssasem.Func { ...wideEnumFunc, enums: [walkableEnum] };
    if (!ssarc.lower(walkableEnumFunc, [2], ssaunits.plan(walkableEnumFunc, [2]), irlower.struct_tab_empty()).ok) { return 59; }
    var arrayEnum = semrecords.Enum { ty: shapeType, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] }], layout: semrecords.layout_variant() };
    var enumFunc = ssasem.Func { graph: enumGraph, values: [f.result, shapeType], params: [f.result], result: shapeType, records: [], enums: [arrayEnum], calls: [] };
    var enumPlan = ssaunits.plan(enumFunc, [2]);
    if (!enumPlan.ok) { eprint(enumPlan.why); return 15; }
    if (!ssarc.lower(enumFunc, [2], enumPlan, irlower.struct_tab_empty()).ok) { return 16; }
    // A type that reaches itself has no finite INLINE expansion, so its
    // children are released by a per-type helper the walk calls. The call is
    // what makes the descent finite, so the helper's own body must contain it.
    var selfType: typeinfo.Type = typeinfo.TypeStruct { name: "Node", args: [] };
    var selfSchema = semrecords.Record { ty: selfType, fields: [semrecords.Field { name: "kid", ty: selfType }] };
    var selfGraph = ssa.SFunc { name: "cycle", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    var selfFunc = ssasem.Func { graph: selfGraph, values: [selfType], params: [selfType], result: selfType, records: [selfSchema], enums: [], calls: [] };
    var selfPlan = ssaunits.plan(selfFunc, [2]);
    if (!selfPlan.ok) { eprint(selfPlan.why); return 17; }
    var selfLowered = ssarc.lower(selfFunc, [2], selfPlan, irlower.struct_tab_empty());
    if (!selfLowered.ok) { eprint(selfLowered.why); return 18; }
    var selfHelpers = ssarc.drop_helpers(selfFunc);
    if (selfHelpers.len() != 1) { return 19; }
    if (selfHelpers[0].name != "__sem_drop_Node") { eprint(selfHelpers[0].name); return 20; }
    if (selfHelpers[0].n_params != 1) { return 21; }
    // n_locals covers the parameter even before a child slot is reserved.
    if (selfHelpers[0].n_locals < 1) { return 22; }
    var sawSelfCall: boolean = false;
    for o in selfHelpers[0].ops {
        if (ir.render_op(o) == "call_direct __sem_drop_Node/1") { sawSelfCall = true; }
    }
    if (!sawSelfCall) { return 23; }
    // A schema with no reference field needs no helper, so none is emitted:
    // a body exists exactly when a call to it does.
    var flatType: typeinfo.Type = typeinfo.TypeStruct { name: "Flat", args: [] };
    var flatSchema = semrecords.Record { ty: flatType, fields: [semrecords.Field { name: "n", ty: typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false } }] };
    var flatFunc = ssasem.Func { graph: selfGraph, values: [flatType], params: [flatType], result: flatType, records: [flatSchema], enums: [], calls: [] };
    if (ssarc.drop_helpers(flatFunc).len() != 0) { return 24; }
    // Two views of one type merge to a single tail entry rather than two.
    var merged = ssarc.merge_helpers([], ssarc.drop_helpers(selfFunc).append(ssarc.drop_helpers(selfFunc)[0]));
    if (merged.len() != 1) { return 25; }
    if (!merged[0].ok) { eprint(merged[0].why); return 26; }
    // Two bodies under one symbol refuse instead. The weak linkage that lets
    // duplicates co-link would otherwise pick one of them silently.
    var perturbed = irlower.LowerResult { ...selfHelpers[0], ops: selfHelpers[0].ops.append(ir.op_const_i32(1)) };
    var clashed = ssarc.merge_helpers([], [selfHelpers[0], perturbed]);
    if (clashed.len() != 1) { return 27; }
    if (clashed[0].ok) { return 28; }
    if (clashed[0].why != "conflicting drop helper for __sem_drop_Node") { eprint(clashed[0].why); return 29; }
    // The AST-caller row for a nominal result. The bare name asserts SOLE
    // ownership, which this boundary does not promise, and its exit sweep runs
    // the field walk unguarded — so it is granted only to a schema with no
    // reference field, where there is no field walk to run and the release is
    // the rc-guarded box dec alone.
    var i32ty: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false };
    var boxOnly: typeinfo.Type = typeinfo.TypeStruct { name: "BoxOnly", args: [] };
    var boxOnlySchema = semrecords.Record { ty: boxOnly, fields: [semrecords.Field { name: "n", ty: i32ty }] };
    var boxOnlyFunc = ssasem.Func { graph: selfGraph, values: [boxOnly], params: [boxOnly], result: boxOnly, records: [boxOnlySchema], enums: [], calls: [] };
    if (util.index_of_str(ssarc.caller_sigs(irlower.fn_sigs_empty(), "mk", boxOnlyFunc, [3]).return_fresh_struct_ret_fns, "mk") < 0) { return 30; }
    // A reference field is exactly what makes that sweep dangerous, so the same
    // shape one field over gets nothing and keeps the leak floor.
    var withKids: typeinfo.Type = typeinfo.TypeStruct { name: "WithKids", args: [] };
    var withKidsSchema = semrecords.Record { ty: withKids, fields: [semrecords.Field { name: "xs", ty: f.result }] };
    var withKidsFunc = ssasem.Func { graph: selfGraph, values: [withKids], params: [withKids], result: withKids, records: [withKidsSchema], enums: [], calls: [] };
    if (util.index_of_str(ssarc.caller_sigs(irlower.fn_sigs_empty(), "mk", withKidsFunc, [3]).return_fresh_struct_ret_fns, "mk") >= 0) { return 31; }
    // The parameter rows come from the contract, not the syntax: a borrowed
    // reference parameter is the retained-keep row and never the bare one,
    // and a counted or scalar parameter has neither.
    var rowSigs = ssarc.caller_sigs(irlower.fn_sigs_empty(), "mk", ssasem.Func { ...withKidsFunc, params: [withKids, i32ty, withKids] }, [2, 1, 3]);
    var rowAll: string = "";
    for bucket in rowSigs.borrowable_params { rowAll = rowAll + bucket; }
    if (!has_sub(rowAll, "CNT:mk|100\n") || has_sub("\n" + rowAll, "\nmk|")) { return 130; }
    if (util.index_of_str(rowSigs.param_counted, "PCNT:mk|100") < 0 || rowSigs.param_counted.len() != 1) { return 132; }
    var rowNone = ssarc.caller_sigs(irlower.FnSigs { ...rowSigs, borrowable_params: irlower.borrow_reg_set(rowSigs.borrowable_params, "mk", "1") }, "mk", withKidsFunc, [3]);
    rowAll = "";
    for bucket in rowNone.borrowable_params { rowAll = rowAll + bucket; }
    if (has_sub("\n" + rowAll, "\nmk|")) { return 131; }
    // A length reads its receiver and hands back an i32 that owns nothing: an
    // array selects arr_len, a string str_len, and a receiver that is neither
    // is not a counted container this can read at all.
    var lenGraph = ssa.SFunc { name: "len", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.length(), 1, [0], 0)], term: ret(1) }] };
    var arrLen = ssasem.Func { graph: lenGraph, values: [f.result, i32ty], params: [f.result], result: i32ty, records: [], enums: [], calls: [] };
    var arrLenPlan = ssaunits.plan(arrLen, [2]);
    if (!arrLenPlan.ok) { eprint(arrLenPlan.why); return 32; }
    var arrLenLowered = ssarc.lower(arrLen, [2], arrLenPlan, irlower.struct_tab_empty());
    if (!arrLenLowered.ok) { eprint(arrLenLowered.why); return 33; }
    var sawArrLen: boolean = false;
    for o in arrLenLowered.ops { if (ir.render_op(o) == "arr_len") { sawArrLen = true; } }
    if (!sawArrLen) { return 34; }
    var strTy: typeinfo.Type = typeinfo.TypeString { tag: 0 };
    var strLen = ssasem.Func { graph: lenGraph, values: [strTy, i32ty], params: [strTy], result: i32ty, records: [], enums: [], calls: [] };
    var strLenPlan = ssaunits.plan(strLen, [2]);
    if (!strLenPlan.ok) { eprint(strLenPlan.why); return 35; }
    var strLenLowered = ssarc.lower(strLen, [2], strLenPlan, irlower.struct_tab_empty());
    if (!strLenLowered.ok) { eprint(strLenLowered.why); return 36; }
    var sawStrLen: boolean = false;
    for o in strLenLowered.ops { if (ir.render_op(o) == "str_len") { sawStrLen = true; } }
    if (!sawStrLen) { return 37; }
    var badRecv = ssasem.Func { graph: lenGraph, values: [boxOnly, i32ty], params: [boxOnly], result: i32ty, records: [boxOnlySchema], enums: [], calls: [] };
    if (ssaunits.plan(badRecv, [2]).why != "length container type") { return 38; }
    var badResult = ssasem.Func { graph: lenGraph, values: [f.result, f.result], params: [f.result], result: f.result, records: [], enums: [], calls: [] };
    if (ssaunits.plan(badResult, [2]).why != "length result type") { return 39; }
    // An append takes the receiver's unit and hands one back. The runtime's push
    // gives the unit back only when the receiver's box is the only one its count
    // names, so the count test that chooses between the in-place grow and the
    // copy is emitted here rather than left to the shared helper. A counted
    // receiver dead after the push moves its unit into it.
    var appendGraph = ssa.SFunc { name: "append", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.append(), result: 2, args: [0, 1], imm: 0, str: "" }], term: ret(2) }] };
    var appendFunc = ssasem.Func { graph: appendGraph, values: [f.result, i32ty, f.result],
        params: [f.result, i32ty], result: f.result, records: [], enums: [], calls: [] };
    var appendPlan = ssaunits.plan(appendFunc, [3, 1]);
    if (!appendPlan.ok) { eprint(appendPlan.why); return 40; }
    var appendLowered = ssarc.lower(appendFunc, [3, 1], appendPlan, irlower.struct_tab_empty());
    if (!appendLowered.ok) { eprint(appendLowered.why); return 41; }
    var sawPush: boolean = false;
    var sawPushUnique: boolean = false;
    for o in appendLowered.ops {
        if (ir.render_op(o) == "arr_push_owned") { sawPush = true; }
        if (o.str == "__fern_rc_is_unique") { sawPushUnique = true; }
    }
    if (!sawPush || !sawPushUnique) { return 42; }
    // The same graph with a BORROWED receiver: no unit of this function's to
    // move, so the plan RETAINS one at the push. The count then names two boxes,
    // the copy runs, and the result is a box nobody else holds rather than an
    // alias of the caller's.
    var borrowAppendPlan = ssaunits.plan(appendFunc, [2, 1]);
    if (!borrowAppendPlan.ok) { eprint(borrowAppendPlan.why); return 43; }
    var borrowAppendLowered = ssarc.lower(appendFunc, [2, 1], borrowAppendPlan, irlower.struct_tab_empty());
    if (!borrowAppendLowered.ok) { eprint(borrowAppendLowered.why); return 104; }
    var sawRetain: boolean = false;
    var sawBorrowUnique: boolean = false;
    for o in borrowAppendLowered.ops {
        if (o.str == "__fern_rc_inc") { sawRetain = true; }
        if (o.str == "__fern_rc_is_unique") { sawBorrowUnique = true; }
    }
    if (!sawRetain || !sawBorrowUnique) { return 105; }
    // One element replaced hands the receiver's unit over the same way, and
    // lowers to the count test that chooses the in-place store or the copy;
    // a scalar element retains nothing, a counted one retains the copy's
    // elements and releases the element the store replaces.
    var withGraph = ssa.SFunc { name: "with", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.with(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(3) }] };
    var withFunc = ssasem.Func { graph: withGraph, values: [f.result, i32ty, i32ty, f.result],
        params: [f.result, i32ty, i32ty], result: f.result, records: [], enums: [], calls: [] };
    var withPlan = ssaunits.plan(withFunc, [3, 1, 1]);
    if (!withPlan.ok) { eprint(withPlan.why); return 121; }
    var withLowered = ssarc.lower(withFunc, [3, 1, 1], withPlan, irlower.struct_tab_empty());
    if (!withLowered.ok) { eprint(withLowered.why); return 122; }
    var sawUnique: boolean = false;
    var sawSet: boolean = false;
    var sawIncElems: boolean = false;
    for o in withLowered.ops {
        if (o.str == "__fern_rc_is_unique") { sawUnique = true; }
        if (ir.render_op(o) == "arr_set") { sawSet = true; }
        if (o.str == "__fern_arr_inc_elems") { sawIncElems = true; }
    }
    if (!sawUnique || !sawSet || sawIncElems) { return 123; }
    var borrowWithPlan = ssaunits.plan(withFunc, [2, 1, 1]);
    if (!borrowWithPlan.ok) { eprint(borrowWithPlan.why); return 124; }
    var borrowWithLowered = ssarc.lower(withFunc, [2, 1, 1], borrowWithPlan, irlower.struct_tab_empty());
    if (!borrowWithLowered.ok) { eprint(borrowWithLowered.why); return 106; }
    sawRetain = false;
    for o in borrowWithLowered.ops { if (o.str == "__fern_rc_inc") { sawRetain = true; } }
    if (!sawRetain) { return 107; }
    var badIndex = ssasem.Func { ...withFunc, values: [f.result, strTy, i32ty, f.result], params: [f.result, strTy, i32ty] };
    if (ssaunits.plan(badIndex, [3, 2, 1]).why != "with index type") { return 125; }
    var badWithElem = ssasem.Func { ...withFunc, values: [f.result, i32ty, strTy, f.result], params: [f.result, i32ty, strTy] };
    if (ssaunits.plan(badWithElem, [3, 1, 2]).why != "with element type") { return 126; }
    var strArr: typeinfo.Type = typeinfo.TypeArray { elem: strTy };
    var strWith = ssasem.Func { ...withFunc, values: [strArr, i32ty, strTy, strArr], params: [strArr, i32ty, strTy], result: strArr };
    var strWithPlan = ssaunits.plan(strWith, [3, 1, 3]);
    if (!strWithPlan.ok) { eprint(strWithPlan.why); return 127; }
    var strWithLowered = ssarc.lower(strWith, [3, 1, 3], strWithPlan, irlower.struct_tab_empty());
    if (!strWithLowered.ok) { eprint(strWithLowered.why); return 128; }
    var sawStrFree: boolean = false;
    sawIncElems = false;
    for o in strWithLowered.ops {
        if (o.str == "__fern_arr_inc_elems") { sawIncElems = true; }
        if (o.str == "__fern_str_free") { sawStrFree = true; }
    }
    if (!sawIncElems || !sawStrFree) { return 129; }
    // An element that is not the array's own type, and a result that is not the
    // receiver's, are contract errors rather than lowering ones.
    var badElem = ssasem.Func { ...appendFunc, values: [f.result, strTy, f.result], params: [f.result, strTy] };
    if (ssaunits.plan(badElem, [3, 2]).why != "append element type") { return 44; }
    var badRecvAppend = ssasem.Func { graph: appendGraph, values: [strTy, i32ty, strTy],
        params: [strTy, i32ty], result: strTy, records: [], enums: [], calls: [] };
    if (ssaunits.plan(badRecvAppend, [3, 1]).why != "append container type") { return 45; }
    // A slice is a VIEW: it owns its box and borrows the source's bytes, so it
    // is released by the view helper rather than the ordinary string free —
    // which would skip the immortal rc sentinel and leak the box on the
    // register backends. Its type says so, and a slice typed as an owned
    // string is refused. The slice must DIE here, not be returned: a returned
    // value is handed to the caller, so it has no drop site and would emit no
    // release at all.
    var viewTy: typeinfo.Type = typeinfo.TypeString { tag: 1 };
    var sliceGraph = ssa.SFunc { name: "slice", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.slice(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(1) }] };
    var sliceFunc = ssasem.Func { graph: sliceGraph, values: [strTy, i32ty, i32ty, viewTy],
        params: [strTy, i32ty, i32ty], result: i32ty, records: [], enums: [], calls: [] };
    var ownedSlice = ssasem.Func { ...sliceFunc, values: [strTy, i32ty, i32ty, strTy] };
    if (ssaunits.plan(ownedSlice, [2, 1, 1]).why != "slice container type") { return 103; }
    var slicePlan = ssaunits.plan(sliceFunc, [2, 1, 1]);
    if (!slicePlan.ok) { eprint(slicePlan.why); return 46; }
    var sliceLowered = ssarc.lower(sliceFunc, [2, 1, 1], slicePlan, irlower.struct_tab_empty());
    if (!sliceLowered.ok) { eprint(sliceLowered.why); return 47; }
    var sawSlice: boolean = false;
    var sawViewFree: boolean = false;
    var sawPlainFree: boolean = false;
    for o in sliceLowered.ops {
        if (ir.render_op(o) == "str_slice") { sawSlice = true; }
        if (ir.render_op(o) == "call_direct __fern_str_view_free/1") { sawViewFree = true; }
        if (ir.render_op(o) == "call_direct __fern_str_free/1") { sawPlainFree = true; }
    }
    if (!sawSlice) { return 48; }
    if (!sawViewFree) { return 49; }
    if (sawPlainFree) { return 50; }
    // An ordinary string result keeps the plain free, so the view symbol is
    // selected per VALUE and not applied to every string.
    var plainGraph = ssa.SFunc { name: "plain", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    var plainFunc = ssasem.Func { graph: plainGraph, values: [strTy], params: [strTy], result: strTy,
        records: [], enums: [], calls: [] };
    var plainPlan = ssaunits.plan(plainFunc, [3]);
    if (!plainPlan.ok) { eprint(plainPlan.why); return 51; }
    for o in ssarc.lower(plainFunc, [3], plainPlan, irlower.struct_tab_empty()).ops {
        if (ir.render_op(o) == "call_direct __fern_str_view_free/1") { return 52; }
    }
    // The bounds are i32 and the receiver is a string; neither is negotiable.
    var badBound = ssasem.Func { ...sliceFunc, values: [strTy, strTy, i32ty, viewTy], params: [strTy, strTy, i32ty], result: i32ty };
    if (ssaunits.plan(badBound, [2, 2, 1]).why != "slice bound type") { return 53; }
    // A schema field the walk never reads only has to LAY OUT, not lower. A
    // wide scalar ahead of a reference field shifts nothing, because every
    // backend takes a field's offset from its index on a uniform 8-byte slot,
    // so the helper reads the string at field 1 and never touches field 0.
    var dropGraph = ssa.SFunc { name: "drop", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(1, 1, [], 0)], term: ret(1) }] };
    var f64ty: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: false };
    var wideRec: typeinfo.Type = typeinfo.TypeStruct { name: "Wide", args: [] };
    var wideRecSchema = semrecords.Record { ty: wideRec, fields: [semrecords.Field { name: "d", ty: f64ty },
        semrecords.Field { name: "s", ty: strTy }] };
    var wideFunc = ssasem.Func { graph: dropGraph, values: [wideRec, i32ty], params: [wideRec], result: i32ty,
        records: [wideRecSchema], enums: [], calls: [] };
    var widePlan = ssaunits.plan(wideFunc, [3]);
    if (!widePlan.ok) { eprint(widePlan.why); return 54; }
    var wideLowered = ssarc.lower(wideFunc, [3], widePlan, irlower.struct_tab_empty());
    if (!wideLowered.ok) { eprint(wideLowered.why); return 55; }
    var sawField1: boolean = false;
    var sawField0: boolean = false;
    for h in ssarc.drop_helpers(wideFunc) {
        for o in h.ops {
            if (ir.render_op(o) == "struct_get 1") { sawField1 = true; }
            if (ir.render_op(o) == "struct_get 0") { sawField0 = true; }
        }
    }
    if (!sawField1) { return 56; }
    if (sawField0) { return 57; }
    // A VALUE of that width lowers, in a slot of its own the way an i64 does;
    // only a RECORD construction needs the per-field store width, which this
    // boundary withholds (declaration index -1). The narrower float occupies the
    // same slot, rounded to single precision where it is made.
    var wideVal = ssasem.Func { graph: dropGraph, values: [f64ty, i32ty], params: [f64ty], result: i32ty,
        records: [], enums: [], calls: [] };
    var wideValPlan = ssaunits.plan(wideVal, [1]);
    if (!wideValPlan.ok) { eprint(wideValPlan.why); return 58; }
    var wideValLowered = ssarc.lower(wideVal, [1], wideValPlan, irlower.struct_tab_empty());
    if (!wideValLowered.ok) { eprint(wideValLowered.why); return 60; }
    if (wideValLowered.f64_slots.len() != 1 || wideValLowered.f64_slots[0] != 0) { return 110; }
    var f32ty: typeinfo.Type = typeinfo.TypeFloat { width: 32, polymorphic: false };
    var narrowVal = ssasem.Func { ...wideVal, values: [f32ty, i32ty], params: [f32ty] };
    var narrowValLowered = ssarc.lower(narrowVal, [1], ssaunits.plan(narrowVal, [1]), irlower.struct_tab_empty());
    if (!narrowValLowered.ok) { eprint(narrowValLowered.why); return 111; }
    if (narrowValLowered.f64_slots.len() != 1 || narrowValLowered.f64_slots[0] != 0) { return 111; }
    var floatElem = ssasem.Func { graph: ssa.SFunc { ...dropGraph, nvals: 2, blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            inst(ssasem.array_new(), 1, [0], 0)], term: ret(1) }] },
        values: [f64ty, typeinfo.TypeArray { elem: f64ty }], params: [f64ty], result: typeinfo.TypeArray { elem: f64ty }, records: [], enums: [], calls: [] };
    // An ARRAY element of that width does lower: every element op carries its
    // own slot width, so the construction stores eight bytes and wasm reads
    // back the float form. The i64 shares the stride and takes the integer
    // form, which is what the op's unsigned flag selects.
    var floatElemLowered = ssarc.lower(floatElem, [1], ssaunits.plan(floatElem, [1]), irlower.struct_tab_empty());
    if (!floatElemLowered.ok) { eprint(floatElemLowered.why); return 112; }
    if (!made_wide(floatElemLowered, false)) { return 133; }
    // A float constant carries its text, as a wide integer does.
    var floatK = ssasem.Func { graph: ssa.SFunc { ...dropGraph, nparams: 0, nvals: 1,
            blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 0)], term: ret(0) }] },
        values: [f64ty], params: [], result: f64ty, records: [], enums: [], calls: [] };
    if (ssaunits.plan(floatK, []).why != "float constant needs its literal text") { return 113; }
    // A string index reads its receiver and hands back a scalar. The source
    // DIES at the read here and the result is returned past it, which a
    // projection could never do — the planner would refuse the borrow. That it
    // plans at all is the pin: str_index is not in projects(), so nothing
    // anchors the i32 to the bytes it came from.
    var idxGraph = ssa.SFunc { name: "idx", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.str_index(), result: 2, args: [0, 1], imm: 0, str: "" }], term: ret(2) }] };
    var u8ty: typeinfo.Type = typeinfo.TypeI32 { width: 8, unsigned: true, is_char: false };
    var idxFunc = ssasem.Func { graph: idxGraph, values: [strTy, i32ty, u8ty],
        params: [strTy, i32ty], result: u8ty, records: [], enums: [], calls: [] };
    var idxPlan = ssaunits.plan(idxFunc, [3, 1]);
    if (!idxPlan.ok) { eprint(idxPlan.why); return 61; }
    var idxLowered = ssarc.lower(idxFunc, [3, 1], idxPlan, irlower.struct_tab_empty());
    if (!idxLowered.ok) { eprint(idxLowered.why); return 62; }
    var sawIndex: boolean = false;
    for o in idxLowered.ops { if (ir.render_op(o) == "str_index") { sawIndex = true; } }
    if (!sawIndex) { return 63; }
    // An array receiver has its own projection, and the index is not negotiable.
    var idxArr = ssasem.Func { ...idxFunc, values: [f.result, i32ty, u8ty], params: [f.result, i32ty] };
    if (ssaunits.plan(idxArr, [3, 1]).why != "string index container type") { return 64; }
    var idxBad = ssasem.Func { ...idxFunc, values: [strTy, strTy, u8ty], params: [strTy, strTy] };
    if (ssaunits.plan(idxBad, [3, 2]).why != "string index type") { return 65; }
    // Add, subtract, multiply and shift-left are the operators whose result can
    // leave the range its type names, so each masks back to its own width in a
    // register that is wider than it. Nothing else does: a bitwise op and a
    // shift right stay inside a range their operands are already in, and a
    // comparison lands in a boolean.
    if (binary_masks("+", i32ty, i32ty) != "i32") { eprint(binary_masks("+", i32ty, i32ty)); return 66; }
    if (binary_masks("*", i32ty, i32ty) != "i32") { return 67; }
    if (binary_masks(">>", i32ty, i32ty) != "") { return 68; }
    if (binary_masks("/", i32ty, i32ty) != "") { return 69; }
    // Negation is the same subtraction, so it wraps for the same reason.
    var negGraph = ssa.SFunc { name: "neg", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: 10, result: 1, args: [0], imm: 0, str: "-" }], term: ret(1) }] };
    var negFunc = ssasem.Func { graph: negGraph, values: [i32ty, i32ty], params: [i32ty], result: i32ty,
        records: [], enums: [], calls: [] };
    if (masks(ssarc.lower(negFunc, [1], ssaunits.plan(negFunc, [1]), irlower.struct_tab_empty())) != "i32") { return 70; }
    // The byte is the type the checker gives a string index, so it is not
    // negotiable either: an i32 result is a contract error, not a free widening.
    var idxWide = ssasem.Func { ...idxFunc, values: [strTy, i32ty, i32ty], result: i32ty };
    if (ssaunits.plan(idxWide, [3, 1]).why != "string index result type") { return 71; }
    // A byte masks to eight bits where an i32 masks to thirty-two, and the same
    // operators do it — the width comes from the result type, not the opcode.
    var bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
    if (binary_masks("+", u8ty, u8ty) != "u8") { eprint(binary_masks("+", u8ty, u8ty)); return 72; }
    if (binary_masks("<<", u8ty, u8ty) != "u8") { return 73; }
    if (binary_masks("&", u8ty, u8ty) != "") { return 74; }
    if (binary_masks("<", u8ty, bt) != "") { return 75; }
    // A cast narrows by masking and widens by doing nothing: a byte's producing
    // sites keep it in range, so the value already reads as the wider type.
    if (cast_masks(i32ty, u8ty) != "u8") { return 76; }
    if (cast_masks(u8ty, i32ty) != "") { return 77; }
    if (cast_masks(u8ty, u8ty) != "") { return 78; }
    if (cast_masks(i32ty, i32ty) != "") { return 79; }
    // Into and out of the f64 is a real conversion and never a mask; a
    // reference is not a cast operand at all, and neither is the byte for the
    // float, whose convert has no opcode at that width.
    var f64ty2: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: false };
    if (cast_masks(i32ty, f64ty2) != "") { return 80; }
    if (cast_masks(f64ty2, i32ty) != "") { return 81; }
    if (cast_masks(strTy, i32ty) != "plan:cast operand type") { return 82; }
    if (cast_masks(u8ty, f64ty2) != "plan:cast operand type") { return 114; }
    // A byte's constant is pushed with no mask, so it has to be in range here.
    var kGraph = ssa.SFunc { name: "k", nparams: 0, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 255)], term: ret(0) }] };
    var kFunc = ssasem.Func { graph: kGraph, values: [u8ty], params: [], result: u8ty,
        records: [], enums: [], calls: [] };
    if (!ssaunits.plan(kFunc, []).ok) { eprint(ssaunits.plan(kFunc, []).why); return 83; }
    var kOver = ssasem.Func { ...kFunc, graph: ssa.SFunc { ...kGraph,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 256)], term: ret(0) }] } };
    if (ssaunits.plan(kOver, []).why != "integer constant range") { return 84; }
    // A 64-bit value gets a slot of its own. Its operators run at width 64 and
    // are already full-width, so nothing masks after them; crossing into and
    // out of that domain is an explicit extend and wrap.
    var i64ty: typeinfo.Type = typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false };
    if (binary_masks("+", i64ty, i64ty) != "") { eprint(binary_masks("+", i64ty, i64ty)); return 85; }
    if (binary_masks("<<", i64ty, i64ty) != "") { return 86; }
    if (binary_masks("<", i64ty, bt) != "") { return 87; }
    if (cast_masks(i32ty, i64ty) != "extend") { eprint(cast_masks(i32ty, i64ty)); return 88; }
    if (cast_masks(u8ty, i64ty) != "extend") { return 89; }
    if (cast_masks(i64ty, i32ty) != "wrap") { return 90; }
    if (cast_masks(i64ty, u8ty) != "wrapu8") { eprint(cast_masks(i64ty, u8ty)); return 91; }
    if (cast_masks(i64ty, i64ty) != "") { return 92; }
    // The width-64 operator selection is what a compare at that width needs,
    // so it is read off the OPERANDS rather than off a boolean result.
    var wideBin = ssarc.lower(wide_binary(i64ty, bt, "<"), [1, 1], ssaunits.plan(wide_binary(i64ty, bt, "<"), [1, 1]), irlower.struct_tab_empty());
    if (!wideBin.ok) { eprint(wideBin.why); return 93; }
    var sawWide: boolean = false;
    for o in wideBin.ops { if (o.kind_tag == ir.kind_id("lt_s") && o.width == 64) { sawWide = true; } }
    if (!sawWide) { return 94; }
    // A wide value is an array element and a VALUE, and the array that holds
    // it is one box like any other.
    var wideArr: typeinfo.Type = typeinfo.TypeArray { elem: i64ty };
    var wideArrFunc = ssasem.Func { graph: g, values: [wideArr], params: [wideArr], result: wideArr,
        records: [], enums: [], calls: [] };
    var wideArrLowered = ssarc.lower(wideArrFunc, [2], ssaunits.plan(wideArrFunc, [2]), irlower.struct_tab_empty());
    if (!wideArrLowered.ok) { eprint(wideArrLowered.why); return 95; }
    if (wideArrLowered.arr_slots.len() != 1 || wideArrLowered.arr_slots[0] != 0) { return 134; }
    var wideElem = ssasem.Func { graph: ssa.SFunc { ...dropGraph, nvals: 2, blocks: [ssa.SBlock { id: 7, preds: [],
            insts: [inst(6, 0, [], 0), inst(ssasem.array_new(), 1, [0], 0)], term: ret(1) }] },
        values: [i64ty, wideArr], params: [i64ty], result: wideArr, records: [], enums: [], calls: [] };
    var wideElemLowered = ssarc.lower(wideElem, [1], ssaunits.plan(wideElem, [1]), irlower.struct_tab_empty());
    if (!wideElemLowered.ok) { eprint(wideElemLowered.why); return 135; }
    if (!made_wide(wideElemLowered, true)) { return 136; }
    // A TUPLE element of that width still has none: op_tuple_make spells no
    // element kinds here, so there is no store width to write it at.
    var buildGraph = ssa.SFunc { name: "build", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            inst(ssasem.tuple_new(), 1, [0, 0], 0)], term: ret(1) }] };
    var wideTup: typeinfo.Type = typeinfo.TypeTuple { elements: [i64ty, i64ty] };
    var buildFunc = ssasem.Func { graph: buildGraph, values: [i64ty, wideTup], params: [i64ty], result: wideTup,
        records: [], enums: [], calls: [] };
    if (!refused(ssarc.lower(buildFunc, [1], ssaunits.plan(buildFunc, [1]), irlower.struct_tab_empty()), "unsupported physical RC value type")) { return 96; }
    // The same rule where the container's own type says nothing about it. A
    // record is named by its declaration, so a wide FIELD is invisible to the
    // type check above and to the drop walk, which never reads a scalar field
    // at all — the construction is the one place it shows.
    var narrowTup: typeinfo.Type = typeinfo.TypeTuple { elements: [i32ty, i32ty] };
    var sneakFunc = ssasem.Func { graph: buildGraph, values: [i64ty, narrowTup], params: [i64ty], result: narrowTup,
        records: [], enums: [], calls: [] };
    if (ssaunits.plan(sneakFunc, [1]).ok) { return 97; }
    var wide64Ty: typeinfo.Type = typeinfo.TypeStruct { name: "Wide64", args: [] };
    var wide64Schema = semrecords.Record { ty: wide64Ty, fields: [semrecords.Field { name: "n", ty: i64ty }] };
    var wide64Graph = ssa.SFunc { name: "mkwide", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            inst(ssasem.record_new(), 1, [0], 0)], term: ret(1) }] };
    var wide64Func = ssasem.Func { graph: wide64Graph, values: [i64ty, wide64Ty], params: [i64ty], result: wide64Ty,
        records: [wide64Schema], enums: [], calls: [] };
    var wide64Plan = ssaunits.plan(wide64Func, [1]);
    if (!wide64Plan.ok) { eprint(wide64Plan.why); return 101; }
    // A wide field stores at the width its declaration names, which the
    // construction carries as the declaration's index; with no declaration in
    // the table there is no width to carry, so the construction is refused
    // rather than built through a narrow store.
    if (!refused(ssarc.lower(wide64Func, [1], wide64Plan, irlower.struct_tab_empty()), "unsupported physical RC construction without declaration")) { return 102; }
    var declTab = irlower.struct_tab(parser.parse_module(lexer.tokenize("enum Pair { W(i32) } struct Wide64 { n: i64 } enum Span { W(i64) }")).structs);
    var wide64Lowered = ssarc.lower(wide64Func, [1], wide64Plan, declTab);
    if (!wide64Lowered.ok) { eprint(wide64Lowered.why); return 115; }
    var wide64Decl: i32 = 0 - 1;
    for o in wide64Lowered.ops { if (o.str == "Wide64") { wide64Decl = o.decl; } }
    if (irlower.decl_at_field_type(declTab, wide64Decl, 0) != "i64") { return 116; }
    // A variant resolves within its own enum: two enums declare W here, and
    // the by-name answer is the first-declared one's, whose payload is narrow.
    var spanTy: typeinfo.Type = typeinfo.TypeUnion { name: "Span", args: [] };
    var spanEnum = semrecords.Enum { ty: spanTy, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: i64ty }] }], layout: semrecords.layout_variant() };
    var spanGraph = ssa.SFunc { name: "mkspan", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: ssasem.variant_new(), result: 1, args: [0], imm: 0, str: "W" }], term: ret(1) }] };
    var spanFunc = ssasem.Func { graph: spanGraph, values: [i64ty, spanTy], params: [i64ty], result: spanTy,
        records: [], enums: [spanEnum], calls: [] };
    var spanPlan = ssaunits.plan(spanFunc, [1]);
    if (!spanPlan.ok) { eprint(spanPlan.why); return 117; }
    if (!refused(ssarc.lower(spanFunc, [1], spanPlan, irlower.struct_tab_empty()), "unsupported physical RC construction without declaration")) { return 118; }
    var spanLowered = ssarc.lower(spanFunc, [1], spanPlan, declTab);
    if (!spanLowered.ok) { eprint(spanLowered.why); return 119; }
    var spanDecl: i32 = 0 - 1;
    for o in spanLowered.ops { if (o.str == "W") { spanDecl = o.decl; } }
    if (irlower.decl_at_field_type(declTab, spanDecl, 0) != "i64") { return 120; }
    // A constant with no signed i32 immediate to use carries the literal's
    // text instead; a narrow signed one carries none, and neither may carry both.
    var wideK = ssa.SFunc { name: "wk", nparams: 0, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [ssa.SInst { kind_tag: 1, result: 0, args: [], imm: 0, str: "4294967296" }], term: ret(0) }] };
    var wideKFunc = ssasem.Func { graph: wideK, values: [i64ty], params: [], result: i64ty,
        records: [], enums: [], calls: [] };
    if (!ssaunits.plan(wideKFunc, []).ok) { eprint(ssaunits.plan(wideKFunc, []).why); return 98; }
    var noText = ssasem.Func { ...wideKFunc, graph: ssa.SFunc { ...wideK,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 0)], term: ret(0) }] } };
    if (ssaunits.plan(noText, []).why != "constant needs its literal text") { return 99; }
    var narrowText = ssasem.Func { graph: wideK, values: [i32ty], params: [], result: i32ty,
        records: [], enums: [], calls: [] };
    if (ssaunits.plan(narrowText, []).why != "narrow constant carries text") { return 100; }
    // A u32 occupies the i32's slot but reaches past the immediate's sign bit, so
    // it takes the text form at every value rather than at some of them.
    var u32ty: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: true, is_char: false };
    var u32Text = ssasem.Func { ...wideKFunc, values: [u32ty], result: u32ty };
    if (!ssaunits.plan(u32Text, []).ok) { eprint(ssaunits.plan(u32Text, []).why); return 101; }
    var u32Imm = ssasem.Func { ...u32Text, graph: ssa.SFunc { ...wideK,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 7)], term: ret(0) }] } };
    if (ssaunits.plan(u32Imm, []).why != "constant needs its literal text") { return 102; }
    // A map's box is the raw {keys, vals} pair __fern_map_free_ks frees, with
    // no reference count in it, so a unit of one is LINEAR: the graph below
    // returns a borrowed map, which the return supplies by retaining.
    var mapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: i32ty };
    var mapFunc = ssasem.Func { graph: g, values: [mapTy], params: [mapTy], result: mapTy,
        records: [], enums: [], calls: [] };
    var mapPlan = ssaunits.plan(mapFunc, [2]);
    if (!mapPlan.ok) { eprint(mapPlan.why); return 137; }
    if (!refused(ssarc.lower(mapFunc, [2], mapPlan, irlower.struct_tab_empty()), "map unit is not shared")) { return 138; }
    // The same graph over an OWNED map moves that unit, and the map is freed
    // by its own helper rather than released by a count.
    var ownMapPlan = ssaunits.plan(mapFunc, [3]);
    if (!ownMapPlan.ok) { eprint(ownMapPlan.why); return 139; }
    var ownMapLowered = ssarc.lower(mapFunc, [3], ownMapPlan, irlower.struct_tab_empty());
    if (!ownMapLowered.ok) { eprint(ownMapLowered.why); return 140; }
    for o in ownMapLowered.ops {
        if (o.str == "__fern_rc_inc" || o.str == "__fern_rc_dec") { return 141; }
    }
    // A STRING value column is counted: the map owns a unit of every value and
    // its release walks the column, so the plan admits it where it once refused
    // the whole idea of a reference value.
    var strMapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: strTy };
    var strMapFunc = ssasem.Func { ...mapFunc, values: [strMapTy], params: [strMapTy], result: strMapTy };
    var strMapPlan = ssaunits.plan(strMapFunc, [3]);
    if (!strMapPlan.ok) { eprint(strMapPlan.why); return 142; }
    // What is admitted is the shapes whose deep release the runtime provides —
    // a string column and a column of string arrays — not every reference. A
    // column of i32 arrays has no such walk and stays refused.
    var arrMapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: typeinfo.TypeArray { elem: i32ty } };
    var arrMapFunc = ssasem.Func { ...mapFunc, values: [arrMapTy], params: [arrMapTy], result: arrMapTy };
    if (ssaunits.plan(arrMapFunc, [3]).why != "unsupported counted-unit type") { return 146; }
    // A key that is not a string keys a column that release does not walk.
    var intMapTy: typeinfo.Type = typeinfo.TypeMap { key: i32ty, value: i32ty };
    var intMapFunc = ssasem.Func { ...mapFunc, values: [intMapTy], params: [intMapTy], result: intMapTy };
    if (ssaunits.plan(intMapFunc, [3]).why != "unsupported counted-unit type") { return 143; }
    // A map and a boolean spell different drop helpers: without a key of its
    // own a map would key as the fall-through leaf does.
    if (ssasem.type_key(mapTy) == ssasem.type_key(typeinfo.TypeBool { tag: 0 })) { return 144; }
    if (ssasem.type_key(mapTy) == ssasem.type_key(strMapTy)) { return 145; }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "reject.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "reject.fern", "reject")
	if output, err := runX86_64Bin(runner, driver).CombinedOutput(); err != nil {
		t.Fatalf("physical rejection: %v\n%s", err, output)
	}
}

type physicalRCCase struct{ name, setup, modes string }

func physicalRCCases() []physicalRCCase {
	base := []physicalRCCase{
		{"projected-return", "", ""},
		{"shared-parent", `types = [i, row, rows, pair, rows, i, row];
ops = [inst(1, 0, [], 7), inst(ssasem.array_new(), 1, [0], 0), inst(ssasem.array_new(), 2, [1], 0),
    inst(ssasem.tuple_new(), 3, [2, 2], 0), inst(ssasem.tuple_get(), 4, [3], 0),
    inst(1, 5, [], 0), inst(ssasem.array_get(), 6, [4, 5], 0)]; result = 6;`, ""},
		{"duplicate-child", `ops = ops.with(2, inst(ssasem.array_new(), 2, [1, 1], 0));`, ""},
		{"empty-parent", `types = [i, row, rows]; ops = [inst(1, 0, [], 7), inst(ssasem.array_new(), 1, [0], 0), inst(ssasem.array_new(), 2, [], 0)]; result = 1;`, ""},
		{"unused-deep-parent", `types = [i, row, rows, pair, row]; ops = [inst(1, 0, [], 7), inst(ssasem.array_new(), 1, [0], 0), inst(ssasem.array_new(), 2, [1], 0), inst(ssasem.tuple_new(), 3, [2, 2], 0), inst(ssasem.array_new(), 4, [0], 0)];`, ""},
		{"copied-projection", `types = types.append(row); ops = ops.append(inst(7, 5, [4], 0)); result = 5;`, ""},
		{"mixed-scalar-tuple", `var bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 }; var mixed: typeinfo.Type = typeinfo.TypeTuple { elements: [bt, row] };
types = [i, bt, row, mixed, row]; ops = [inst(1, 0, [], 7), inst(2, 1, [], 1), inst(ssasem.array_new(), 2, [0], 0), inst(ssasem.tuple_new(), 3, [1, 2], 0), inst(ssasem.tuple_get(), 4, [3], 1)];`, ""},
		{"borrowed-parameter", `params = [row]; types = [row]; ops = [inst(6, 0, [], 0)]; result = 0;`, "[2]"},
		{"counted-parameter", `params = [row]; types = [row]; ops = [inst(6, 0, [], 0)]; result = 0;`, "[3]"},
		{"unused-counted-parameter", `params = [row]; types = [row, i, row]; ops = [inst(6, 0, [], 0), inst(1, 1, [], 7), inst(ssasem.array_new(), 2, [1], 0)]; result = 2;`, "[3]"},
	}
	return append(base, physicalRCBranchCases()...)
}

// Compile the compiler modules once, then select fixtures at runtime. This
// keeps the bootstrap and self-host matrix on exactly the same fixture source.
func physicalRCBundle(cases []physicalRCCase) string {
	base := physicalRCSource("", "")
	prefix := strings.Split(base, "function fixture():")[0]
	main := "function main(): i32 {" + strings.SplitN(base, "function main(): i32 {", 2)[1]
	var functions, selection, sources strings.Builder
	selection.WriteString("var selection = args(); var f = fixture_0(); var modes: i32[] = [];\n")
	for i, tc := range cases {
		source := physicalRCSource(tc.setup, tc.modes)
		fixture := "function fixture():" + strings.Split(strings.Split(source, "function fixture():")[1], "function main(): i32 {")[0]
		functions.WriteString(strings.Replace(fixture, "function fixture()", fmt.Sprintf("function fixture_%d()", i), 1))
		modes := tc.modes
		if modes == "" {
			modes = "[]"
		}
		fmt.Fprintf(&selection, "if (selection[2] == %q) { f = fixture_%d(); modes = %s; }\n", tc.name, i, modes)
		if tc.modes != "" {
			literal := strings.Split(strings.Split(source, "var src: string = ")[1], "\n")[0]
			fmt.Fprintf(&sources, "if (selection[2] == %q) { src = %s }\n", tc.name, literal)
		}
	}
	main = strings.Replace(main, "var f = fixture();\n    var modes: i32[] = [];", selection.String(), 1)
	main = strings.Replace(main, "var mod = parser.parse_module", sources.String()+"var mod = parser.parse_module", 1)
	return prefix + functions.String() + main
}

func testPhysicalRC(t *testing.T, selfhost bool) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	cases := physicalRCCases()
	entry := filepath.Join(dir, "physical.fern")
	if err := os.WriteFile(entry, []byte(physicalRCBundle(cases)), 0o644); err != nil {
		t.Fatal(err)
	}
	var driver, armDriverRunner string
	if selfhost {
		cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
		root, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(runner, cli, "-target", "arm64-linux", "-emit", "asm", entry, root)
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("self-host driver compile: %v\n%s", err, diagnostics.String())
		}
		armGCC, armRunner := arm64Tooling(t)
		armDriverRunner = armRunner
		driver = buildBinArm64(t, armGCC, dir, "physical-driver", string(output))
	} else {
		driver = buildSelfHostBin(t, gcc, dir, "physical.fern", "physical-driver")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"arm64-linux", "x86-64-linux", "x86-64-sanitize", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					emitTarget, mode := target, "FERN_LEAKCHECK=1"
					if target == "x86-64-sanitize" {
						emitTarget, mode = "x86-64-linux", "FERN_SANITIZE=1"
					}
					compile := func(t *testing.T, omitRetains bool) []byte {
						t.Helper()
						args := []string{emitTarget, tc.name}
						if omitRetains {
							args = append(args, "omit-retains")
						}
						cmd := runX86_64Bin(runner, driver, args...)
						if selfhost {
							cmd = runArm64Bin(armDriverRunner, driver, args...)
						}
						cmd.Env = append(os.Environ(), mode)
						var diagnostics bytes.Buffer
						cmd.Stderr = &diagnostics
						output, err := cmd.Output()
						if err != nil {
							t.Fatalf("physical lowering: %v\n%s", err, diagnostics.String())
						}
						if omitRetains && !strings.Contains(diagnostics.String(), "omitted physical retains") {
							t.Fatalf("mutation was not applied: %s", diagnostics.String())
						}
						return output
					}
					run := physicalRCRun(t, gcc, runner, dir, tc.name, target, compile(t, false))
					got, err := run.CombinedOutput()
					if err != nil {
						t.Fatalf("physical runtime: %v\n%s", err, got)
					}
					if target != "wasm32-wasi" {
						var allocs, frees, live int64
						summary := leakSummaryLine(string(got))
						if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
							t.Fatal(err)
						}
						if allocs == 0 || allocs != frees || live != 0 {
							t.Fatalf("unbalanced: %s", summary)
						}
					}
					if tc.name == "edge-parent-drop" {
						t.Run("missing-edge-retain", func(t *testing.T) {
							bad := physicalRCRun(t, gcc, runner, dir, tc.name+"-broken", target, compile(t, true))
							output, err := bad.CombinedOutput()
							want := 99 // The post-frame underflow guard.
							if target == "x86-64-sanitize" {
								want = 124 // The runtime sanitizer catches the invalid release earlier.
								if !strings.Contains(string(output), "fern-sanitizer:") {
									t.Fatalf("missing sanitizer diagnostic: %v\n%s", err, output)
								}
							}
							if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != want {
								t.Fatalf("missing edge retain: want lifetime failure %d, got %v\n%s", want, err, output)
							}
						})
					}
				})
			}
		})
	}
}

func physicalRCRun(t *testing.T, gcc string, runner []string, dir, name, target string, output []byte) *exec.Cmd {
	t.Helper()
	switch target {
	case "arm64-linux":
		armGCC, armRunner := arm64Tooling(t)
		return runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, name+"-arm", string(output)))
	case "x86-64-linux", "x86-64-sanitize":
		return runX86_64Bin(runner, buildBin(t, gcc, dir, name+"-x86", string(output)))
	case "wasm32-wasi":
		wat := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(wat, output, 0o644); err != nil {
			t.Fatal(err)
		}
		return exec.Command("wasmtime", "run", wat)
	default:
		t.Fatalf("unknown physical RC target %q", target)
		return nil
	}
}
