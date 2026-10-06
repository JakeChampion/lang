package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostNdarrayAdapterPrune(t *testing.T) {
	const src = `import "./ir";
import "./irtables";
import "./lexer";
import "./parser";
import "./semndprune";
function body(name: string, ops: ir.Op[]): irtables.LowerResult {
  return irtables.LowerResult { ok: true, ops: ops, n_locals: 0, n_params: 0,
    erased_wide: false, superseded: false, arr_slots: [], i64_slots: [], f64_slots: [], str_slots: [],
    why: "", name: name, result_kind: 0, dbg_slots: [], dbg_names: [], dbg_types: [] };
}
function main(): i32 {
  let mod = parser.parse_module(lexer.tokenize("function main(): i32 { return 0; } function ndarray__inner_kernel(): i32 { return 0; }"));
  // Declaration bodies have empty names; instances carry their own names.
  let bodies = [body("", []), body("", [ir.op_call_direct("ndarray__made", 0)])];
  let extra = [body("ndarray__made", [ir.op_call_direct("ndarray__leaf", 0)]), body("ndarray__leaf", []), body("ndarray__unrelated", [])];
  let dead = semndprune.unused_adapter(mod, bodies, extra);
  if (dead.bodies[0].superseded || !dead.bodies[1].superseded || dead.bodies[1].name != "" || dead.extra.len() != 1 || dead.extra[0].name != "ndarray__unrelated") { return 1; }
  let used = semndprune.unused_adapter(mod, bodies.with(0, body("", [ir.op_call_direct("ndarray__inner_kernel", 0)])), extra);
  if (used.bodies[1].superseded || used.extra.len() != 3) { return 2; }
  let shared = semndprune.unused_adapter(mod, bodies.with(0, body("", [ir.op_call_direct("ndarray__made", 0)])), extra);
  if (!shared.bodies[1].superseded || shared.extra.len() != 3) { return 3; }
  let address = semndprune.unused_adapter(mod, bodies.with(0, body("", [ir.op_const_str("address:ndarray__inner_kernel;payload")])), extra);
  if (address.bodies[1].superseded) { return 4; }
  let exported = parser.Module { ...mod, exports: [parser.ExportBinding { func_name: "ndarray__inner_kernel", iface: "", wit: "", c_abi: true }] };
  if (semndprune.unused_adapter(exported, bodies, extra).bodies[1].superseded) { return 5; }
  let kept_child = parser.Module { ...mod, exports: [parser.ExportBinding { func_name: "ndarray__made", iface: "", wit: "", c_abi: true }] };
  let kept = semndprune.unused_adapter(kept_child, bodies, extra);
  if (!kept.bodies[1].superseded || kept.extra.len() != 3) { return 6; }
  let implicit = [body("ndarray__Record.method", []), body("__runtime", [])];
  let routed = bodies.with(1, body("", [ir.op_call_direct("ndarray__Record.method", 0), ir.op_call_direct("__runtime", 0)]));
  if (semndprune.unused_adapter(mod, routed, implicit).extra.len() != 2) { return 7; }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semndprune.fern")
	if err := os.WriteFile(filepath.Join(dir, "prune.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "prune.fern", "prune")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("adapter pruning: %v\n%s", err, out)
	}
}
