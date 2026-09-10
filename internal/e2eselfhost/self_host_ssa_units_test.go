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

const unitHelpers = `
function find(p: ssaunits.Plan, block: i32, point: i32, target: i32): ssaunits.Step {
    for s in p.steps { if (s.block == block && s.point == point && s.target == target) { return s; } }
    return ssaunits.Step { block: 0 - 1, point: 0 - 1, target: 0 - 1, supplies: [], drops: [] };
}
function replace(p: ssaunits.Plan, replacement: ssaunits.Step): ssaunits.Plan {
    var steps: ssaunits.Step[] = [];
    for s in p.steps {
        if (s.block == replacement.block && s.point == replacement.point && s.target == replacement.target) {
            steps = steps.append(replacement);
        } else { steps = steps.append(s); }
    }
    return ssaunits.Plan { ...p, steps: steps };
}
function supply(s: ssaunits.Step, at: i32, value: i32, slot: i32, mode: i32): boolean {
    if (at < 0 || at >= s.supplies.len()) { return false; }
    var item = s.supplies[at];
    return item.value == value && item.slot == slot && item.mode == mode;
}
function drops(s: ssaunits.Step, expected: i32[]): boolean {
    if (s.drops.len() != expected.len()) { return false; }
    var i: i32 = 0;
    while (i < expected.len()) { if (s.drops[i] != expected[i]) { return false; } i = i + 1; }
    return true;
}
`

const unitDuplicate = `
var stored: typeinfo.Type = typeinfo.TypeTuple { elements: [sa, sa] };
params = [sa]; types = [sa, stored]; result = types[1]; modes = [3];
graph = ssa.SFunc { name: "duplicate", nparams: 1, nvals: 2, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(ssasem.tuple_new(), 1, [0, 0], 0)], term: ret(1) }
] };
`

const unitLoop = `
params = [sa, bt]; types = [sa, bt, sa]; result = sa; modes = [3, 1];
graph = ssa.SFunc { name: "loop", nparams: 2, nvals: 3, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 37, preds: [17], insts: [], term: ret(2) },
    ssa.SBlock { id: 27, preds: [17], insts: [], term: br(17) },
    ssa.SBlock { id: 17, preds: [7, 27], insts: [inst(8, 2, [0, 2], 0)], term: ssa.STerm { kind_tag: 3, cond: 1, t: 27, f: 37, target: 0, value: 0 } },
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1)], term: br(17) }
] };
`

// A call to `g(borrowed, counted)` returning a fresh array: the counted
// argument is supplied like a construction operand, the borrowed one is only
// read, and the result is a unit of this function's own.
const unitCall = `
var callee: ssasem.Contract = contract("g", [sa, sa], [2, 3], sa);
calls = [callee];
params = [sa, sa]; types = [sa, sa, sa, sa]; result = sa; modes = [3, 3];
graph = ssa.SFunc { name: "call", nparams: 2, nvals: 4, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), inst(6, 1, [], 1), call_inst(2, "g", [0, 1]), call_inst(3, "g", [0, 0])], term: ret(3) }
] };
`

// A string constant and a concatenation are units of this function's own,
// released when dead; a borrowed string parameter returned is retained.
const unitString = `
params = [st]; types = [st, st, st]; result = st; modes = [2];
graph = ssa.SFunc { name: "concat", nparams: 1, nvals: 3, entry: 7, takes_env: false, blocks: [
    ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0), ssa.SInst { kind_tag: 5, result: 1, args: [], imm: 0, str: "a" }, ssa.SInst { kind_tag: 9, result: 2, args: [0, 1], imm: 0, str: "+" }], term: ret(0) }
] };
`

type unitCase struct{ name, setup, check, mutate, want string }

