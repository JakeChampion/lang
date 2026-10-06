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
// Explicit caller contract for ABI tests. The typed caller reads the
// contract of the source's produce, so a hand-built callee with other modes
// gets a caller whose supplies are written out to match them.
function caller(template: irtables.LowerResult, mode: i32): irtables.LowerResult {
    let ops: ir.Op[] = [ir.op_const_i32(7), ir.op_arr_make(1, 32), ir.op_store_local(0),
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
    let slot: i32 = 0;
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
    return irtables.LowerResult { ...template, ops: ops, n_locals: 3,
        n_params: 0, arr_slots: [0, 1], str_slots: [], i64_slots: [], f64_slots: [] };
}
function fixture(): ssasem.Func {
    let i: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
    let row: typeinfo.Type = typeinfo.TypeArray { elem: i, view: false };
    let rows: typeinfo.Type = typeinfo.TypeArray { elem: row, view: false };
    let pair: typeinfo.Type = typeinfo.TypeTuple { elements: [rows, rows] };
    let types: typeinfo.Type[] = [i, row, rows, i, row];
    let ops: ssa.SInst[] = [inst(1, 0, [], 7), inst(ssasem.array_new(), 1, [0], 0),
        inst(ssasem.array_new(), 2, [1], 0), inst(1, 3, [], 0), inst(ssasem.array_get(), 4, [2, 3], 0)];
    let result: i32 = 4;
    let params: typeinfo.Type[] = [];
    let blocks: ssa.SBlock[] = [];
    FIXTURE
    let graph = ssa.SFunc { name: "produce", nparams: params.len(), nvals: types.len(), entry: 7,
        takes_env: false, blocks: [ssa.SBlock { id: 7, preds: [], insts: ops, term: ret(result) }] };
    if (blocks.len() > 0) { graph = ssa.SFunc { ...graph, blocks: blocks }; }
    return ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: graph, values: types, params: params, result: row, records: semrecords.no_records(), enums: [], calls: [] };
}
function main(): i32 {
    let f = fixture();
    let modes: i32[] = MODES;
    let p = ssaunits.plan(f, modes, ssaunits.no_view());
    // Every row is built on the heap: a static box is immortal, so a lost retain goes undetected and missing-edge-retain would stop failing.
    let none: string[] = [];
    for c in p.constants { none = none.append(""); }
    p = ssaunits.Plan { ...p, constants: none };
    let lowered = ssarc.lower(f, modes, p, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!lowered.ok) { eprint(lowered.why); return 1; }
    let options = args();
    if (options.len() > 3 && options[3] == "omit-retains") {
        let broken: ir.Op[] = [];
        let removed: boolean = false;
        for op in lowered.ops {
            // rc_inc returns its input. Keeping the load and following drop
            // preserves stack shape while deliberately omitting the count.
            if (op.str != "__fern_rc_inc") { broken = broken.append(op); }
            else { removed = true; }
        }
        if (!removed) { eprint("no physical retain to omit"); return 4; }
        eprint("omitted physical retains\n");
        lowered = irtables.LowerResult { ...lowered, ops: broken };
    }
    let src: string = "function produce(): i32[] { return [7]; } @noinline function exercise(): i32 { let j = 0; while (j < 32) { let xs = produce(); let churn = [91, 92, 93]; if (xs.len() != 1 || xs[0] != 7 || churn[0] != 91) { return 2; } j = j + 1; } return 0; } function main(): i32 { let result = exercise(); if (result != 0) { return result; } if (__rc_underflow_count() != 0) { return 99; } return 0; }";
    let mod = parser.parse_module(lexer.tokenize(src));
    let av = args();
    // The rest of the program is the typed lowering's, as the emit entries
    // gate it; produce's body is the fixture's.
    let d = semlower.driven(mod, av[1]);
    let g: ircore.Gated = ircore.Gated { ok: false, im: d.full, stab: irtables.struct_tab_empty(), cache: [] };
    if (av[1] == "wasm32-wasi") {
        let wm = ircore.with_records(wasm_ir.route_normalized(d.full), d.sub);
        let l = ircore.gate(wm, d.sub);
        g = ircore.Gated { ok: l.ok, im: wm, stab: irtables.struct_tab(wm.structs), cache: l.cache };
    } else { g = semlower.program(d); }
    if (!g.ok) { return 3; }
    let cache: irtables.LowerResult[] = [];
    let at: i32 = 0;
    while (at < g.cache.len()) {
        let name: string = "";
        if (at < g.im.funcs.len()) { name = g.im.funcs[at].name; }
        if (name == "produce") { cache = cache.append(lowered); }
        else if (name == "exercise" && modes.len() > 0 && modes[0] != 1) { cache = cache.append(caller(g.cache[at], modes[0])); }
        else { cache = cache.append(g.cache[at]); }
        at = at + 1;
    }
    if (av[1] == "x86-64-linux") {
        print(asm_ir.emit_module_ir_unit_flat(g.im, true, false, "", [], g.im.funcs, g.stab, 0, 0 - 1, cache, d.sub.rt_lower, 0 as usize, asmcore.env_switches()));
    } else if (av[1] == "arm64-linux") {
        strbuf_reset();
        let state = asmcore.new_state();
        state = asmcore.EmitState { ...state, struct_decls: g.stab, funcs: g.im.funcs, rt_lower: d.sub.rt_lower };
        state = asm_arm64_ir.emit_body(g.im, state, false, cache);
        state = asm_arm64_ir.emit_ir_runtime(state, false);
        print(strbuf_take());
    } else { print(wasm_ir.emit_ir_module_mode(g.im, cache, 0, asmcore.env_switches())); }
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
		source = strings.Replace(source, "let xs = produce();", "let xs = produce("+arguments+");", 1)
		source = strings.Replace(source, "xs[0] != 7", "xs[0] != "+expected, 1)
	} else if modes != "[]" {
		parameter := "seed: i32[]"
		if modes == "[3]" {
			parameter = "own " + parameter
		}
		source = strings.Replace(source, "function produce(): i32[] { return [7]; }", "function produce("+parameter+"): i32[] { return seed; }", 1)
		source = strings.Replace(source, "let j = 0; while", "let seed = [7]; let before = seed; let j = 0; while", 1)
		source = strings.Replace(source, "let xs = produce();", "let xs = produce(seed);", 1)
		source = strings.Replace(source, "churn[0] != 91", "churn[0] != 91 || before[0] != 7 || seed[0] != 7", 1)
	}
	source = strings.Replace(source, `let src: string = "function produce`, `let src: string = "@noinline function produce`, 1)
	return `import "./ssarc"; import "./suspend"; import "./ssasem"; import "./ssaunits"; import "./ssa";
import "./typeinfo"; import "./semrecords"; import "./parser"; import "./lexer"; import "./irtables";
import "./ir"; import "./util";
import "./ircore"; import "./asmcore"; import "./asm_ir"; import "./asm_arm64_ir"; import "./wasm_ir"; import "./irverifyrc";
import "./semlower";
` + source
}

func TestSelfHostSSAPhysicalRC(t *testing.T) {
	testPhysicalRC(t, false)
}

func TestSelfHostSSAPhysicalRCIRArm64(t *testing.T) {
	testPhysicalRC(t, true)
}

