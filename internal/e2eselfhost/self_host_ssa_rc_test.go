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
    var lowered = ssarc.lower(f, modes, p);
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
// The width masks one lowered graph emits, concatenated — "" when it emits
// none. A contract the planner refuses comes back as its reason instead, so
// one helper covers both what an operation is allowed to be and what it
// lowers to.
function masks(r: irlower.LowerResult): string {
    if (!r.ok) { return "lower:" + r.why; }
    var out: string = "";
    for o in r.ops { if (o.kind_tag == ir.kind_id("int_cast")) { out = out + o.str; } }
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
    return masks(ssarc.lower(f, [1, 1], p));
}
function cast_masks(from: typeinfo.Type, to: typeinfo.Type): string {
    var g = ssa.SFunc { name: "cast", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: ssasem.cast(), result: 1, args: [0], imm: 0, str: "" }], term: ret(1) }] };
    var f = ssasem.Func { graph: g, values: [from, to], params: [from], result: to,
        records: [], enums: [], calls: [] };
    var p = ssaunits.plan(f, [1]);
    if (!p.ok) { return "plan:" + p.why; }
    return masks(ssarc.lower(f, [1], p));
}
function main(): i32 {
    var f = fixture();
    var p = ssaunits.plan(f, []);
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, ok: false }), "missing successful unit plan")) { return 1; }
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, steps: [] }), "missing or duplicate entry step")) { return 2; }
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
    if (!refused(ssarc.lower(cfg, [], cp), "physical RC needs reducible graph")) { return 4; }
    // A wide array has no stack representation here; a string does now.
    var wide: typeinfo.Type = typeinfo.TypeArray { elem: typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false } };
    var g = ssa.SFunc { name: "unsupported", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    var typed = ssasem.Func { graph: g, values: [wide], params: [wide], result: wide, records: [], enums: [], calls: [] };
    var plan = ssaunits.plan(typed, [2]);
    if (!plan.ok) { eprint(plan.why); return 5; }
    if (!refused(ssarc.lower(typed, [2], plan), "unsupported physical RC value type")) { return 6; }
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
    if (!refused(ssarc.lower(genericFunc, [2], genericPlan), "unsupported physical RC value type")) { return 8; }
    // A wide array is not such a field. The walk visits only the REFERENCE
    // fields, and an array of scalars has no element to visit, so it needs its
    // own box released and nothing more — even though a wide array VALUE stays
    // out, as the pin above holds.
    var wideField = semrecords.Record { ty: recordType, fields: [semrecords.Field { name: "xs", ty: f.result },
        semrecords.Field { name: "ns", ty: wide }] };
    var wideFieldFunc = ssasem.Func { graph: g, values: [recordType], params: [recordType], result: recordType, records: [wideField], enums: [], calls: [] };
    var wideFieldPlan = ssaunits.plan(wideFieldFunc, [2]);
    if (!wideFieldPlan.ok) { eprint(wideFieldPlan.why); return 9; }
    if (!ssarc.lower(wideFieldFunc, [2], wideFieldPlan).ok) { return 10; }
    var recordFunc = ssasem.Func { graph: recordGraph, values: [f.result, recordType], params: [f.result], result: recordType, records: [schema], enums: [], calls: [] };
    var recordPlan = ssaunits.plan(recordFunc, [2]);
    if (!recordPlan.ok) { eprint(recordPlan.why); return 11; }
    if (!ssarc.lower(recordFunc, [2], recordPlan).ok) { return 12; }
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
    if (!refused(ssarc.lower(wideEnumFunc, [2], wideEnumPlan), "unsupported physical RC variant field type")) { return 14; }
    var walkableEnum = semrecords.Enum { ...wideEnum, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] },
        semrecords.Variant { name: "N", fields: [semrecords.Field { name: "__ev", ty: wide }] }] };
    var walkableEnumFunc = ssasem.Func { ...wideEnumFunc, enums: [walkableEnum] };
    if (!ssarc.lower(walkableEnumFunc, [2], ssaunits.plan(walkableEnumFunc, [2])).ok) { return 59; }
    var arrayEnum = semrecords.Enum { ty: shapeType, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] }], layout: semrecords.layout_variant() };
    var enumFunc = ssasem.Func { graph: enumGraph, values: [f.result, shapeType], params: [f.result], result: shapeType, records: [], enums: [arrayEnum], calls: [] };
    var enumPlan = ssaunits.plan(enumFunc, [2]);
    if (!enumPlan.ok) { eprint(enumPlan.why); return 15; }
    if (!ssarc.lower(enumFunc, [2], enumPlan).ok) { return 16; }
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
    var selfLowered = ssarc.lower(selfFunc, [2], selfPlan);
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
    if (util.index_of_str(ssarc.caller_sigs(irlower.fn_sigs_empty(), "mk", boxOnlyFunc).return_fresh_struct_ret_fns, "mk") < 0) { return 30; }
    // A reference field is exactly what makes that sweep dangerous, so the same
    // shape one field over gets nothing and keeps the leak floor.
    var withKids: typeinfo.Type = typeinfo.TypeStruct { name: "WithKids", args: [] };
    var withKidsSchema = semrecords.Record { ty: withKids, fields: [semrecords.Field { name: "xs", ty: f.result }] };
    var withKidsFunc = ssasem.Func { graph: selfGraph, values: [withKids], params: [withKids], result: withKids, records: [withKidsSchema], enums: [], calls: [] };
    if (util.index_of_str(ssarc.caller_sigs(irlower.fn_sigs_empty(), "mk", withKidsFunc).return_fresh_struct_ret_fns, "mk") >= 0) { return 31; }
    // A length reads its receiver and hands back an i32 that owns nothing: an
    // array selects arr_len, a string str_len, and a receiver that is neither
    // is not a counted container this can read at all.
    var lenGraph = ssa.SFunc { name: "len", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.length(), 1, [0], 0)], term: ret(1) }] };
    var arrLen = ssasem.Func { graph: lenGraph, values: [f.result, i32ty], params: [f.result], result: i32ty, records: [], enums: [], calls: [] };
    var arrLenPlan = ssaunits.plan(arrLen, [2]);
    if (!arrLenPlan.ok) { eprint(arrLenPlan.why); return 32; }
    var arrLenLowered = ssarc.lower(arrLen, [2], arrLenPlan);
    if (!arrLenLowered.ok) { eprint(arrLenLowered.why); return 33; }
    var sawArrLen: boolean = false;
    for o in arrLenLowered.ops { if (ir.render_op(o) == "arr_len") { sawArrLen = true; } }
    if (!sawArrLen) { return 34; }
    var strTy: typeinfo.Type = typeinfo.TypeString { tag: 0 };
    var strLen = ssasem.Func { graph: lenGraph, values: [strTy, i32ty], params: [strTy], result: i32ty, records: [], enums: [], calls: [] };
    var strLenPlan = ssaunits.plan(strLen, [2]);
    if (!strLenPlan.ok) { eprint(strLenPlan.why); return 35; }
    var strLenLowered = ssarc.lower(strLen, [2], strLenPlan);
    if (!strLenLowered.ok) { eprint(strLenLowered.why); return 36; }
    var sawStrLen: boolean = false;
    for o in strLenLowered.ops { if (ir.render_op(o) == "str_len") { sawStrLen = true; } }
    if (!sawStrLen) { return 37; }
    var badRecv = ssasem.Func { graph: lenGraph, values: [boxOnly, i32ty], params: [boxOnly], result: i32ty, records: [boxOnlySchema], enums: [], calls: [] };
    if (ssaunits.plan(badRecv, [2]).why != "length container type") { return 38; }
    var badResult = ssasem.Func { graph: lenGraph, values: [f.result, f.result], params: [f.result], result: f.result, records: [], enums: [], calls: [] };
    if (ssaunits.plan(badResult, [2]).why != "length result type") { return 39; }
    // An append takes the receiver's unit and hands one back, so it is admitted
    // only where that unit is MOVED. A counted receiver dead after the push is;
    // a borrowed one is not, and is refused rather than lowered.
    var appendGraph = ssa.SFunc { name: "append", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.append(), result: 2, args: [0, 1], imm: 0, str: "" }], term: ret(2) }] };
    var appendFunc = ssasem.Func { graph: appendGraph, values: [f.result, i32ty, f.result],
        params: [f.result, i32ty], result: f.result, records: [], enums: [], calls: [] };
    var appendPlan = ssaunits.plan(appendFunc, [3, 1]);
    if (!appendPlan.ok) { eprint(appendPlan.why); return 40; }
    var appendLowered = ssarc.lower(appendFunc, [3, 1], appendPlan);
    if (!appendLowered.ok) { eprint(appendLowered.why); return 41; }
    var sawPush: boolean = false;
    for o in appendLowered.ops { if (ir.render_op(o) == "arr_push_owned") { sawPush = true; } }
    if (!sawPush) { return 42; }
    // The same graph with a BORROWED receiver: its unit is not this function's
    // to hand over, so the plan refuses instead of aliasing the result onto it.
    if (ssaunits.plan(appendFunc, [2, 1]).why != "append receiver is not consumed") { return 43; }
    // An element that is not the array's own type, and a result that is not the
    // receiver's, are contract errors rather than lowering ones.
    var badElem = ssasem.Func { ...appendFunc, values: [f.result, strTy, f.result], params: [f.result, strTy] };
    if (ssaunits.plan(badElem, [3, 2]).why != "append element type") { return 44; }
    var badRecvAppend = ssasem.Func { graph: appendGraph, values: [strTy, i32ty, strTy],
        params: [strTy, i32ty], result: strTy, records: [], enums: [], calls: [] };
    if (ssaunits.plan(badRecvAppend, [3, 1]).why != "append container type") { return 45; }
    // A slice owns its box and borrows the source's bytes, so it is released by
    // the view helper rather than the ordinary string free — which would skip
    // the immortal rc sentinel and leak the box on the register backends.
    // The slice must DIE here, not be returned: a returned value is handed to
    // the caller, so it has no drop site and would emit no release at all.
    var sliceGraph = ssa.SFunc { name: "slice", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.slice(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(1) }] };
    var sliceFunc = ssasem.Func { graph: sliceGraph, values: [strTy, i32ty, i32ty, strTy],
        params: [strTy, i32ty, i32ty], result: i32ty, records: [], enums: [], calls: [] };
    var slicePlan = ssaunits.plan(sliceFunc, [2, 1, 1]);
    if (!slicePlan.ok) { eprint(slicePlan.why); return 46; }
    var sliceLowered = ssarc.lower(sliceFunc, [2, 1, 1], slicePlan);
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
    for o in ssarc.lower(plainFunc, [3], plainPlan).ops {
        if (ir.render_op(o) == "call_direct __fern_str_view_free/1") { return 52; }
    }
    // The bounds are i32 and the receiver is a string; neither is negotiable.
    var badBound = ssasem.Func { ...sliceFunc, values: [strTy, strTy, i32ty, strTy], params: [strTy, strTy, i32ty], result: i32ty };
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
    var wideLowered = ssarc.lower(wideFunc, [3], widePlan);
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
    // A VALUE of that width stays out. Only a construction needs the per-field
    // store width, and this boundary withholds it (declaration index -1), so
    // admitting one would store a double through an i32 slot on wasm.
    var wideVal = ssasem.Func { graph: dropGraph, values: [f64ty, i32ty], params: [f64ty], result: i32ty,
        records: [], enums: [], calls: [] };
    var wideValPlan = ssaunits.plan(wideVal, [1]);
    if (!wideValPlan.ok) { eprint(wideValPlan.why); return 58; }
    var wideValWhy: string = ssarc.lower(wideVal, [1], wideValPlan).why;
    if (wideValWhy != "unsupported physical RC value type") { eprint(wideValWhy); return 60; }
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
    var idxLowered = ssarc.lower(idxFunc, [3, 1], idxPlan);
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
    if (masks(ssarc.lower(negFunc, [1], ssaunits.plan(negFunc, [1]))) != "i32") { return 70; }
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
    // Both ends are integers. A cast is a reinterpretation of one slot, which
    // a float or a reference is not.
    var f64ty2: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: false };
    if (cast_masks(i32ty, f64ty2) != "plan:cast result type") { return 80; }
    if (cast_masks(f64ty2, i32ty) != "plan:cast operand type") { return 81; }
    if (cast_masks(strTy, i32ty) != "plan:cast operand type") { return 82; }
    // A byte's constant is pushed with no mask, so it has to be in range here.
    var kGraph = ssa.SFunc { name: "k", nparams: 0, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 255)], term: ret(0) }] };
    var kFunc = ssasem.Func { graph: kGraph, values: [u8ty], params: [], result: u8ty,
        records: [], enums: [], calls: [] };
    if (!ssaunits.plan(kFunc, []).ok) { eprint(ssaunits.plan(kFunc, []).why); return 83; }
    var kOver = ssasem.Func { ...kFunc, graph: ssa.SFunc { ...kGraph,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 256)], term: ret(0) }] } };
    if (ssaunits.plan(kOver, []).why != "integer constant range") { return 84; }
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