func unitCases() []unitCase {
	base := []unitCase{
		{"nested-projections", "", `
var r = find(p, 7, ssaunits.return_point(), 0 - 1);
if (!supply(r, 0, 5, 0, ssaunits.retain_unit()) || !drops(r, [0])) { return 11; }
var created = find(p, 7, 6, 0 - 1);
if (!supply(created, 0, 5, 0, ssaunits.retain_unit()) || !drops(created, [6])) { return 12; }
if (!drops(find(p, 7, 9, 0 - 1), [8])) { return 13; }
`, "", ""},
		{"borrowed-root", "modes = [2, 1];", `
var r = find(p, 7, ssaunits.return_point(), 0 - 1);
if (!supply(r, 0, 5, 0, ssaunits.retain_unit()) || !drops(r, [])) { return 14; }
`, "", ""},
		{"duplicate-store", unitDuplicate, `
var s = find(p, 7, 1, 0 - 1);
if (!supply(s, 0, 0, 0, ssaunits.retain_unit()) || !supply(s, 1, 0, 1, ssaunits.move_unit()) || !drops(s, [])) { return 15; }
if (!supply(find(p, 7, ssaunits.return_point(), 0 - 1), 0, 1, 0, ssaunits.move_unit())) { return 16; }
`, "", ""},
		{"borrowed-store", unitDuplicate + "modes = [2];", `
var s = find(p, 7, 1, 0 - 1);
if (!supply(s, 0, 0, 0, ssaunits.retain_unit()) || !supply(s, 1, 0, 1, ssaunits.retain_unit())) { return 17; }
`, "", ""},
		{"branch-phis", semanticPhi + "modes = [1, 3, 3];", `
if (!drops(find(p, 7, ssaunits.edge_point(), 17), [2]) || !drops(find(p, 7, ssaunits.edge_point(), 27), [1])) { return 18; }
if (!supply(find(p, 17, ssaunits.edge_point(), 37), 0, 1, 0, ssaunits.move_unit())) { return 19; }
if (!supply(find(p, 27, ssaunits.edge_point(), 37), 0, 2, 0, ssaunits.move_unit())) { return 20; }
`, "", ""},
		{"loop-phi", unitLoop, `
if (!supply(find(p, 7, ssaunits.edge_point(), 17), 0, 0, 0, ssaunits.move_unit())) { return 21; }
if (!supply(find(p, 27, ssaunits.edge_point(), 17), 0, 2, 0, ssaunits.move_unit())) { return 22; }
`, "", ""},
		{"unused-parameter", unitDuplicate + `result = typeinfo.TypeVoid { tag: 0 }; graph = ssa.SFunc { ...graph, nvals: 1, blocks: [ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0 - 1) }] }; types = [sa];`, `if (!drops(find(p, 7, ssaunits.entry_point(), 0 - 1), [0])) { return 23; }`, "", ""},
		{"missing-supply", unitDuplicate, "", `var s = find(p, 7, 1, 0 - 1); p = replace(p, ssaunits.Step { ...s, supplies: [] });`, "unit supply arity"},
		{"wrong-slot", unitDuplicate, "", `var s = find(p, 7, 1, 0 - 1); var a = s.supplies[0]; p = replace(p, ssaunits.Step { ...s, supplies: [ssaunits.Supply { ...a, slot: 1 }, s.supplies[1]] });`, "unit supply identity or slot"},
		{"double-move", unitDuplicate, "", `var s = find(p, 7, 1, 0 - 1); var a = s.supplies[0]; p = replace(p, ssaunits.Step { ...s, supplies: [ssaunits.Supply { ...a, mode: 2 }, s.supplies[1]] });`, "move without counted unit"},
		{"borrowed-move", unitDuplicate + "modes = [2];", "", `var s = find(p, 7, 1, 0 - 1); var a = s.supplies[0]; p = replace(p, ssaunits.Step { ...s, supplies: [ssaunits.Supply { ...a, mode: 2 }, s.supplies[1]] });`, "move without counted unit"},
		{"invalid-mode", unitDuplicate, "", `var s = find(p, 7, 1, 0 - 1); var a = s.supplies[0]; p = replace(p, ssaunits.Step { ...s, supplies: [ssaunits.Supply { ...a, mode: 0 }, s.supplies[1]] });`, "invalid unit supply mode"},
		{"premature-parent-drop", "", "", `var s = find(p, 7, 2, 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [0] });`, "borrow outlives container unit"},
		{"leaked-parent", "", "", `var s = find(p, 7, ssaunits.return_point(), 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [] });`, "counted unit leaks at return"},
		{"drop-moved-value", unitDuplicate, "", `var s = find(p, 7, 1, 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [0] });`, "drop without counted unit"},
		{"missing-return", unitDuplicate, "", `var steps: ssaunits.Step[] = []; for s in p.steps { if (s.point != ssaunits.return_point()) { steps = steps.append(s); } } p = ssaunits.Plan { ...p, steps: steps };`, "missing or duplicate return step"},
		{"duplicate-step", unitDuplicate, "", `p = ssaunits.Plan { ...p, steps: p.steps.append(p.steps[0]) };`, "missing or duplicate operation step"},
		{"extra-step", unitDuplicate, "", `p = ssaunits.Plan { ...p, steps: p.steps.append(ssaunits.Step { block: 999, point: 0, target: 0 - 1, supplies: [], drops: [] }) };`, "extra unit plan steps"},
		{"broken-edge-invariant", semanticPhi + "modes = [1, 3, 3];", "", `var s = find(p, 7, ssaunits.edge_point(), 17); p = replace(p, ssaunits.Step { ...s, drops: [] });`, "edge unit invariant mismatch"},
		{"changed-parameter-contract", unitDuplicate, "", `modes = [2];`, "move without counted unit"},
		{"missing-entry", unitDuplicate, "", `var steps: ssaunits.Step[] = []; for s in p.steps { if (s.point != ssaunits.entry_point()) { steps = steps.append(s); } } p = ssaunits.Plan { ...p, steps: steps };`, "missing or duplicate entry step"},
		{"invalid-drop-id", unitDuplicate, "", `var s = find(p, 7, 1, 0 - 1); p = replace(p, ssaunits.Step { ...s, drops: [99] });`, "drop value out of range"},
		{"call-supplies", unitCall, `
var first = find(p, 7, 2, 0 - 1);
if (first.supplies.len() != 1 || !supply(first, 0, 1, 1, ssaunits.move_unit()) || !drops(first, [2])) { return 24; }
var second = find(p, 7, 3, 0 - 1);
if (second.supplies.len() != 1 || !supply(second, 0, 0, 1, ssaunits.move_unit()) || !drops(second, [])) { return 25; }
if (!supply(find(p, 7, ssaunits.return_point(), 0 - 1), 0, 3, 0, ssaunits.move_unit())) { return 26; }
`, "", ""},
		{"call-borrowed-argument", unitCall + "modes = [2, 3];", `
var second = find(p, 7, 3, 0 - 1);
if (!supply(second, 0, 0, 1, ssaunits.retain_unit()) || !drops(second, [])) { return 27; }
`, "", ""},
		{"changed-call-contract", unitCall, "", `f = ssasem.Func { ...f, calls: [contract("g", [sa, sa], [2, 2], sa)] };`, "unit supply arity"},
		{"call-contract-mode", unitCall, "", `f = ssasem.Func { ...f, calls: [contract("g", [sa, sa], [1, 3], sa)] };`, "reference parameter mode"},
		{"dropped-call-contract", unitCall, "", `f = ssasem.Func { ...f, calls: [] };`, "missing call contract"},
		{"string-units", unitString, `
var s = find(p, 7, 2, 0 - 1);
if (s.supplies.len() != 0 || !drops(s, [1, 2])) { return 28; }
if (!supply(find(p, 7, ssaunits.return_point(), 0 - 1), 0, 0, 0, ssaunits.retain_unit())) { return 29; }
`, "", ""},
	}
	base = append(base, unitRecordCases()...)
	return append(base, unitEnumCases()...)
}

