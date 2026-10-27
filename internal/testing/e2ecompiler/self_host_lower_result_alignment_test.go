package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const lowerResultAlignmentProg = `import "./lexer";
import "./parser";
import "./irtables";
import "./util";
import "./ir";
import "./sourcepos";

function body(kind: i32, name: string, params: i32): irtables.LowerState {
  return irtables.LowerState {
    ok: kind != 0, superseded: kind == 1, why: if (kind == 0) { "declined" } else { "" },
    ops: [], n_locals: params, n_params: params, erased_wide: false,
    arr_slots: [], i64_slots: [], f64_slots: [], v128_slots: [], str_slots: [],
    name: name, result_kind: irtables.result_i32(), dbg_slots: [], dbg_names: [], dbg_types: [],
    source_positions: irtables.no_source(),
  };
}

function main(): i32 {
  let source = "struct Point { x: i32 }\nfunction failed(x: i32): i32 { return x; }\nfunction (p: Point) omitted(x: i32): i32 { return p.x + x; }\nfunction produced(x: i32): i32 { return x; }\nfunction helper(x: i32): i32 { return x; }\n";
  let mod = parser.parse_module(lexer.tokenize(source));
  let decls = [mod.funcs[0], mod.funcs[1], mod.funcs[2]];
  let states = [body(0, "unused_failure", 99), body(1, "unused_superseded", 98), body(2, "declaration_name_wins", 7), body(2, "helper", 9)];
  let rows: irtables.LowerResult[] = [];
  for state in states { rows = rows.append(irtables.lower_result(state)); }
  let index = irtables.fn_sig_index(mod.funcs, decls, rows);
  let names = ["failed", "Point.omitted", "produced", "helper"];
  let expected = [1, 2, 7, 9];
  for i in 0..names.len() {
    let at = util.index_of_str(index.names, names[i]);
    if (at < 0 || index.params[at] != expected[i]) { return i + 1; }
  }
  if (util.index_of_str(index.names, "unused_failure") >= 0 || util.index_of_str(index.names, "unused_superseded") >= 0 || util.index_of_str(index.names, "declaration_name_wins") >= 0) { return 5; }
  for kind in 0..3 {
    let before = body(kind, "status", 3);
    let after = irtables.lower_result(before);
    if (after.ok != before.ok || after.superseded != before.superseded || after.why != before.why) { return 6; }
    match (irtables.body_payload(after)) {
      Some(_) => { if (kind != 2) { return 17; } },
      None => { if (kind == 2) { return 18; } }
    }
  }
  let before = irtables.LowerState {
    ...body(2, "metadata", 3), ops: [ir.op_const_i32(11), ir.op_return()], n_locals: 7, erased_wide: true,
    arr_slots: [0], i64_slots: [1], f64_slots: [2], v128_slots: [3], str_slots: [4],
    dbg_slots: [6], dbg_names: ["value"], dbg_types: ["i64"],
    source_positions: sourcepos.SourceInfo { values: [sourcepos.SourcePos { line: 41, col: "2" }], terms: [sourcepos.SourcePos { line: 42, col: "3" }], entry: sourcepos.SourcePos { line: 40, col: "1" } },
  };
  let complete = match (irtables.lower_outcome(before)) {
    Ok(body) => match (body) { Some(r) => r, None => { exit(12); } },
    Err(_) => { exit(13); }
  };
  let selected = match (irtables.body_payload(irtables.lowered_result(complete))) {
    Some(r) => r,
    None => { exit(19); }
  };
  let after = irtables.lowered_result(selected);
  if (!after.ok || after.superseded || after.name != "metadata" || after.n_params != 3 || after.n_locals != 7 || !after.erased_wide || after.result_kind != irtables.result_i32()) { return 7; }
  if (after.ops.len() != 2 || after.ops[0].i32_imm != 11 || after.ops[1].kind_tag != ir.K_RETURN) { return 8; }
  if (after.arr_slots.len() != 1 || after.arr_slots[0] != 0 || after.i64_slots[0] != 1 || after.f64_slots[0] != 2 || after.v128_slots[0] != 3 || after.str_slots[0] != 4) { return 9; }
  if (after.dbg_slots[0] != 6 || after.dbg_names[0] != "value" || after.dbg_types[0] != "i64") { return 10; }
  if (after.source_positions.entry.line != 40 || after.source_positions.entry.col != "1" || after.source_positions.values[0].line != 41 || after.source_positions.values[0].col != "2" || after.source_positions.terms[0].line != 42 || after.source_positions.terms[0].col != "3") { return 11; }
  for kind in 0..4 {
    let state = irtables.LowerState { ...before, ok: kind == 1 || kind == 2, superseded: kind == 1 || kind == 3, why: if (kind == 0 || kind == 3) { "partial refusal" } else { "" } };
    let helper = irtables.helper_outcome(state);
    if (irtables.helper_name(helper) != "metadata") { return 20; }
    let helper_view = irtables.helper_results([helper])[0];
    if (helper_view.ok != state.ok || helper_view.superseded != state.superseded || helper_view.why != state.why || helper_view.ops.len() != state.ops.len() || helper_view.dbg_names[0] != "value") { return 21; }
    match (helper) {
      Ok(r) => { if (kind != 2 || r.source_positions.entry.line != 40 || r.n_params != 3) { return 22; } },
      Err(r) => { if (kind == 2 || r.ok != state.ok || r.superseded != state.superseded || r.why != state.why || r.name != "metadata" || r.ops[0].i32_imm != 11 || r.dbg_names[0] != "value") { return 23; } }
    }
    match (irtables.lower_outcome(state)) {
      Ok(body) => {
        match (body) {
          Some(r) => { if (kind != 2 || r.name != "metadata" || r.ops.len() != 2 || r.n_params != 3 || r.source_positions.entry.line != 40) { return 14; } },
          None => { if (kind != 1) { return 15; } }
        }
      },
      Err(r) => {
        if (kind != 0 && kind != 3 || r.ok || r.superseded != (kind == 3) || r.why != "partial refusal" || r.name != "metadata" || r.ops.len() != 2 || r.ops[0].i32_imm != 11 || r.n_params != 3 || r.dbg_names[0] != "value" || r.source_positions.entry.line != 40) { return 16; }
      }
    }
  }
  return 0;
}
`

func TestSelfHostLowerResultAlignmentContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irtables.fern")
	writeResolveProject(t, dir, map[string]string{"lower_result_alignment.fern": lowerResultAlignmentProg})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "lower_result_alignment.fern", "lower_result_alignment", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("lowered signature alignment: %v\n%s", err, out)
			}
		})
	}
}

func TestSelfHostEmitBodyBoundary(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ircore.fern")
	source := `import "./ircore";` + "\n" + strings.Split(lowerResultAlignmentProg, "function main(): i32 {")[0] + `
function main(): i32 {
  let av = args();
  let state = body(util.digits_to_i32(av[1]), "stored_name", 3);
  let selected = ircore.body_for_emit("authoritative_name", irtables.lower_result(state));
  return selected.n_params - 3;
}
`
	writeResolveProject(t, dir, map[string]string{"emit_body_boundary.fern": source})
	cases := []struct {
		name    string
		kind    string
		code    int
		message string
	}{
		{"produced", "2", 0, ""},
		{"declined", "0", 3, "error: authoritative_name did not lower: declined"},
		{"superseded", "1", 3, "error: internal: superseded body reached emission: authoritative_name"},
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "emit_body_boundary.fern", "emit_body_boundary", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.kind)
					out, err := cmd.CombinedOutput()
					code := 0
					if err != nil {
						exit, ok := err.(*exec.ExitError)
						if !ok {
							t.Fatalf("body boundary command: %v\n%s", err, out)
						}
						code = exit.ExitCode()
					}
					if code != tc.code || !strings.Contains(string(out), tc.message) || tc.message == "" && len(out) != 0 {
						t.Fatalf("body boundary: exit %d, want %d, message %q\n%s", code, tc.code, tc.message, out)
					}
				})
			}
		})
	}
}
