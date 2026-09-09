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
// Explicit caller contract for ABI tests. Semantic calls are not yet admitted
// by ssasem; using AST escape inference here would test a different contract.
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
    FIXTURE
    var graph = ssa.SFunc { name: "produce", nparams: params.len(), nvals: types.len(), entry: 7,
        takes_env: false, blocks: [ssa.SBlock { id: 7, preds: [], insts: ops, term: ret(result) }] };
    return ssasem.Func { graph: graph, values: types, params: params, result: row };
}
function main(): i32 {
    var f = fixture();
    var modes: i32[] = MODES;
    var p = ssaunits.plan(f, modes);
    var lowered = ssarc.lower(f, modes, p);
    if (!lowered.ok) { eprint(lowered.why); return 1; }
    var src: string = "function produce(): i32[] { return [7]; } function main(): i32 { var j = 0; while (j < 32) { var xs = produce(); var churn = [91, 92, 93]; if (xs.len() != 1 || xs[0] != 7 || churn[0] != 91) { return 2; } j = j + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }";
    var mod = parser.parse_module(lexer.tokenize(src));
    var tab = irlower.struct_tab(mod.structs);
    var base = ircore.wp_fn_sigs(mod.funcs, tab);
    var g = ircore.lower_gated(mod, tab, base, [], false);
    if (!g.ok) { return 3; }
    var cache: irlower.LowerResult[] = [];
    var at: i32 = 0;
    for fd in mod.funcs {
        if (fd.name == "produce") { cache = cache.append(lowered); }
        else if (modes.len() > 0) { cache = cache.append(caller(g.cache[at], modes[0])); }
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
	if modes != "[]" {
		parameter := "seed: i32[]"
		if modes == "[3]" {
			parameter = "own " + parameter
		}
		source = strings.Replace(source, "function produce(): i32[] { return [7]; }", "function produce("+parameter+"): i32[] { return seed; }", 1)
		source = strings.Replace(source, "var j = 0; while", "var seed = [7]; var before = seed; var j = 0; while", 1)
		source = strings.Replace(source, "var xs = produce();", "var xs = produce(seed);", 1)
		source = strings.Replace(source, "churn[0] != 91", "churn[0] != 91 || before[0] != 7 || seed[0] != 7", 1)
	}
	return `import "./ssarc"; import "./ssasem"; import "./ssaunits"; import "./ssa";
import "./typeinfo"; import "./parser"; import "./lexer"; import "./irlower"; import "./ir";
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
function main(): i32 {
    var f = fixture();
    var p = ssaunits.plan(f, []);
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, ok: false }), "missing successful unit plan")) { return 1; }
    if (!refused(ssarc.lower(f, [], ssaunits.Plan { ...p, steps: [] }), "missing or duplicate entry step")) { return 2; }
    var b = f.graph.blocks[0];
    var first = ssa.SBlock { ...b, term: ssa.STerm { kind_tag: 2, target: 27, value: 0, cond: 0, t: 0, f: 0 } };
    var last = ssa.SBlock { id: 27, preds: [7], insts: [], term: b.term };
    var graph = ssa.SFunc { ...f.graph, blocks: [first, last] };
    var cfg = ssasem.Func { ...f, graph: graph };
    var cp = ssaunits.plan(cfg, []);
    if (!cp.ok) { eprint(cp.why); return 3; }
    if (!refused(ssarc.lower(cfg, [], cp), "physical RC needs straight-line return graph")) { return 4; }
    var types: typeinfo.Type[] = [typeinfo.TypeString { tag: 0 }, typeinfo.TypeArray { elem: typeinfo.TypeI32 { width: 64, unsigned: false, is_char: false } }];
    for ty in types {
        var g = ssa.SFunc { name: "unsupported", nparams: 1, nvals: 1, entry: 7, takes_env: false,
            blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }] };
        var typed = ssasem.Func { graph: g, values: [ty], params: [ty], result: ty };
        var plan = ssaunits.plan(typed, [2]);
        if (!plan.ok) { eprint(plan.why); return 5; }
        if (!refused(ssarc.lower(typed, [2], plan), "unsupported physical RC value type")) { return 6; }
    }
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
	return []physicalRCCase{
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
					cmd := runX86_64Bin(runner, driver, emitTarget, tc.name)
					if selfhost {
						cmd = runArm64Bin(armDriverRunner, driver, emitTarget, tc.name)
					}
					cmd.Env = append(os.Environ(), mode)
					var diagnostics bytes.Buffer
					cmd.Stderr = &diagnostics
					output, err := cmd.Output()
					if err != nil {
						t.Fatalf("physical lowering: %v\n%s", err, diagnostics.String())
					}
					var run *exec.Cmd
					switch target {
					case "arm64-linux":
						armGCC, armRunner := arm64Tooling(t)
						run = runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, tc.name+"-arm", string(output)))
					case "x86-64-linux", "x86-64-sanitize":
						run = runX86_64Bin(runner, buildBin(t, gcc, dir, tc.name+"-x86", string(output)))
					case "wasm32-wasi":
						wat := filepath.Join(dir, tc.name+".wat")
						if err := os.WriteFile(wat, output, 0o644); err != nil {
							t.Fatal(err)
						}
						run = exec.Command("wasmtime", "run", wat)
					}
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
				})
			}
		})
	}
}