func unitSource(indices []int) (string, string) {
	var source, main, want strings.Builder
	source.WriteString("import \"./ssa\";\nimport \"./ssasem\";\nimport \"./semtypes\";\nimport \"./semrecords\";\nimport \"./typeinfo\";\nimport \"./ssaunits\";\n")
	source.WriteString(semanticHelpers + unitHelpers)
	main.WriteString("function main(): i32 {\n")
	for _, i := range indices {
		tc := unitCases()[i]
		fmt.Fprintf(&source, "function unit_case_%d(): i32 {\n%s\nvar modes: i32[] = [3, 1];\n%s\n", i, semanticFixture, tc.setup)
		source.WriteString(`
var f = ssasem.Func { graph: graph, values: types, params: params, result: result, records: records, enums: enums, calls: calls };
var p = ssaunits.plan(f, modes);
if (!p.ok) { print(p.why); return 1; }
`)
		if i == 0 {
			source.WriteString(`
var bad = ssaunits.plan(f, []);
if (bad.ok || bad.steps.len() != 0 || bad.why != "parameter mode dimensions") { return 31; }
bad = ssaunits.plan(f, [1, 1]);
if (bad.ok || bad.steps.len() != 0 || bad.why != "reference parameter mode") { return 32; }
bad = ssaunits.plan(f, [3, 3]);
if (bad.ok || bad.steps.len() != 0 || bad.why != "scalar parameter mode") { return 33; }
var opaque_types: typeinfo.Type[] = [view, typeinfo.TypeStruct { name: "Box", args: [] }];
for opaque in opaque_types {
    var g = ssa.SFunc { name: "opaque", nparams: 1, nvals: 1, entry: 7, takes_env: false, blocks: [
        ssa.SBlock { id: 7, preds: [], insts: [inst(6, 0, [], 0)], term: ret(0) }
    ] };
    bad = ssaunits.plan(ssasem.Func { graph: g, values: [opaque], params: [opaque], result: opaque, records: [], enums: [], calls: [] }, [3]);
    if (bad.ok || bad.steps.len() != 0 || bad.why != "unsupported counted-unit type") { return 34; }
}
`)
		}
		source.WriteString(tc.check + "\n" + tc.mutate + "\n")
		source.WriteString("print(ssaunits.verify(f, modes, p)); return 0; }\n")
		fmt.Fprintf(&main, "if (unit_case_%d() != 0) { return %d; }\n", i, i+1)
		want.WriteString(tc.want + "\n")
	}
	main.WriteString("return 0; }\n")
	source.WriteString(main.String())
	return source.String(), want.String()
}