// physicalRCRejectSource is the contract-rejection program: the fixture prefix
// plus a main that asserts every refusal. Built apart from its test so the
// fixture gate can type-check it without the x86_64 tooling.
func physicalRCRejectSource() string {
	return strings.Split(physicalRCSource("", ""), "function main(): i32 {")[0] + `
function refused(r: irtables.LowerResult, why: string): boolean {
    return !r.ok && r.why == why && r.ops.len() == 0 && r.n_locals == 0 && r.n_params == 0;
}
function has_sub(all: string, needle: string): boolean {
    let i: i32 = 0;
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
function made_wide(r: irtables.LowerResult, integer: boolean): boolean {
    for o in r.ops {
        if (o.kind_tag == ir.kind_id("arr_make")) { return o.width == 64 && o.unsigned == integer; }
    }
    return false;
}
// The width conversions one lowered graph emits, concatenated — "" when it
// emits none. A contract the planner refuses comes back as its reason instead,
// so one helper covers both what an operation is allowed to be and what it
// lowers to.
function masks(r: irtables.LowerResult): string {
    if (!r.ok) { return "lower:" + r.why; }
    let out: string = "";
    for o in r.ops {
        if (o.kind_tag == ir.kind_id("int_cast")) { out = out + o.str; }
        if (o.kind_tag == ir.kind_id("int_extend")) { out = out + "extend"; }
        if (o.kind_tag == ir.kind_id("int_wrap")) { out = out + "wrap"; }
    }
    return out;
}
function binary_masks(op: string, t: typeinfo.Type, result: typeinfo.Type): string {
    let g = ssa.SFunc { name: "bin", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: 9, result: 2, args: [0, 1], imm: 0, str: op }], term: ret(2) }] };
    let f = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [t, t, result], params: [t, t], result: result,
        records: semrecords.no_records(), enums: [], calls: [] };
    let p = ssaunits.plan(f, [1, 1], ssaunits.no_view());
    if (!p.ok) { return "plan:" + p.why; }
    return masks(ssarc.lower(f, [1, 1], p, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()));
}
function wide_binary(t: typeinfo.Type, result: typeinfo.Type, op: string): ssasem.Func {
    let g = ssa.SFunc { name: "wbin", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: 9, result: 2, args: [0, 1], imm: 0, str: op }], term: ret(2) }] };
    return ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [t, t, result], params: [t, t], result: result,
        records: semrecords.no_records(), enums: [], calls: [] };
}
function cast_masks(from: typeinfo.Type, to: typeinfo.Type): string {
    let g = ssa.SFunc { name: "cast", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: ssasem.cast(), result: 1, args: [0], imm: 0, str: "" }], term: ret(1) }] };
    let f = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [from, to], params: [from], result: to,
        records: semrecords.no_records(), enums: [], calls: [] };
    let p = ssaunits.plan(f, [1], ssaunits.no_view());
    if (!p.ok) { return "plan:" + p.why; }
    return masks(ssarc.lower(f, [1], p, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()));
}
function main(): i32 {
    let f = fixture();
    let p = ssaunits.plan(f, [], ssaunits.no_view());
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, ok: false }, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()), "missing successful unit plan")) { return 1; }
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, steps: [] }, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()), "missing or duplicate entry step")) { return 2; }
    let b = f.graph.blocks[0];
    let first = ssa.SBlock { ...b, insts: b.insts.append(inst(2, 5, [], 0)), term: ssa.STerm { kind_tag: 2, target: 27, value: 0, cond: 0, t: 0, f: 0 } };
    // Two blocks entering each other with neither dominating: a cycle no
    // single loop label can head.
    first = ssa.SBlock { ...first, term: ssa.STerm { kind_tag: 3, target: 0, value: 0, cond: 5, t: 27, f: 37 } };
    let left = ssa.SBlock { id: 27, preds: [7, 37], insts: [], term: ssa.STerm { kind_tag: 2, target: 37, value: 0, cond: 0, t: 0, f: 0 } };
    let right = ssa.SBlock { id: 37, preds: [7, 27], insts: [], term: ssa.STerm { kind_tag: 3, target: 0, value: 0, cond: 5, t: 27, f: 47 } };
    let last = ssa.SBlock { id: 47, preds: [37], insts: [], term: b.term };
    let graph = ssa.SFunc { ...f.graph, nvals: 6, blocks: [first, left, right, last] };
    let cfg = ssasem.Func { ...f, graph: graph, values: f.values.append(typeinfo.TypeBool { tag: 0 }) };
    let cp = ssaunits.plan(cfg, [], ssaunits.no_view());
    if (!cp.ok) { eprint(cp.why); return 3; }
    if (!refused(ssarc.lower(cfg, [], cp, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()), "physical RC needs reducible graph")) { return 4; }
    // An array of a 64-bit element: its ops carry the eight-byte stride, so it
    // is a value here like any other array.
    let wide: typeinfo.Type = typeinfo.TypeArray { elem: typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false, polymorphic: false }, view: false };
    let g = ssa.SFunc { name: "unsupported", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    let typed = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [wide], params: [wide], result: wide, records: semrecords.no_records(), enums: [], calls: [] };
    let plan = ssaunits.plan(typed, [2], ssaunits.no_view());
    if (!plan.ok) { eprint(plan.why); return 5; }
    if (!ssarc.lower(typed, [2], plan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 6; }
    // A record instance with type arguments is found in the schema table by
    // its whole type, arguments included, so it lowers as a plain record does.
    let wideType: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [wide] };
    let wideSchema = semrecords.Record { views: false, ty: wideType, fields: [semrecords.Field { name: "xs", ty: f.result }] };
    let recordType: typeinfo.Type = typeinfo.TypeStruct { name: "Box", args: [] };
    let schema = semrecords.Record { views: false, ty: recordType, fields: [semrecords.Field { name: "xs", ty: f.result }] };
    let recordGraph = ssa.SFunc { name: "record", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.record_new(), 1, [0], 0)], term: ret(1) }] };
    let genericFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: recordGraph, values: [f.result, wideType], params: [f.result], result: wideType, records: semrecords.records_of([wideSchema]), enums: [], calls: [] };
    let genericPlan = ssaunits.plan(genericFunc, [2], ssaunits.no_view());
    if (!genericPlan.ok) { eprint(genericPlan.why); return 7; }
    if (!ssarc.lower(genericFunc, [2], genericPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 8; }
    // A map cursor holds a unit of its map, so the planner admits it exactly
    // when it admits the map: a key or a value record with no schema in the
    // table refuses the cursor as it refuses the map.
    let cursorType: typeinfo.Type = typeinfo.TypeStruct { name: "MapIter", args: [typeinfo.TypeStruct { name: "Missing", args: [] }, typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false }] };
    let cursorFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [cursorType], params: [cursorType], result: cursorType, records: semrecords.no_records(), enums: [], calls: [] };
    let cursorMap: typeinfo.Type = typeinfo.TypeMap { key: typeinfo.TypeStruct { name: "Missing", args: [] }, value: typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false } };
    let cursorMapPlan = ssaunits.plan(ssasem.Func { ...cursorFunc, values: [cursorMap], params: [cursorMap], result: cursorMap }, [2], ssaunits.no_view());
    let cursorPlan = ssaunits.plan(cursorFunc, [2], ssaunits.no_view());
    if (cursorMapPlan.ok || cursorPlan.ok || cursorPlan.why != cursorMapPlan.why) { eprint(cursorPlan.why); return 211; }
    let valueCursorType: typeinfo.Type = typeinfo.TypeStruct { name: "MapIter", args: [typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false }, typeinfo.TypeStruct { name: "Missing", args: [] }] };
    let valueCursorFunc = ssasem.Func { ...cursorFunc, values: [valueCursorType], params: [valueCursorType], result: valueCursorType };
    if (ssaunits.plan(valueCursorFunc, [2], ssaunits.no_view()).ok) { return 213; }
    // So does a wide array field. The walk visits only the REFERENCE
    // fields, and an array of scalars has no element to visit, so it needs its
    // own box released and nothing more.
    let wideField = semrecords.Record { views: false, ty: recordType, fields: [semrecords.Field { name: "xs", ty: f.result },
        semrecords.Field { name: "ns", ty: wide }] };
    let wideFieldFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [recordType], params: [recordType], result: recordType, records: semrecords.records_of([wideField]), enums: [], calls: [] };
    let wideFieldPlan = ssaunits.plan(wideFieldFunc, [2], ssaunits.no_view());
    if (!wideFieldPlan.ok) { eprint(wideFieldPlan.why); return 9; }
    if (!ssarc.lower(wideFieldFunc, [2], wideFieldPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 10; }
    let recordFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: recordGraph, values: [f.result, recordType], params: [f.result], result: recordType, records: semrecords.records_of([schema]), enums: [], calls: [] };
    let recordPlan = ssaunits.plan(recordFunc, [2], ssaunits.no_view());
    if (!recordPlan.ok) { eprint(recordPlan.why); return 11; }
    if (!ssarc.lower(recordFunc, [2], recordPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 12; }
    // A variant payload that is a record instance, or a wide array, is walkable
    // for the same reason the record field is, on a variant the graph never
    // builds as well as on the one it does.
    let shapeType: typeinfo.Type = typeinfo.TypeUnion { name: "Shape", args: [] };
    let enumGraph = ssa.SFunc { name: "enum", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), ssa.SInst { kind_tag: ssasem.variant_new(), result: 1, args: [0], imm: 0, str: "W" }], term: ret(1) }] };
    let wideEnum = semrecords.Enum { views: false, ty: shapeType, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] },
        semrecords.Variant { name: "N", fields: [semrecords.Field { name: "__ev", ty: wideType }] }], layout: semrecords.layout_variant() };
    let wideEnumFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: enumGraph, values: [f.result, shapeType], params: [f.result], result: shapeType, records: semrecords.records_of([wideSchema]), enums: [wideEnum], calls: [] };
    let wideEnumPlan = ssaunits.plan(wideEnumFunc, [2], ssaunits.no_view());
    if (!wideEnumPlan.ok) { eprint(wideEnumPlan.why); return 13; }
    if (!ssarc.lower(wideEnumFunc, [2], wideEnumPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 14; }
    let walkableEnum = semrecords.Enum { ...wideEnum, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] },
        semrecords.Variant { name: "N", fields: [semrecords.Field { name: "__ev", ty: wide }] }] };
    let walkableEnumFunc = ssasem.Func { ...wideEnumFunc, enums: [walkableEnum] };
    if (!ssarc.lower(walkableEnumFunc, [2], ssaunits.plan(walkableEnumFunc, [2], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 59; }
    let arrayEnum = semrecords.Enum { views: false, ty: shapeType, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: f.result }] }], layout: semrecords.layout_variant() };
    let enumFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: enumGraph, values: [f.result, shapeType], params: [f.result], result: shapeType, records: semrecords.no_records(), enums: [arrayEnum], calls: [] };
    let enumPlan = ssaunits.plan(enumFunc, [2], ssaunits.no_view());
    if (!enumPlan.ok) { eprint(enumPlan.why); return 15; }
    if (!ssarc.lower(enumFunc, [2], enumPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ok) { return 16; }
    // A type that reaches itself has no finite INLINE expansion, so its
    // children are released by a per-type helper the walk calls, and a site
    // releases the whole value through a second one. The pair calls each other,
    // which is what makes the descent finite.
    let selfType: typeinfo.Type = typeinfo.TypeStruct { name: "Node", args: [] };
    let selfSchema = semrecords.Record { views: false, ty: selfType, fields: [semrecords.Field { name: "kid", ty: selfType }] };
    let selfGraph = ssa.SFunc { name: "cycle", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    let selfFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: selfGraph, values: [selfType], params: [selfType], result: selfType, records: semrecords.records_of([selfSchema]), enums: [], calls: [] };
    let selfPlan = ssaunits.plan(selfFunc, [2], ssaunits.no_view());
    if (!selfPlan.ok) { eprint(selfPlan.why); return 17; }
    let selfLowered = ssarc.lower(selfFunc, [2], selfPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!selfLowered.ok) { eprint(selfLowered.why); return 18; }
    let selfHelpers = ssarc.drop_helpers(selfFunc);
    if (selfHelpers.len() != 2) { return 19; }
    if (selfHelpers[0].name != "__sem_drop_Node") { eprint(selfHelpers[0].name); return 20; }
    if (selfHelpers[1].name != "__sem_release_Node") { eprint(selfHelpers[1].name); return 200; }
    if (selfHelpers[0].n_params != 1 || selfHelpers[1].n_params != 1) { return 21; }
    // n_locals covers the parameter even before a child slot is reserved.
    if (selfHelpers[0].n_locals < 1 || selfHelpers[1].n_locals < 1) { return 22; }
    let dropCallsRelease: boolean = false;
    for o in selfHelpers[0].ops {
        if (ir.render_op(o) == "call_direct __sem_release_Node/1") { dropCallsRelease = true; }
    }
    let releaseCallsDrop: boolean = false;
    for o in selfHelpers[1].ops {
        if (ir.render_op(o) == "call_direct __sem_drop_Node/1") { releaseCallsDrop = true; }
    }
    if (!dropCallsRelease || !releaseCallsDrop) { return 23; }
    // A schema with no reference field needs no helper, so none is emitted:
    // a body exists exactly when a call to it does.
    let flatType: typeinfo.Type = typeinfo.TypeStruct { name: "Flat", args: [] };
    let flatSchema = semrecords.Record { views: false, ty: flatType, fields: [semrecords.Field { name: "n", ty: typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false } }] };
    let flatFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: selfGraph, values: [flatType], params: [flatType], result: flatType, records: semrecords.records_of([flatSchema]), enums: [], calls: [] };
    if (ssarc.drop_helpers(flatFunc).len() != 0) { return 24; }
    // Two functions that both name one type produce its helpers once: the
    // module's collection skips a symbol it already carries, and the merge
    // keeps the first of a symbol handed to it twice rather than two.
    let cycle2 = ssasem.Func { ...selfFunc, graph: ssa.SFunc { ...selfGraph, name: "cycle2" } };
    let collected = ssarc.with_drop_helpers(ssarc.with_drop_helpers(ssarc.no_helpers(), selfFunc), cycle2);
    if (collected.rows.len() != 2) { return 25; }
    if (collected.rows[0].name != "__sem_drop_Node" || collected.rows[1].name != "__sem_release_Node") { eprint(collected.rows[0].name); return 26; }
    if (ssarc.with_drop_helpers(collected, flatFunc).rows.len() != 2) { return 27; }
    let merged = ssarc.merge_helpers([], collected.rows.append(collected.rows[0]));
    if (merged.len() != 2) { return 28; }
    if (!merged[0].ok || !merged[1].ok || ssarc.merge_helpers(merged, collected.rows).len() != 2) { eprint(merged[0].why); return 29; }
    // A frame releasing a nominal box with children makes one call to the
    // type's release helper; the uniqueness test and the child walk live there.
    let sinkGraph = ssa.SFunc { name: "sink", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), ssa.SInst { kind_tag: 1, result: 1, args: [], imm: 0, str: "" }], term: ret(1) }] };
    let sinkResult: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
    let sinkFunc = ssasem.Func { ...selfFunc, graph: sinkGraph, values: [selfType, sinkResult], result: sinkResult };
    let sinkPlan = ssaunits.plan(sinkFunc, [3], ssaunits.no_view());
    if (!sinkPlan.ok) { eprint(sinkPlan.why); return 196; }
    let sinkLowered = ssarc.lower(sinkFunc, [3], sinkPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!sinkLowered.ok) { eprint(sinkLowered.why); return 197; }
    let releases: i32 = 0;
    for o in sinkLowered.ops {
        if (ir.render_op(o) == "call_direct __sem_release_Node/1") { releases = releases + 1; }
        if (ir.render_op(o) == "call_direct __fern_rc_is_unique/1") { return 198; }
    }
    if (releases != 1) { return 199; }
    let i32ty: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: false, is_char: false, polymorphic: false };
    let boxOnly: typeinfo.Type = typeinfo.TypeStruct { name: "BoxOnly", args: [] };
    let boxOnlySchema = semrecords.Record { views: false, ty: boxOnly, fields: [semrecords.Field { name: "n", ty: i32ty }] };
    // A length reads its receiver and hands back an i32 that owns nothing: an
    // array selects arr_len, a string str_len, and a receiver that is neither
    // is not a counted container this can read at all.
    let lenGraph = ssa.SFunc { name: "len", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.length(), 1, [0], 0)], term: ret(1) }] };
    let arrLen = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: lenGraph, values: [f.result, i32ty], params: [f.result], result: i32ty, records: semrecords.no_records(), enums: [], calls: [] };
    let arrLenPlan = ssaunits.plan(arrLen, [2], ssaunits.no_view());
    if (!arrLenPlan.ok) { eprint(arrLenPlan.why); return 32; }
    let arrLenLowered = ssarc.lower(arrLen, [2], arrLenPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!arrLenLowered.ok) { eprint(arrLenLowered.why); return 33; }
    let sawArrLen: boolean = false;
    for o in arrLenLowered.ops { if (ir.render_op(o) == "arr_len") { sawArrLen = true; } }
    if (!sawArrLen) { return 34; }
    let strTy: typeinfo.Type = typeinfo.TypeString { tag: 0 };
    let strLen = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: lenGraph, values: [strTy, i32ty], params: [strTy], result: i32ty, records: semrecords.no_records(), enums: [], calls: [] };
    let strLenPlan = ssaunits.plan(strLen, [2], ssaunits.no_view());
    if (!strLenPlan.ok) { eprint(strLenPlan.why); return 35; }
    let strLenLowered = ssarc.lower(strLen, [2], strLenPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!strLenLowered.ok) { eprint(strLenLowered.why); return 36; }
    let sawStrLen: boolean = false;
    for o in strLenLowered.ops { if (ir.render_op(o) == "str_len") { sawStrLen = true; } }
    if (!sawStrLen) { return 37; }
    let badRecv = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: lenGraph, values: [boxOnly, i32ty], params: [boxOnly], result: i32ty, records: semrecords.records_of([boxOnlySchema]), enums: [], calls: [] };
    if (ssaunits.plan(badRecv, [2], ssaunits.no_view()).why != "length container type") { return 38; }
    let badResult = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: lenGraph, values: [f.result, f.result], params: [f.result], result: f.result, records: semrecords.no_records(), enums: [], calls: [] };
    if (ssaunits.plan(badResult, [2], ssaunits.no_view()).why != "length result type") { return 39; }
    // An append takes the receiver's unit and hands one back. The runtime's push
    // gives the unit back only when the receiver's box is the only one its count
    // names, so the count test that chooses between the in-place grow and the
    // copy is emitted here rather than left to the shared helper. A counted
    // receiver dead after the push moves its unit into it.
    let appendGraph = ssa.SFunc { name: "append", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.append(), result: 2, args: [0, 1], imm: 0, str: "" }], term: ret(2) }] };
    let appendFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: appendGraph, values: [f.result, i32ty, f.result],
        params: [f.result, i32ty], result: f.result, records: semrecords.no_records(), enums: [], calls: [] };
    let appendPlan = ssaunits.plan(appendFunc, [3, 1], ssaunits.no_view());
    if (!appendPlan.ok) { eprint(appendPlan.why); return 40; }
    let appendLowered = ssarc.lower(appendFunc, [3, 1], appendPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!appendLowered.ok) { eprint(appendLowered.why); return 41; }
    let sawPush: boolean = false;
    let sawPushUnique: boolean = false;
    for o in appendLowered.ops {
        if (ir.render_op(o) == "arr_push_owned") { sawPush = true; }
        if (o.str == "__fern_rc_is_unique") { sawPushUnique = true; }
    }
    if (!sawPush || !sawPushUnique) { return 42; }
    // The same graph with a BORROWED receiver takes the other lowering. This
    // function holds no unit to move, so it takes none before the push either:
    // a count added up front would answer the push's own rc gate, which then
    // copies a buffer nobody else holds and makes a loop of appends quadratic
    // (#9365). It pushes through the NON-consuming helper, which grows in
    // place when it is the only reader and un-shares when it is not, and takes
    // its unit afterwards — as a retain of the result exactly when the result
    // came back as the receiver's own box.
    //
    // So the uniqueness test must NOT be emitted here: its presence is the
    // regression.
    let borrowAppendPlan = ssaunits.plan(appendFunc, [2, 1], ssaunits.no_view());
    if (!borrowAppendPlan.ok) { eprint(borrowAppendPlan.why); return 43; }
    let borrowAppendLowered = ssarc.lower(appendFunc, [2, 1], borrowAppendPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!borrowAppendLowered.ok) { eprint(borrowAppendLowered.why); return 104; }
    let sawRetain: boolean = false;
    let sawBorrowUnique: boolean = false;
    let sawBorrowPush: boolean = false;
    let sawOwnedPush: boolean = false;
    for o in borrowAppendLowered.ops {
        if (o.str == "__fern_rc_inc") { sawRetain = true; }
        if (o.str == "__fern_rc_is_unique") { sawBorrowUnique = true; }
        if (ir.render_op(o) == "arr_push") { sawBorrowPush = true; }
        if (ir.render_op(o) == "arr_push_owned") { sawOwnedPush = true; }
    }
    if (!sawRetain || sawBorrowUnique) { return 105; }
    if (!sawBorrowPush || sawOwnedPush) { return 106; }
    // An append onto a record FIELD, where the record is a borrowed parameter
    // read afterwards only through its OTHER field — the functional update
    // R { ...r, xs: r.xs.append(v) } — is admitted for the in-place grow
    // (#9365): the plan names the record at the append's result, the
    // lowering tests the RECORD's count rather than retaining the receiver
    // up front, pushes through the non-consuming helper, and nulls the field
    // on the identity arm. The same graph with a second read of the appended
    // field after the push is refused, and takes the retain-then-copy form.
    let growType: typeinfo.Type = typeinfo.TypeStruct { name: "Grow", args: [] };
    let growSchema = semrecords.Record { views: false, ty: growType, fields: [semrecords.Field { name: "xs", ty: f.result }, semrecords.Field { name: "n", ty: i32ty }] };
    let growGraph = ssa.SFunc { name: "grow", nparams: 2, nvals: 6, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.record_get(), result: 2, args: [0], imm: 0, str: "xs" },
            ssa.SInst { kind_tag: ssasem.append(), result: 3, args: [2, 1], imm: 0, str: "" },
            ssa.SInst { kind_tag: ssasem.record_get(), result: 4, args: [0], imm: 1, str: "n" },
            inst(ssasem.record_new(), 5, [3, 4], 0)], term: ret(5) }] };
    let growFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: growGraph, values: [growType, i32ty, f.result, f.result, i32ty, growType],
        params: [growType, i32ty], result: growType, records: semrecords.records_of([growSchema]), enums: [], calls: [] };
    let growPlan = ssaunits.plan(growFunc, [2, 1], ssaunits.no_view());
    if (!growPlan.ok) { eprint(growPlan.why); return 140; }
    if (growPlan.grows[3] != 0 || growPlan.grow_fields[3] != 0) { return 141; }
    let growRows = ssaunits.grow_rows("grow", growFunc, growPlan, [], util.name_index([]));
    if (growRows.len() != 1 || growRows[0].param != 0 || growRows[0].field != 0) { return 142; }
    let growLowered = ssarc.lower(growFunc, [2, 1], growPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!growLowered.ok) { eprint(growLowered.why); return 144; }
    let sawGrowUnique: boolean = false;
    let sawGrowPush: boolean = false;
    let sawGrowNull: boolean = false;
    let sawGrowRetain: boolean = false;
    for o in growLowered.ops {
        if (o.str == "__fern_rc_is_unique") { sawGrowUnique = true; }
        if (ir.render_op(o) == "arr_push") { sawGrowPush = true; }
        if (ir.render_op(o) == "struct_set 0") { sawGrowNull = true; }
        if (o.str == "__fern_rc_inc") { sawGrowRetain = true; }
    }
    if (!sawGrowUnique || !sawGrowPush || !sawGrowNull || sawGrowRetain) { return 145; }
    let readGraph = ssa.SFunc { ...growGraph, nvals: 7,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.record_get(), result: 2, args: [0], imm: 0, str: "xs" },
            ssa.SInst { kind_tag: ssasem.append(), result: 3, args: [2, 1], imm: 0, str: "" },
            ssa.SInst { kind_tag: ssasem.record_get(), result: 4, args: [0], imm: 1, str: "n" },
            ssa.SInst { kind_tag: ssasem.record_get(), result: 6, args: [0], imm: 0, str: "xs" },
            inst(ssasem.record_new(), 5, [3, 4], 0)], term: ret(5) }] };
    let readFunc = ssasem.Func { ...growFunc, graph: readGraph, values: [growType, i32ty, f.result, f.result, i32ty, growType, f.result] };
    let readPlan = ssaunits.plan(readFunc, [2, 1], ssaunits.no_view());
    if (!readPlan.ok) { eprint(readPlan.why); return 146; }
    if (readPlan.grows[3] != 0 - 1) { return 147; }
    if (ssaunits.grow_rows("grow", readFunc, readPlan, [], util.name_index([])).len() != 0) { return 148; }
    let readLowered = ssarc.lower(readFunc, [2, 1], readPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!readLowered.ok) { eprint(readLowered.why); return 149; }
    let sawReadNull: boolean = false;
    let sawReadOwnedPush: boolean = false;
    for o in readLowered.ops {
        if (ir.render_op(o) == "struct_set 0") { sawReadNull = true; }
        if (ir.render_op(o) == "arr_push_owned") { sawReadOwnedPush = true; }
    }
    if (sawReadNull || !sawReadOwnedPush) { return 150; }
    // A with on the same field takes the same admission: the plan names the
    // record, the callee's row says the field may be written, and the lowering
    // tests the record's and the buffer's counts rather than retaining the
    // receiver, writes in place on the identity arm and nulls the field there,
    // and copies through arr_slice on the other.
    let fieldWithGraph = ssa.SFunc { ...growGraph, name: "set",
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.record_get(), result: 2, args: [0], imm: 0, str: "xs" },
            ssa.SInst { kind_tag: ssasem.with(), result: 3, args: [2, 1, 1], imm: 0, str: "" },
            ssa.SInst { kind_tag: ssasem.record_get(), result: 4, args: [0], imm: 1, str: "n" },
            inst(ssasem.record_new(), 5, [3, 4], 0)], term: ret(5) }] };
    let fieldWithFunc = ssasem.Func { ...growFunc, graph: fieldWithGraph };
    let fieldWithPlan = ssaunits.plan(fieldWithFunc, [2, 1], ssaunits.no_view());
    if (!fieldWithPlan.ok) { eprint(fieldWithPlan.why); return 160; }
    if (fieldWithPlan.grows[3] != 0 || fieldWithPlan.grow_fields[3] != 0) { return 161; }
    let fieldWithRows = ssaunits.grow_rows("set", fieldWithFunc, fieldWithPlan, [], util.name_index([]));
    if (fieldWithRows.len() != 1 || fieldWithRows[0].param != 0 || fieldWithRows[0].field != 0) { return 162; }
    let fieldWithLowered = ssarc.lower(fieldWithFunc, [2, 1], fieldWithPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!fieldWithLowered.ok) { eprint(fieldWithLowered.why); return 163; }
    let fieldWithUniques: i32 = 0;
    let sawFieldWithSet: boolean = false;
    let sawFieldWithNull: boolean = false;
    let sawFieldWithCopy: boolean = false;
    let sawFieldWithRetain: boolean = false;
    for o in fieldWithLowered.ops {
        if (o.str == "__fern_rc_is_unique") { fieldWithUniques = fieldWithUniques + 1; }
        if (ir.render_op(o) == "arr_set") { sawFieldWithSet = true; }
        if (ir.render_op(o) == "struct_set 0") { sawFieldWithNull = true; }
        if (ir.render_op(o) == "arr_slice") { sawFieldWithCopy = true; }
        if (o.str == "__fern_rc_inc") { sawFieldWithRetain = true; }
    }
    if (fieldWithUniques != 2 || !sawFieldWithSet || !sawFieldWithNull || !sawFieldWithCopy || sawFieldWithRetain) { return 164; }
    // The same with on a borrowed ARRAY parameter has no field to null and no
    // non-consuming helper to write through, so it keeps the retain and copies.
    let arrWithGraph = ssa.SFunc { ...appendGraph, name: "set_arr",
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.with(), result: 2, args: [0, 1, 1], imm: 0, str: "" }], term: ret(2) }] };
    let arrWithFunc = ssasem.Func { ...appendFunc, graph: arrWithGraph };
    let arrWithPlan = ssaunits.plan(arrWithFunc, [2, 1], ssaunits.no_view());
    if (!arrWithPlan.ok) { eprint(arrWithPlan.why); return 165; }
    if (ssaunits.grow_rows("set_arr", arrWithFunc, arrWithPlan, [], util.name_index([])).len() != 0) { return 166; }
    let arrWithLowered = ssarc.lower(arrWithFunc, [2, 1], arrWithPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!arrWithLowered.ok) { eprint(arrWithLowered.why); return 167; }
    let sawArrWithRetain: boolean = false;
    for o in arrWithLowered.ops { if (o.str == "__fern_rc_inc") { sawArrWithRetain = true; } }
    if (!sawArrWithRetain) { return 168; }
    // A caller that still reads a record it lends to grow holds a count on
    // the field the callee may grow, and on that field alone: the bracket
    // reads the callee's row, captures the buffer, and releases the capture.
    let callGraph = ssa.SFunc { name: "caller", nparams: 2, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.call(), result: 2, args: [0, 1], imm: 0, str: "grow" },
            ssa.SInst { kind_tag: ssasem.record_get(), result: 3, args: [0], imm: 1, str: "n" }], term: ret(3) }] };
    let callFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: callGraph, values: [growType, i32ty, growType, i32ty],
        params: [growType, i32ty], result: i32ty, records: semrecords.records_of([growSchema]), enums: [],
        calls: [ssasem.Contract { name: "grow", params: [growType, i32ty], modes: [2, 1], result: growType }] };
    let callPlan = ssaunits.plan(callFunc, [2, 1], ssaunits.no_view());
    if (!callPlan.ok) { eprint(callPlan.why); return 151; }
    let growTable: ssaunits.GrowTable = ssaunits.GrowTable { rows: growRows, by_callee: util.name_index(["grow"]) };
    let callLowered = ssarc.lower(callFunc, [2, 1], callPlan, irtables.struct_tab_empty(), growTable, util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!callLowered.ok) { eprint(callLowered.why); return 152; }
    let shareIncs: i32 = 0;
    let shareDecs: i32 = 0;
    let sawFieldGet: boolean = false;
    for o in callLowered.ops {
        if (o.str == "__fern_arr_share_inc") { shareIncs = shareIncs + 1; }
        if (o.str == "__fern_arr_share_dec") { shareDecs = shareDecs + 1; }
        if (ir.render_op(o) == "struct_get 0") { sawFieldGet = true; }
    }
    if (shareIncs != 1 || shareDecs != 1 || !sawFieldGet) { return 153; }
    let unbracketed = ssarc.lower(callFunc, [2, 1], callPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    for o in unbracketed.ops { if (o.str == "__fern_arr_share_inc") { return 154; } }
    // A field HANDED to a callee — R { ...r, xs: g(r.xs) } — is not bracketed
    // when the record is read no further through it, and the closure gives the
    // handing function the callee's row at that field: grow_table over both.
    let viaGraph = ssa.SFunc { name: "via", nparams: 2, nvals: 6, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.record_get(), result: 2, args: [0], imm: 0, str: "xs" },
            ssa.SInst { kind_tag: ssasem.call(), result: 3, args: [2, 1], imm: 0, str: "push" },
            ssa.SInst { kind_tag: ssasem.record_get(), result: 4, args: [0], imm: 1, str: "n" },
            inst(ssasem.record_new(), 5, [3, 4], 0)], term: ret(5) }] };
    let viaFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: viaGraph, values: [growType, i32ty, f.result, f.result, i32ty, growType],
        params: [growType, i32ty], result: growType, records: semrecords.records_of([growSchema]), enums: [],
        calls: [ssasem.Contract { name: "push", params: [f.result, i32ty], modes: [2, 1], result: f.result }] };
    let viaPlan = ssaunits.plan(viaFunc, [2, 1], ssaunits.no_view());
    if (!viaPlan.ok) { eprint(viaPlan.why); return 155; }
    let pushPlan = ssaunits.plan(appendFunc, [2, 1], ssaunits.no_view());
    let table = ssaunits.grow_table(["push", "via"], [appendFunc, viaFunc], [pushPlan, viaPlan]);
    let sawPushRow: boolean = false;
    let sawViaRow: boolean = false;
    for row in table.rows {
        if (row.callee == "push" && row.param == 0 && row.field == 0 - 1) { sawPushRow = true; }
        if (row.callee == "via" && row.param == 0 && row.field == 0) { sawViaRow = true; }
    }
    if (!sawPushRow || !sawViaRow || table.rows.len() != 2) { return 156; }
    let viaLowered = ssarc.lower(viaFunc, [2, 1], viaPlan, irtables.struct_tab_empty(), table, util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!viaLowered.ok) { eprint(viaLowered.why); return 157; }
    // No retain up front; the share bracket is there, but GATED on the
    // record's count, so a record with a holder this frame cannot see still
    // makes the callee copy.
    let sawViaGate: boolean = false;
    let sawViaShare: boolean = false;
    for o in viaLowered.ops {
        if (o.str == "__fern_rc_inc") { return 158; }
        if (o.str == "__fern_rc_is_unique") { sawViaGate = true; }
        if (o.str == "__fern_arr_share_inc") { sawViaShare = true; }
    }
    if (!sawViaGate || !sawViaShare) { return 159; }
    // One element replaced hands the receiver's unit over the same way, and
    // lowers to the count test that chooses the in-place store or the copy;
    // a scalar element retains nothing, a counted one retains the copy's
    // elements and releases the element the store replaces.
    let withGraph = ssa.SFunc { name: "with", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.with(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(3) }] };
    let withFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: withGraph, values: [f.result, i32ty, i32ty, f.result],
        params: [f.result, i32ty, i32ty], result: f.result, records: semrecords.no_records(), enums: [], calls: [] };
    let withPlan = ssaunits.plan(withFunc, [3, 1, 1], ssaunits.no_view());
    if (!withPlan.ok) { eprint(withPlan.why); return 121; }
    let withLowered = ssarc.lower(withFunc, [3, 1, 1], withPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!withLowered.ok) { eprint(withLowered.why); return 122; }
    let sawUnique: boolean = false;
    let sawSet: boolean = false;
    let sawIncElems: boolean = false;
    for o in withLowered.ops {
        if (o.str == "__fern_rc_is_unique") { sawUnique = true; }
        if (ir.render_op(o) == "arr_set") { sawSet = true; }
        if (o.str == "__fern_arr_inc_elems") { sawIncElems = true; }
    }
    if (!sawUnique || !sawSet || sawIncElems) { return 123; }
    let borrowWithPlan = ssaunits.plan(withFunc, [2, 1, 1], ssaunits.no_view());
    if (!borrowWithPlan.ok) { eprint(borrowWithPlan.why); return 124; }
    let borrowWithLowered = ssarc.lower(withFunc, [2, 1, 1], borrowWithPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!borrowWithLowered.ok) { eprint(borrowWithLowered.why); return 106; }
    sawRetain = false;
    for o in borrowWithLowered.ops { if (o.str == "__fern_rc_inc") { sawRetain = true; } }
    if (!sawRetain) { return 107; }
    let badIndex = ssasem.Func { ...withFunc, values: [f.result, strTy, i32ty, f.result], params: [f.result, strTy, i32ty] };
    if (ssaunits.plan(badIndex, [3, 2, 1], ssaunits.no_view()).why != "with index type") { return 125; }
    let badWithElem = ssasem.Func { ...withFunc, values: [f.result, i32ty, strTy, f.result], params: [f.result, i32ty, strTy] };
    if (ssaunits.plan(badWithElem, [3, 1, 2], ssaunits.no_view()).why != "with element type") { return 126; }
    let strArr: typeinfo.Type = typeinfo.TypeArray { elem: strTy, view: false };
    let strWith = ssasem.Func { ...withFunc, values: [strArr, i32ty, strTy, strArr], params: [strArr, i32ty, strTy], result: strArr };
    let strWithPlan = ssaunits.plan(strWith, [3, 1, 3], ssaunits.no_view());
    if (!strWithPlan.ok) { eprint(strWithPlan.why); return 127; }
    let strWithLowered = ssarc.lower(strWith, [3, 1, 3], strWithPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!strWithLowered.ok) { eprint(strWithLowered.why); return 128; }
    let sawStrFree: boolean = false;
    sawIncElems = false;
    for o in strWithLowered.ops {
        if (o.str == "__fern_arr_inc_elems") { sawIncElems = true; }
        if (o.str == "__fern_str_free") { sawStrFree = true; }
    }
    if (!sawIncElems || !sawStrFree) { return 129; }
    // An element that is not the array's own type, and a result that is not the
    // receiver's, are contract errors rather than lowering ones.
    let badElem = ssasem.Func { ...appendFunc, values: [f.result, strTy, f.result], params: [f.result, strTy] };
    if (ssaunits.plan(badElem, [3, 2], ssaunits.no_view()).why != "append element type") { return 44; }
    let badRecvAppend = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: appendGraph, values: [strTy, i32ty, strTy],
        params: [strTy, i32ty], result: strTy, records: semrecords.no_records(), enums: [], calls: [] };
    if (ssaunits.plan(badRecvAppend, [3, 1], ssaunits.no_view()).why != "append container type") { return 45; }
    // A slice is a VIEW: it owns its box and borrows the source's bytes, so it
    // is released by the view helper rather than the ordinary string free —
    // which would skip the immortal rc sentinel and leak the box on the
    // register backends. Its type says so, and a slice typed as an owned
    // string is refused. One that dies in the frame that made it takes its
    // box from the frame (str_slice frame:N, ssaunits.frame_views); the view
    // helper passes over that box's immortal rc. The slice must DIE here, not
    // be returned: a returned value is handed to the caller, so it has no
    // drop site and would emit no release at all.
    let viewTy: typeinfo.Type = typeinfo.TypeString { tag: 1 };
    let sliceGraph = ssa.SFunc { name: "slice", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.slice(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(1) }] };
    let sliceFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: sliceGraph, values: [strTy, i32ty, i32ty, viewTy],
        params: [strTy, i32ty, i32ty], result: i32ty, records: semrecords.no_records(), enums: [], calls: [] };
    let ownedSlice = ssasem.Func { ...sliceFunc, values: [strTy, i32ty, i32ty, strTy] };
    if (ssaunits.plan(ownedSlice, [2, 1, 1], ssaunits.no_view()).why != "slice container type") { return 103; }
    let slicePlan = ssaunits.plan(sliceFunc, [2, 1, 1], ssaunits.no_view());
    if (!slicePlan.ok) { eprint(slicePlan.why); return 46; }
    let sliceLowered = ssarc.lower(sliceFunc, [2, 1, 1], slicePlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!sliceLowered.ok) { eprint(sliceLowered.why); return 47; }
    let sawFrameSlice: boolean = false;
    let sawViewFree: boolean = false;
    let sawPlainFree: boolean = false;
    for o in sliceLowered.ops {
        let rendered: string = ir.render_op(o);
        if (rendered.len() > 16 && slice_unchecked(rendered, 0, 16) == "str_slice frame:") { sawFrameSlice = true; }
        if (rendered == "call_direct __fern_str_view_free/1") { sawViewFree = true; }
        if (rendered == "call_direct __fern_str_free/1") { sawPlainFree = true; }
    }
    if (!sawFrameSlice) { return 48; }
    if (!sawViewFree) { return 49; }
    if (sawPlainFree) { return 50; }
    // An ordinary string result keeps the plain free, so the view symbol is
    // selected per VALUE and not applied to every string.
    let plainGraph = ssa.SFunc { name: "plain", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
    let plainFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: plainGraph, values: [strTy], params: [strTy], result: strTy,
        records: semrecords.no_records(), enums: [], calls: [] };
    let plainPlan = ssaunits.plan(plainFunc, [3], ssaunits.no_view());
    if (!plainPlan.ok) { eprint(plainPlan.why); return 51; }
    for o in ssarc.lower(plainFunc, [3], plainPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()).ops {
        if (ir.render_op(o) == "call_direct __fern_str_view_free/1") { return 52; }
    }
    // The bounds are i32 and the receiver is a string; neither is negotiable.
    let badBound = ssasem.Func { ...sliceFunc, values: [strTy, strTy, i32ty, viewTy], params: [strTy, strTy, i32ty], result: i32ty };
    if (ssaunits.plan(badBound, [2, 2, 1], ssaunits.no_view()).why != "slice bound type") { return 53; }
    // A schema field the walk never reads only has to LAY OUT, not lower. A
    // wide scalar ahead of a reference field shifts nothing, because every
    // backend takes a field's offset from its index on a uniform 8-byte slot,
    // so the helper reads the string at field 1 and never touches field 0.
    let dropGraph = ssa.SFunc { name: "drop", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(1, 1, [], 0)], term: ret(1) }] };
    let f64ty: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: false };
    let wideRec: typeinfo.Type = typeinfo.TypeStruct { name: "Wide", args: [] };
    let wideRecSchema = semrecords.Record { views: false, ty: wideRec, fields: [semrecords.Field { name: "d", ty: f64ty },
        semrecords.Field { name: "s", ty: strTy }] };
    let wideFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: dropGraph, values: [wideRec, i32ty], params: [wideRec], result: i32ty,
        records: semrecords.records_of([wideRecSchema]), enums: [], calls: [] };
    let widePlan = ssaunits.plan(wideFunc, [3], ssaunits.no_view());
    if (!widePlan.ok) { eprint(widePlan.why); return 54; }
    let wideLowered = ssarc.lower(wideFunc, [3], widePlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wideLowered.ok) { eprint(wideLowered.why); return 55; }
    let sawField1: boolean = false;
    let sawField0: boolean = false;
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
    let wideVal = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: dropGraph, values: [f64ty, i32ty], params: [f64ty], result: i32ty,
        records: semrecords.no_records(), enums: [], calls: [] };
    let wideValPlan = ssaunits.plan(wideVal, [1], ssaunits.no_view());
    if (!wideValPlan.ok) { eprint(wideValPlan.why); return 58; }
    let wideValLowered = ssarc.lower(wideVal, [1], wideValPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wideValLowered.ok) { eprint(wideValLowered.why); return 60; }
    if (wideValLowered.f64_slots.len() != 1 || wideValLowered.f64_slots[0] != 0) { return 110; }
    let f32ty: typeinfo.Type = typeinfo.TypeFloat { width: 32, polymorphic: false };
    let narrowVal = ssasem.Func { ...wideVal, values: [f32ty, i32ty], params: [f32ty] };
    let narrowValLowered = ssarc.lower(narrowVal, [1], ssaunits.plan(narrowVal, [1], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!narrowValLowered.ok) { eprint(narrowValLowered.why); return 111; }
    if (narrowValLowered.f64_slots.len() != 1 || narrowValLowered.f64_slots[0] != 0) { return 111; }
    let floatElem = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: ssa.SFunc { ...dropGraph, nvals: 2, blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            inst(ssasem.array_new(), 1, [0], 0)], term: ret(1) }] },
        values: [f64ty, typeinfo.TypeArray { elem: f64ty, view: false }], params: [f64ty], result: typeinfo.TypeArray { elem: f64ty, view: false }, records: semrecords.no_records(), enums: [], calls: [] };
    // An ARRAY element of that width does lower: every element op carries its
    // own slot width, so the construction stores eight bytes and wasm reads
    // back the float form. The i64 shares the stride and takes the integer
    // form, which is what the op's unsigned flag selects.
    let floatElemLowered = ssarc.lower(floatElem, [1], ssaunits.plan(floatElem, [1], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!floatElemLowered.ok) { eprint(floatElemLowered.why); return 112; }
    if (!made_wide(floatElemLowered, false)) { return 133; }
    // A float constant carries its text, as a wide integer does.
    let floatK = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: ssa.SFunc { ...dropGraph, nparams: 0, nvals: 1,
            blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 0)], term: ret(0) }] },
        values: [f64ty], params: [], result: f64ty, records: semrecords.no_records(), enums: [], calls: [] };
    if (ssaunits.plan(floatK, [], ssaunits.no_view()).why != "float constant needs its literal text") { return 113; }
    // A string index reads its receiver and hands back a scalar. The source
    // DIES at the read here and the result is returned past it, which a
    // projection could never do — the planner would refuse the borrow. That it
    // plans at all is the pin: str_index is not in projects(), so nothing
    // anchors the i32 to the bytes it came from.
    let idxGraph = ssa.SFunc { name: "idx", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.str_index(), result: 2, args: [0, 1], imm: 0, str: "" }], term: ret(2) }] };
    let u8ty: typeinfo.Type = typeinfo.TypeI32 { width: 8, unsigned: true, is_char: false, polymorphic: false };
    let idxFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: idxGraph, values: [strTy, i32ty, u8ty],
        params: [strTy, i32ty], result: u8ty, records: semrecords.no_records(), enums: [], calls: [] };
    let idxPlan = ssaunits.plan(idxFunc, [3, 1], ssaunits.no_view());
    if (!idxPlan.ok) { eprint(idxPlan.why); return 61; }
    let idxLowered = ssarc.lower(idxFunc, [3, 1], idxPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!idxLowered.ok) { eprint(idxLowered.why); return 62; }
    let sawIndex: boolean = false;
    for o in idxLowered.ops { if (ir.render_op(o) == "str_index") { sawIndex = true; } }
    if (!sawIndex) { return 63; }
    // An array receiver has its own projection, and the index is not negotiable.
    let idxArr = ssasem.Func { ...idxFunc, values: [f.result, i32ty, u8ty], params: [f.result, i32ty] };
    if (ssaunits.plan(idxArr, [3, 1], ssaunits.no_view()).why != "string index container type") { return 64; }
    let idxBad = ssasem.Func { ...idxFunc, values: [strTy, strTy, u8ty], params: [strTy, strTy] };
    if (ssaunits.plan(idxBad, [3, 2], ssaunits.no_view()).why != "string index type") { return 65; }
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
    let negGraph = ssa.SFunc { name: "neg", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: 10, result: 1, args: [0], imm: 0, str: "-" }], term: ret(1) }] };
    let negFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: negGraph, values: [i32ty, i32ty], params: [i32ty], result: i32ty,
        records: semrecords.no_records(), enums: [], calls: [] };
    if (masks(ssarc.lower(negFunc, [1], ssaunits.plan(negFunc, [1], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none())) != "i32") { return 70; }
    // The byte is the type the checker gives a string index, so it is not
    // negotiable either: an i32 result is a contract error, not a free widening.
    let idxWide = ssasem.Func { ...idxFunc, values: [strTy, i32ty, i32ty], result: i32ty };
    if (ssaunits.plan(idxWide, [3, 1], ssaunits.no_view()).why != "string index result type") { return 71; }
    // A byte masks to eight bits where an i32 masks to thirty-two, and the same
    // operators do it — the width comes from the result type, not the opcode.
    let bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
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
    // Into a float is a real conversion and never a mask; a reference is not a
    // cast operand at all. OUT of a float into a byte is the one direction that
    // is both: the conversion answers at 32 bits, so the byte takes the same
    // mask the integer narrowing takes.
    let f64ty2: typeinfo.Type = typeinfo.TypeFloat { width: 64, polymorphic: false };
    if (cast_masks(i32ty, f64ty2) != "") { return 80; }
    if (cast_masks(f64ty2, i32ty) != "") { return 81; }
    if (cast_masks(strTy, i32ty) != "plan:cast operand type") { return 82; }
    if (cast_masks(u8ty, f64ty2) != "") { return 114; }
    if (cast_masks(f64ty2, u8ty) != "u8") { return 193; }
    // A byte's constant is pushed with no mask, so it has to be in range here.
    let kGraph = ssa.SFunc { name: "k", nparams: 0, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 255)], term: ret(0) }] };
    let kFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: kGraph, values: [u8ty], params: [], result: u8ty,
        records: semrecords.no_records(), enums: [], calls: [] };
    if (!ssaunits.plan(kFunc, [], ssaunits.no_view()).ok) { eprint(ssaunits.plan(kFunc, [], ssaunits.no_view()).why); return 83; }
    let kOver = ssasem.Func { ...kFunc, graph: ssa.SFunc { ...kGraph,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 256)], term: ret(0) }] } };
    if (ssaunits.plan(kOver, [], ssaunits.no_view()).why != "integer constant range") { return 84; }
    // A 64-bit value gets a slot of its own. Its operators run at width 64 and
    // are already full-width, so nothing masks after them; crossing into and
    // out of that domain is an explicit extend and wrap.
    let i64ty: typeinfo.Type = typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false, polymorphic: false };
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
    let wideBin = ssarc.lower(wide_binary(i64ty, bt, "<"), [1, 1], ssaunits.plan(wide_binary(i64ty, bt, "<"), [1, 1], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wideBin.ok) { eprint(wideBin.why); return 93; }
    let sawWide: boolean = false;
    for o in wideBin.ops { if (o.kind_tag == ir.kind_id("lt_s") && o.width == 64) { sawWide = true; } }
    if (!sawWide) { return 94; }
    // A wide value is an array element and a VALUE, and the array that holds
    // it is one box like any other.
    let wideArr: typeinfo.Type = typeinfo.TypeArray { elem: i64ty, view: false };
    let wideArrFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [wideArr], params: [wideArr], result: wideArr,
        records: semrecords.no_records(), enums: [], calls: [] };
    let wideArrLowered = ssarc.lower(wideArrFunc, [2], ssaunits.plan(wideArrFunc, [2], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wideArrLowered.ok) { eprint(wideArrLowered.why); return 95; }
    if (wideArrLowered.arr_slots.len() != 1 || wideArrLowered.arr_slots[0] != 0) { return 134; }
    let wideElem = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: ssa.SFunc { ...dropGraph, nvals: 2, blocks: [ssa.SBlock { id: 7, preds: [],
            insts: [inst(6, 0, [], 0), inst(ssasem.array_new(), 1, [0], 0)], term: ret(1) }] },
        values: [i64ty, wideArr], params: [i64ty], result: wideArr, records: semrecords.no_records(), enums: [], calls: [] };
    let wideElemLowered = ssarc.lower(wideElem, [1], ssaunits.plan(wideElem, [1], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wideElemLowered.ok) { eprint(wideElemLowered.why); return 135; }
    if (!made_wide(wideElemLowered, true)) { return 136; }
    // Tuple construction carries its element kinds, including wide stores.
    // Admission alone is insufficient: the wasm emitter reads these kinds
    // to choose the store width, so losing them would truncate the elements.
    let buildGraph = ssa.SFunc { name: "build", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            inst(ssasem.tuple_new(), 1, [0, 0], 0)], term: ret(1) }] };
    let wideTup: typeinfo.Type = typeinfo.TypeTuple { elements: [i64ty, i64ty] };
    let buildFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: buildGraph, values: [i64ty, wideTup], params: [i64ty], result: wideTup,
        records: semrecords.no_records(), enums: [], calls: [] };
    let wideTupLowered = ssarc.lower(buildFunc, [1], ssaunits.plan(buildFunc, [1], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wideTupLowered.ok) { eprint(wideTupLowered.why); return 96; }
    let sawWideTuple: boolean = false;
    for o in wideTupLowered.ops {
        if (o.kind_tag == ir.kind_id("tuple_make") && o.i32_imm == 2 && o.str == "i64,i64") { sawWideTuple = true; }
    }
    if (!sawWideTuple) { eprint("wide tuple construction lost its element kinds"); return 96; }
    // Supporting wide tuples does not admit a value whose type disagrees
    // with the declared element type.
    let narrowTup: typeinfo.Type = typeinfo.TypeTuple { elements: [i32ty, i32ty] };
    let sneakFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: buildGraph, values: [i64ty, narrowTup], params: [i64ty], result: narrowTup,
        records: semrecords.no_records(), enums: [], calls: [] };
    if (ssaunits.plan(sneakFunc, [1], ssaunits.no_view()).ok) { return 97; }
    // A record's field width comes from its declaration, which construction
    // must resolve even though the drop walk never reads scalar fields.
    let wide64Ty: typeinfo.Type = typeinfo.TypeStruct { name: "Wide64", args: [] };
    let wide64Schema = semrecords.Record { views: false, ty: wide64Ty, fields: [semrecords.Field { name: "n", ty: i64ty }] };
    let wide64Graph = ssa.SFunc { name: "mkwide", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            inst(ssasem.record_new(), 1, [0], 0)], term: ret(1) }] };
    let wide64Func = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: wide64Graph, values: [i64ty, wide64Ty], params: [i64ty], result: wide64Ty,
        records: semrecords.records_of([wide64Schema]), enums: [], calls: [] };
    let wide64Plan = ssaunits.plan(wide64Func, [1], ssaunits.no_view());
    if (!wide64Plan.ok) { eprint(wide64Plan.why); return 101; }
    // A wide field stores at the width its declaration names, which the
    // construction carries as the declaration's index; with no declaration in
    // the table there is no width to carry, so the construction is refused
    // rather than built through a narrow store.
    if (!refused(ssarc.lower(wide64Func, [1], wide64Plan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()), "unsupported physical RC construction without declaration")) { return 102; }
    let declTab = irtables.struct_tab(parser.parse_module(lexer.tokenize("enum Pair { W(i32) } struct Wide64 { n: i64 } enum Span { W(i64) }")).structs);
    let wide64Lowered = ssarc.lower(wide64Func, [1], wide64Plan, declTab, ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!wide64Lowered.ok) { eprint(wide64Lowered.why); return 115; }
    let wide64Decl: i32 = 0 - 1;
    for o in wide64Lowered.ops { if (o.str == "Wide64") { wide64Decl = o.decl; } }
    if (irtables.decl_at_field_type(declTab, wide64Decl, 0) != "i64") { return 116; }
    // A variant resolves within its own enum: two enums declare W here, and
    // the by-name answer is the first-declared one's, whose payload is narrow.
    let spanTy: typeinfo.Type = typeinfo.TypeUnion { name: "Span", args: [] };
    let spanEnum = semrecords.Enum { views: false, ty: spanTy, variants: [semrecords.Variant { name: "W", fields: [semrecords.Field { name: "__ev", ty: i64ty }] }], layout: semrecords.layout_variant() };
    let spanGraph = ssa.SFunc { name: "mkspan", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: ssasem.variant_new(), result: 1, args: [0], imm: 0, str: "W" }], term: ret(1) }] };
    let spanFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: spanGraph, values: [i64ty, spanTy], params: [i64ty], result: spanTy,
        records: semrecords.no_records(), enums: [spanEnum], calls: [] };
    let spanPlan = ssaunits.plan(spanFunc, [1], ssaunits.no_view());
    if (!spanPlan.ok) { eprint(spanPlan.why); return 117; }
    if (!refused(ssarc.lower(spanFunc, [1], spanPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none()), "unsupported physical RC construction without declaration")) { return 118; }
    let spanLowered = ssarc.lower(spanFunc, [1], spanPlan, declTab, ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!spanLowered.ok) { eprint(spanLowered.why); return 119; }
    let spanDecl: i32 = 0 - 1;
    for o in spanLowered.ops { if (o.str == "W") { spanDecl = o.decl; } }
    if (irtables.decl_at_field_type(declTab, spanDecl, 0) != "i64") { return 120; }
    // A constant with no signed i32 immediate to use carries the literal's
    // text instead; a narrow signed one carries none, and neither may carry both.
    let wideK = ssa.SFunc { name: "wk", nparams: 0, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [ssa.SInst { kind_tag: 1, result: 0, args: [], imm: 0, str: "4294967296" }], term: ret(0) }] };
    let wideKFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: wideK, values: [i64ty], params: [], result: i64ty,
        records: semrecords.no_records(), enums: [], calls: [] };
    if (!ssaunits.plan(wideKFunc, [], ssaunits.no_view()).ok) { eprint(ssaunits.plan(wideKFunc, [], ssaunits.no_view()).why); return 98; }
    let noText = ssasem.Func { ...wideKFunc, graph: ssa.SFunc { ...wideK,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 0)], term: ret(0) }] } };
    if (ssaunits.plan(noText, [], ssaunits.no_view()).why != "constant needs its literal text") { return 99; }
    let narrowText = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: wideK, values: [i32ty], params: [], result: i32ty,
        records: semrecords.no_records(), enums: [], calls: [] };
    if (ssaunits.plan(narrowText, [], ssaunits.no_view()).why != "narrow constant carries text") { return 100; }
    // A u32 occupies the i32's slot but reaches past the immediate's sign bit, so
    // it takes the text form at every value rather than at some of them.
    let u32ty: typeinfo.Type = typeinfo.TypeI32 { width: 32, unsigned: true, is_char: false, polymorphic: false };
    let u32Text = ssasem.Func { ...wideKFunc, values: [u32ty], result: u32ty };
    if (!ssaunits.plan(u32Text, [], ssaunits.no_view()).ok) { eprint(ssaunits.plan(u32Text, [], ssaunits.no_view()).why); return 101; }
    let u32Imm = ssasem.Func { ...u32Text, graph: ssa.SFunc { ...wideK,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(1, 0, [], 7)], term: ret(0) }] } };
    if (ssaunits.plan(u32Imm, [], ssaunits.no_view()).why != "constant needs its literal text") { return 102; }
    // A map's box carries a count, so a unit of one is shared like any
    // box's: the graph below returns a borrowed map, which the return
    // supplies by retaining it.
    let mapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: i32ty };
    let mapFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: g, values: [mapTy], params: [mapTy], result: mapTy,
        records: semrecords.no_records(), enums: [], calls: [] };
    let mapPlan = ssaunits.plan(mapFunc, [2], ssaunits.no_view());
    if (!mapPlan.ok) { eprint(mapPlan.why); return 137; }
    let mapLowered = ssarc.lower(mapFunc, [2], mapPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!mapLowered.ok) { eprint(mapLowered.why); return 138; }
    let sawMapRetain: boolean = false;
    for o in mapLowered.ops {
        if (o.str == "__fern_rc_inc") { sawMapRetain = true; }
    }
    if (!sawMapRetain) { return 195; }
    // The same graph over an OWNED map moves that unit, and the map is freed
    // by its own helper rather than released by a count.
    let ownMapPlan = ssaunits.plan(mapFunc, [3], ssaunits.no_view());
    if (!ownMapPlan.ok) { eprint(ownMapPlan.why); return 139; }
    let ownMapLowered = ssarc.lower(mapFunc, [3], ownMapPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!ownMapLowered.ok) { eprint(ownMapLowered.why); return 140; }
    for o in ownMapLowered.ops {
        if (o.str == "__fern_rc_inc" || o.str == "__fern_rc_dec") { return 141; }
    }
    // A STRING value column is counted: the map owns a unit of every value and
    // its release walks the column, so the plan admits it where it once refused
    // the whole idea of a reference value.
    let strMapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: strTy };
    let strMapFunc = ssasem.Func { ...mapFunc, values: [strMapTy], params: [strMapTy], result: strMapTy };
    let strMapPlan = ssaunits.plan(strMapFunc, [3], ssaunits.no_view());
    if (!strMapPlan.ok) { eprint(strMapPlan.why); return 142; }
    // A column of BOXES — here i32 arrays — is counted too: the map owns a
    // unit of every value, and the value's own release, named by the physical
    // lowering, is what the runtime walks the column with.
    let arrMapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: typeinfo.TypeArray { elem: i32ty, view: false } };
    let arrMapFunc = ssasem.Func { ...mapFunc, values: [arrMapTy], params: [arrMapTy], result: arrMapTy };
    if (!ssaunits.plan(arrMapFunc, [3], ssaunits.no_view()).ok) { eprint(ssaunits.plan(arrMapFunc, [3], ssaunits.no_view()).why); return 146; }
    // A column of function values is a column of environment boxes: the map
    // owns a unit of each, and the column's release walks their captures.
    let fnMapTy: typeinfo.Type = typeinfo.TypeMap { key: strTy, value: typeinfo.TypeFunc { param_types: [i32ty], param_own: [false], ret_type: i32ty, params_known: true } };
    let fnMapFunc = ssasem.Func { ...mapFunc, values: [fnMapTy], params: [fnMapTy], result: fnMapTy };
    if (!ssaunits.plan(fnMapFunc, [3], ssaunits.no_view()).ok) { eprint(ssaunits.plan(fnMapFunc, [3], ssaunits.no_view()).why); return 147; }
    // A function-typed KEY stays refused: nothing hashes or compares one.
    let fnKeyTy: typeinfo.Type = typeinfo.TypeMap { key: typeinfo.TypeFunc { param_types: [i32ty], param_own: [false], ret_type: i32ty, params_known: true }, value: i32ty };
    let fnKeyFunc = ssasem.Func { ...mapFunc, values: [fnKeyTy], params: [fnKeyTy], result: fnKeyTy };
    if (ssaunits.plan(fnKeyFunc, [3], ssaunits.no_view()).why != "function value is not an element") { return 235; }
    // A map over 32-bit integer or boolean columns runs on core/map
    // (ssarc.routed_map): it is admitted, and dropped whole through
    // __map_drop_impl. A string value column runs there too, and its drop
    // releases the column's strings through __map_drop_strcols_impl.
    let intMapTy: typeinfo.Type = typeinfo.TypeMap { key: i32ty, value: i32ty };
    let intMapFunc = ssasem.Func { ...mapFunc, values: [intMapTy], params: [intMapTy], result: intMapTy };
    if (!ssaunits.plan(intMapFunc, [3], ssaunits.no_view()).ok) { eprint(ssaunits.plan(intMapFunc, [3], ssaunits.no_view()).why); return 143; }
    let dropMapGraph = ssa.SFunc { name: "drop_map", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(1, 1, [], 7)], term: ret(1) }] };
    let dropIntMap = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: dropMapGraph, values: [intMapTy, i32ty], params: [intMapTy], result: i32ty, records: semrecords.no_records(), enums: [], calls: [] };
    let dropIntPlan = ssaunits.plan(dropIntMap, [3], ssaunits.no_view());
    if (!dropIntPlan.ok) { eprint(dropIntPlan.why); return 169; }
    let dropIntLowered = ssarc.lower(dropIntMap, [3], dropIntPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!dropIntLowered.ok) { eprint(dropIntLowered.why); return 170; }
    let sawIntFree: boolean = false;
    for o in dropIntLowered.ops { if (o.str == "__map_drop_impl") { sawIntFree = true; } }
    if (!sawIntFree) { return 171; }
    let intStrMapTy: typeinfo.Type = typeinfo.TypeMap { key: i32ty, value: strTy };
    let dropIntStr = ssasem.Func { ...dropIntMap, values: [intStrMapTy, i32ty], params: [intStrMapTy] };
    let dropIntStrLowered = ssarc.lower(dropIntStr, [3], ssaunits.plan(dropIntStr, [3], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!dropIntStrLowered.ok) { eprint(dropIntStrLowered.why); return 172; }
    let sawIntStrFree: boolean = false;
    for o in dropIntStrLowered.ops { if (o.str == "__map_drop_strcols_impl") { sawIntStrFree = true; } }
    if (!sawIntStrFree) { return 173; }
    // A column of boxes on core/map is dropped through __map_drop_boxes_impl,
    // handed the value's release as a closure over an environment-first
    // wrapper the lowering emits beside the release itself; an insert reads
    // out the box it may supersede and releases it through the same release.
    let dropArrMap = ssasem.Func { ...dropIntMap, values: [arrMapTy, i32ty], params: [arrMapTy] };
    let dropArrLowered = ssarc.lower(dropArrMap, [3], ssaunits.plan(dropArrMap, [3], ssaunits.no_view()), irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!dropArrLowered.ok) { eprint(dropArrLowered.why); return 187; }
    let releaseName: string = ssarc.release_helper_name(typeinfo.TypeArray { elem: i32ty, view: false });
    let sawArrFree: boolean = false;
    let sawArrRelease: boolean = false;
    for o in dropArrLowered.ops {
        if (ir.render_op(o) == "call_direct __map_drop_boxes_impl/2") { sawArrFree = true; }
        if (ir.render_op(o) == "const_closure " + releaseName + "$env") { sawArrRelease = true; }
    }
    if (!sawArrFree || !sawArrRelease) { return 188; }
    let sawHelper: boolean = false;
    let sawEnvHelper: boolean = false;
    for h in ssarc.drop_helpers(dropArrMap) {
        if (h.name == releaseName && h.n_params == 1) { sawHelper = true; }
        if (h.name == releaseName + "$env" && h.n_params == 2) { sawEnvHelper = true; }
    }
    if (!sawHelper || !sawEnvHelper) { return 189; }
    let arrInsertGraph = ssa.SFunc { name: "arr_insert", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.map_insert(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(3) }] };
    let arrInsert = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: arrInsertGraph, values: [arrMapTy, strTy, typeinfo.TypeArray { elem: i32ty, view: false }, arrMapTy], params: [arrMapTy, strTy, typeinfo.TypeArray { elem: i32ty, view: false }], result: arrMapTy, records: semrecords.no_records(), enums: [], calls: [] };
    let arrInsertPlan = ssaunits.plan(arrInsert, [3, 3, 3], ssaunits.no_view());
    if (!arrInsertPlan.ok) { eprint(arrInsertPlan.why); return 190; }
    let arrInsertLowered = ssarc.lower(arrInsert, [3, 3, 3], arrInsertPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!arrInsertLowered.ok) { eprint(arrInsertLowered.why); return 191; }
    let sawArrSet: boolean = false;
    let sawOldRead: boolean = false;
    let sawOldRelease: boolean = false;
    for o in arrInsertLowered.ops {
        if (o.kind_tag == 149 && o.str == "__map_set_impl") { sawArrSet = true; }
        if (o.kind_tag == 149 && o.str == "__map_lookup_val") { sawOldRead = true; }
        if (o.kind_tag == 149 && o.str == releaseName) { sawOldRelease = true; }
    }
    if (!sawArrSet || !sawOldRead || !sawOldRelease) { return 192; }
    let intInsertGraph = ssa.SFunc { name: "int_insert", nparams: 3, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), inst(6, 2, [], 2),
            ssa.SInst { kind_tag: ssasem.map_insert(), result: 3, args: [0, 1, 2], imm: 0, str: "" }], term: ret(3) }] };
    let intInsert = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: intInsertGraph, values: [intMapTy, i32ty, i32ty, intMapTy], params: [intMapTy, i32ty, i32ty], result: intMapTy, records: semrecords.no_records(), enums: [], calls: [] };
    let intInsertPlan = ssaunits.plan(intInsert, [3, 1, 1], ssaunits.no_view());
    if (!intInsertPlan.ok) { eprint(intInsertPlan.why); return 174; }
    let intInsertLowered = ssarc.lower(intInsert, [3, 1, 1], intInsertPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!intInsertLowered.ok) { eprint(intInsertLowered.why); return 175; }
    // A routed insert calls core/map's set, whose own copy-on-write hands
    // back a map the frame alone holds; when that is a copy, the receiver is
    // released after the call. An owned receiver over scalar columns retains
    // nothing.
    let sawIntSet: boolean = false;
    let sawIntCopyRelease: boolean = false;
    for o in intInsertLowered.ops {
        if (o.kind_tag == 149 && o.str == "__map_set_impl") { sawIntSet = true; }
        if (o.str == "__fern_rc_dec") { sawIntCopyRelease = true; }
        if (o.str == "__fern_rc_inc") { return 176; }
    }
    if (!sawIntSet || !sawIntCopyRelease) { return 177; }
    // The length borrows the map: core/map's len reads it and the owned map
    // is still dropped by its own helper on the way out.
    let lenMapGraph = ssa.SFunc { name: "map_len", nparams: 1, nvals: 2, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0),
            ssa.SInst { kind_tag: ssasem.map_len(), result: 1, args: [0], imm: 0, str: "" }], term: ret(1) }] };
    let lenMap = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: lenMapGraph, values: [intMapTy, i32ty], params: [intMapTy], result: i32ty, records: semrecords.no_records(), enums: [], calls: [] };
    let lenMapPlan = ssaunits.plan(lenMap, [3], ssaunits.no_view());
    if (!lenMapPlan.ok) { eprint(lenMapPlan.why); return 178; }
    let lenMapLowered = ssarc.lower(lenMap, [3], lenMapPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!lenMapLowered.ok) { eprint(lenMapLowered.why); return 179; }
    let sawLen: boolean = false;
    let sawLenFree: boolean = false;
    for o in lenMapLowered.ops {
        if (o.str == "__map_len_impl") { sawLen = true; }
        if (o.str == "__map_drop_impl") { sawLenFree = true; }
    }
    if (!sawLen || !sawLenFree) { return 180; }
    // An UNREACHABLE end (terminator 4) is the live end of a value-returning
    // body the checker proved never runs: it releases every unit the frame
    // still holds, as a return does, and aborts. An owned string parameter
    // still dies there and no value is read.
    let endGraph = ssa.SFunc { name: "ends", nparams: 1, nvals: 1, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)],
            term: ssa.STerm { kind_tag: 4, value: 0, cond: 0, target: 0, t: 0, f: 0 } }] };
    let endFunc = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: endGraph, values: [strTy], params: [strTy], result: i32ty, records: semrecords.no_records(), enums: [], calls: [] };
    let endPlan = ssaunits.plan(endFunc, [3], ssaunits.no_view());
    if (!endPlan.ok) { eprint(endPlan.why); return 181; }
    let endLowered = ssarc.lower(endFunc, [3], endPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!endLowered.ok) { eprint(endLowered.why); return 182; }
    let sawExit: boolean = false;
    let sawEndFree: boolean = false;
    for o in endLowered.ops {
        if (ir.render_op(o) == "exit") { sawExit = true; }
        if (o.str == "__fern_str_free") { sawEndFree = true; }
    }
    if (!sawExit || !sawEndFree) { return 183; }
    // A get answers an Option of a narrow value column in a fresh box of its
    // own, handed to the frame: core/map's get borrows the map and the key,
    // and the owned map is still dropped on the way out.
    let optI32Ty: typeinfo.Type = typeinfo.TypeUnion { name: "Option", args: [i32ty] };
    let getGraph = ssa.SFunc { name: "map_get", nparams: 2, nvals: 3, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.map_get(), result: 2, args: [0, 1], imm: 0, str: "" }], term: ret(2) }] };
    let optEnum = semrecords.Enum { views: false, ty: optI32Ty, variants: [semrecords.Variant { name: "Some", fields: [semrecords.Field { name: "__ev", ty: i32ty }] },
        semrecords.Variant { name: "None", fields: [] }], layout: semrecords.layout_option() };
    let getMap = ssasem.Func { envs: [], anchors: [], dyns: [], shadows: [], finalizers: [], map_module: true, dbg_vals: [], dbg_names: [], graph: getGraph, values: [intMapTy, i32ty, optI32Ty], params: [intMapTy, i32ty], result: optI32Ty, records: semrecords.no_records(), enums: [optEnum], calls: [] };
    let getPlan = ssaunits.plan(getMap, [3, 1], ssaunits.no_view());
    if (!getPlan.ok) { eprint(getPlan.why); return 184; }
    let getLowered = ssarc.lower(getMap, [3, 1], getPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!getLowered.ok) { eprint(getLowered.why); return 185; }
    let sawGet: boolean = false;
    let sawGetFree: boolean = false;
    for o in getLowered.ops {
        if (o.str == "__map_get_impl") { sawGet = true; }
        if (o.str == "__map_drop_impl") { sawGetFree = true; }
    }
    if (!sawGet || !sawGetFree) { return 186; }
    // A map and a boolean spell different drop helpers: without a key of its
    // own a map would key as the fall-through leaf does.
    if (ssasem.type_key(mapTy) == ssasem.type_key(typeinfo.TypeBool { tag: 0 })) { return 144; }
    if (ssasem.type_key(mapTy) == ssasem.type_key(strMapTy)) { return 145; }
    // A dyn type keys by its trait set, so two dyn types, and an array of
    // either, never share a release with each other or with a leaf; the set's
    // written spelling is escaped into symbol characters, one way only.
    let dynA: typeinfo.Type = typeinfo.TypeDyn { traits: "Aa" };
    let dynPair: typeinfo.Type = typeinfo.TypeDyn { traits: "Pair[i32, u32]" };
    let boolTy: typeinfo.Type = typeinfo.TypeBool { tag: 0 };
    if (ssasem.type_key(dynA) == ssasem.type_key(typeinfo.TypeDyn { traits: "Bb" })) { return 230; }
    if (ssasem.type_key(dynA) == ssasem.type_key(boolTy)) { return 231; }
    if (ssasem.type_key(typeinfo.TypeArray { elem: dynA, view: false }) == ssasem.type_key(typeinfo.TypeArray { elem: boolTy, view: false })) { return 232; }
    if (ssasem.type_key(dynPair) != "$dyn$Pair_5bi32_2c_20u32_5d") { eprint(ssasem.type_key(dynPair)); return 233; }
    if (ssasem.type_key(typeinfo.TypeDyn { traits: "error.Error" }) == ssasem.type_key(typeinfo.TypeDyn { traits: "error__Error" })) { return 234; }
    // An update that keeps a field the construction alone reads neither
    // reads nor stores it while the donor is unique: after the allocation the
    // construction copies the field from the donor, retained, under the
    // token's null arm, then releases the donor, and the field it replaces is
    // stored last, unconditionally.
    let keepGraph = ssa.SFunc { name: "keep", nparams: 2, nvals: 4, entry: 7, takes_env: false,
        blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1),
            ssa.SInst { kind_tag: ssasem.record_get(), result: 2, args: [0], imm: 0, str: "xs" },
            inst(ssasem.record_new(), 3, [2, 1], 0)], term: ret(3) }] };
    let keepFunc = ssasem.Func { ...growFunc, graph: keepGraph, values: [growType, i32ty, f.result, growType] };
    let keepPlan = ssaunits.plan(keepFunc, [ssaunits.counted_mode(), 1], ssaunits.no_view());
    if (!keepPlan.ok) { eprint(keepPlan.why); return 235; }
    let keepLowered = ssarc.lower(keepFunc, [ssaunits.counted_mode(), 1], keepPlan, irtables.struct_tab_empty(), ssaunits.no_grows(), util.name_index([]), ssaunits.no_view(), suspend.none());
    if (!keepLowered.ok) { eprint(keepLowered.why); return 236; }
    let keepUnique: i32 = 0 - 1;
    let keepReuse: i32 = 0 - 1;
    let keepGet: i32 = 0 - 1;
    let keepGets: i32 = 0;
    let keepRetains: i32 = 0;
    let keepSet0: i32 = 0 - 1;
    let keepSet1: i32 = 0 - 1;
    let keepNe: i32 = 0 - 1;
    let ko: i32 = 0;
    while (ko < keepLowered.ops.len()) {
        let o: ir.Op = keepLowered.ops[ko];
        if (o.str == "__fern_rc_is_unique" && keepUnique < 0) { keepUnique = ko; }
        if (o.str == "__fern_alloc_reuse") { keepReuse = ko; }
        if (o.str == "__fern_rc_inc") { keepRetains = keepRetains + 1; }
        if (ir.render_op(o) == "struct_get 0") { keepGet = ko; keepGets = keepGets + 1; }
        if (ir.render_op(o) == "struct_set 0") { keepSet0 = ko; }
        if (ir.render_op(o) == "struct_set 1") { keepSet1 = ko; }
        if (o.kind_tag == ir.kind_id("ne") && keepReuse >= 0 && keepNe < 0) { keepNe = ko; }
        ko = ko + 1;
    }
    if (keepUnique < 0 || keepReuse < 0 || keepGets != 1 || keepRetains != 1) { return 237; }
    if (keepGet < keepReuse || keepLowered.ops[keepGet - 3].kind_tag != ir.kind_id("if") || keepLowered.ops[keepGet + 1].str != "__fern_rc_inc") { return 238; }
    if (keepSet0 != keepGet + 2 || keepNe < keepSet0 || keepSet1 < keepNe) { return 239; }
    let keepVerified: irverifyrc.RcResult = irverifyrc.verify_rc_fn("keep", keepLowered.ops);
    if (keepVerified.checked != 1 || keepVerified.problems.len() != 0) { if (keepVerified.skips.len() > 0) { eprint(keepVerified.skips[0]); } return 240; }
    return 0;
}
`
}

func TestSelfHostSSAPhysicalRCRejects(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	source := physicalRCRejectSource()
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
		{"mixed-scalar-tuple", `let bt: typeinfo.Type = typeinfo.TypeBool { tag: 0 }; let mixed: typeinfo.Type = typeinfo.TypeTuple { elements: [bt, row] };
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
	selection.WriteString("let selection = args(); let f = fixture_0(); let modes: i32[] = [];\n")
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
			literal := strings.Split(strings.Split(source, "let src: string = ")[1], "\n")[0]
			fmt.Fprintf(&sources, "if (selection[2] == %q) { src = %s }\n", tc.name, literal)
		}
	}
	main = strings.Replace(main, "let f = fixture();\n    let modes: i32[] = [];", selection.String(), 1)
	main = strings.Replace(main, "let mod = parser.parse_module", sources.String()+"let mod = parser.parse_module", 1)
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
