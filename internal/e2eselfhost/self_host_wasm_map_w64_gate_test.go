package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapW64GateDriver asks wasm_ir.needs_of about a unit holding one op, for each
// map op that reads or writes an 8-byte value column. A cached link reads each
// unit's needs back without re-lowering it, so every one of them has to record
// @uses_map_w64 on its own: a unit holding only a wide MapIter's value read,
// beside the unit that built the map, would otherwise call
// $__fern_mapiter_value_w64 in a link that never emitted it. The exit code is a
// bitmask of the ops whose unit does not record the need, plus the narrow get
// whose unit must not.
const mapW64GateDriver = `import "./ir";
import "./irtables";
import "./util";
import "./wasm_ir";

function unit(o: ir.Op): irtables.LowerResult[] {
    return [irtables.LowerResult { ok: true, why: "", ops: [o], n_locals: 0,
        n_params: 0, erased_wide: false, superseded: false, arr_slots: [], i64_slots: [],
        f64_slots: [], str_slots: [], name: "", result_kind: irtables.result_i32(), dbg_slots: [], dbg_names: [], dbg_types: [] }];
}

function records(o: ir.Op): boolean {
    return util.index_of_str(wasm_ir.needs_of(unit(o)), "@uses_map_w64") >= 0;
}

function main(): i32 {
    let wide: ir.Op[] = [
        ir.op_map_set(1, false, false, false, true, false, "", 1, 0),
        ir.op_map_get(1, "", 1),
        ir.op_map_get_or(1, "", 1),
        ir.op_map_values(0, 1),
        ir.op_map_iter(1, 0),
        ir.op_mapiter_value(1),
        ir.op_mapiter_value(2),
    ];
    let missing: i32 = 0;
    let bit: i32 = 1;
    for o in wide {
        if (!records(o)) { missing = missing + bit; }
        bit = bit * 2;
    }
    if (records(ir.op_map_get(1, "", 0))) { missing = missing + bit; }
    return missing;
}
`

func TestSelfHostWasmMapW64GateCountsEveryWideOp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "fnsigs.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "map_w64_gate.fern"), []byte(mapW64GateDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "map_w64_gate.fern", "map_w64_gate")
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code == 0 {
		return
	}
	names := []string{"map_set", "map_get", "map_get_or", "map_values", "map_iter", "mapiter_value"}
	var bad []string
	for i, n := range names {
		if code&(1<<i) != 0 {
			bad = append(bad, "wide "+n+" does not record @uses_map_w64")
		}
	}
	if code&(1<<len(names)) != 0 {
		bad = append(bad, "a narrow map_get records @uses_map_w64")
	}
	if len(bad) == 0 {
		t.Fatalf("gate driver exited %d", code)
	}
	t.Error(strings.Join(bad, "; "))
}

// mapK64GateDriver is mapW64GateDriver for an 8-byte KEY column (#10005):
// every op that can reach a `_k64` helper — key kind 3 on the keyed ops, the
// key-wide flag on keys, iter and the iterator's key read — has to record
// @uses_map_k64 on its own unit, and a narrow-keyed get must not.
const mapK64GateDriver = `import "./ir";
import "./irtables";
import "./util";
import "./wasm_ir";

function unit(o: ir.Op): irtables.LowerResult[] {
    return [irtables.LowerResult { ok: true, why: "", ops: [o], n_locals: 0,
        n_params: 0, erased_wide: false, superseded: false, arr_slots: [], i64_slots: [],
        f64_slots: [], str_slots: [], name: "", result_kind: irtables.result_i32(), dbg_slots: [], dbg_names: [], dbg_types: [] }];
}

function records(o: ir.Op): boolean {
    return util.index_of_str(wasm_ir.needs_of(unit(o)), "@uses_map_k64") >= 0;
}

function main(): i32 {
    let wide: ir.Op[] = [
        ir.op_map_new(3, ""),
        ir.op_map_set(3, false, false, false, false, false, "", 0, 0),
        ir.op_map_get(3, "", 0),
        ir.op_map_has(3, ""),
        ir.op_map_get_or(3, "", 0),
        ir.op_map_delete(3, "", false, 0),
        ir.op_map_keys(0, 1),
        ir.op_map_iter(0, 1),
        ir.op_mapiter_key(1),
    ];
    let names: string[] = ["map_new", "map_set", "map_get", "map_has", "map_get_or", "map_delete", "map_keys", "map_iter", "mapiter_key"];
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < wide.len()) {
        if (!records(wide[i])) { print("wide-key " + names[i] + " does not record @uses_map_k64"); bad = 1; }
        i = i + 1;
    }
    if (records(ir.op_map_get(1, "", 0))) { print("a narrow-keyed map_get records @uses_map_k64"); bad = 1; }
    return bad;
}
`

func TestSelfHostWasmMapK64GateCountsEveryWideKeyOp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "fnsigs.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "map_k64_gate.fern"), []byte(mapK64GateDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "map_k64_gate.fern", "map_k64_gate")
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	out, _ := cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("gate driver exited %d:\n%s", code, out)
	}
}