func TestSelfHostSSAUnits(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	for i, tc := range unitCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "ssaunits.fern")
			source, want := unitSource([]int{i})
			if err := os.WriteFile(filepath.Join(dir, "units.fern"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := buildSelfHostBin(t, gcc, dir, "units.fern", "units")
			got, err := runX86_64Bin(runner, bin).CombinedOutput()
			if err != nil || string(got) != want {
				t.Fatalf("unit verification: %v\ngot %q\nwant %q", err, got, want)
			}
		})
	}
}

func TestSelfHostSSAUnitsIRArm64(t *testing.T)  { testUnitsIR(t, "arm64-linux") }
func TestSelfHostSSAUnitsIRX86_64(t *testing.T) { testUnitsIR(t, "x86-64-linux") }
func TestSelfHostSSAUnitsIRWasm(t *testing.T)   { testUnitsIR(t, "wasm32-wasi") }

func testUnitsIR(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	var armGCC, armRunner, wasmtime string
	if target == "arm64-linux" {
		armGCC, armRunner = arm64Tooling(t)
	}
	if target == "wasm32-wasi" {
		var err error
		wasmtime, err = exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	dir := copySelfHostTree(t)
	copySelfHostDriver(t, dir, "ssaunits.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	var indices []int
	for i := range unitCases() {
		indices = append(indices, i)
	}
	source, want := unitSource(indices)
	entry := filepath.Join(dir, "units.fern")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(runner, driver, "-target", target, "-emit", "asm", entry, root)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		t.Fatalf("self-host compile: %v\n%s", err, diagnostics.String())
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		run = runX86_64Bin(runner, buildBin(t, gcc, dir, "units", string(output)))
	case "arm64-linux":
		run = runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, "units", string(output)))
	case "wasm32-wasi":
		wat := filepath.Join(dir, "units.wat")
		if err := os.WriteFile(wat, output, 0o644); err != nil {
			t.Fatal(err)
		}
		run = exec.Command(wasmtime, "run", wat)
	}
	got, err := run.CombinedOutput()
	if err != nil || string(got) != want {
		t.Fatalf("unit verification: %v\ngot %q\nwant %q", err, got, want)
	}
}
