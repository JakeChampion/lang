package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the runtime ABI directly: source programs cannot call the internal
// uniqueness helper. Both the value-producing and fused branch selectors must
// mask a tagged input; a typed alignment proof must survive lifting and avoid
// the mask for an ordinary array. The same object is unique, then shared, then
// released twice, so an incorrect count cannot hide behind a constant result.
const byteViewRCDriver = `import "./ir";
import "./irtables";
import "./ircore";
import "./lexer";
import "./parser";
import "./semlower";
import "./asmcore";
import "./asm_ir";
import "./asm_arm64_ir";
import "./wasm_ir";

function check(own ops: ir.Op[], expected: i32, error: i32): ir.Op[] {
  return ops.append(ir.op_const_i32(expected)).append(ir.op_bin("ne", 0, false))
    .append(ir.op_if(0)).append(ir.op_const_i32(error)).append(ir.op_return()).append(ir.op_end());
}
function main(): i32 {
  let av = args();
  let aligned: boolean = av[2] == "aligned-fused" || av[2] == "aligned-value";
  let value: boolean = av[2] == "value" || av[2] == "aligned-value";
  let call: ir.Op = ir.op_call_direct("__fern_rc_is_unique", 1);
  if (aligned) { call = ir.Op { ...call, i64_imm: 4 }; }
  let probe: ir.Op[] = [ir.op_load_local(0), call];
  if (value) { probe = probe.append(ir.op_return()); }
  else {
    probe = probe.append(ir.op_if(0)).append(ir.op_const_i32(1)).append(ir.op_return())
      .append(ir.op_end()).append(ir.op_const_i32(0)).append(ir.op_return());
  }
  let tag: i32 = 2;
  if (aligned) { tag = 0; }
  let main: ir.Op[] = [ir.op_const_i32(9), ir.op_arr_make(1, 32), ir.op_store_local(0),
    ir.op_load_local(0), ir.op_const_i32(tag), ir.op_raw_addr(), ir.op_store_local(1),
    ir.op_load_local(1), ir.op_call_direct("probe", 1)];
  main = check(main, 1, 71);
  main = main.append(ir.op_load_local(1)).append(ir.op_call_direct("__fern_rc_inc", 1)).append(ir.op_drop());
  main = main.append(ir.op_load_local(1)).append(ir.op_call_direct("probe", 1));
  main = check(main, 0, 72);
  main = main.append(ir.op_load_local(0)).append(ir.op_call_direct("__fern_rc_dec", 1)).append(ir.op_drop());
  main = main.append(ir.op_load_local(0)).append(ir.op_call_direct("__fern_rc_dec", 1)).append(ir.op_drop());
  main = main.append(ir.op_call_direct("__fern_rc_underflow_get", 0)).append(ir.op_return());
  let mod = parser.parse_module(lexer.tokenize("@noinline function probe(p: usize): i32 { return 0; } function main(): i32 { return probe(0 as usize); }"));
  let d = semlower.driven(mod, av[1]);
  let g = semlower.program(d);
  if (av[1] == "wasm32-wasi") {
    let wm = ircore.with_records(wasm_ir.route_normalized(d.full), d.sub);
    let l = ircore.gate(wm, d.sub);
    g = ircore.Gated { ok: l.ok, im: wm, stab: irtables.struct_tab(wm.structs), cache: l.cache };
  }
  if (!g.ok) { return 3; }
  let cache: irtables.LowerResult[] = [];
  let i: i32 = 0;
  for r in g.cache {
    let out = r;
    if (i < g.im.funcs.len() && g.im.funcs[i].name == "probe") {
      out = irtables.LowerResult { ...r, ops: probe, n_params: 1, n_locals: 1 };
    }
    if (i < g.im.funcs.len() && g.im.funcs[i].name == "main") {
      out = irtables.LowerResult { ...r, ops: main, n_params: 0, n_locals: 2 };
    }
    cache = cache.append(out);
    i = i + 1;
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

func TestSelfHostByteViewRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "byte_view_rc.fern"), []byte(byteViewRCDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "byte_view_rc.fern", "byte-view-rc")
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, mode := range []string{"fused", "value", "aligned-fused", "aligned-value"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				cmd := runX86_64Bin(runner, driver, target, mode)
				cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				var diagnostics bytes.Buffer
				cmd.Stderr = &diagnostics
				output, err := cmd.Output()
				if err != nil {
					t.Fatalf("emit: %v\n%s", err, diagnostics.String())
				}
				if target != "wasm32-wasi" {
					body := shapeFnBody(t, string(output), "probe")
					mask := "andq $-3"
					if target == "arm64-linux" {
						mask = "#0xfffffffffffffffd"
					}
					if got, want := strings.Contains(body, mask), !strings.HasPrefix(mode, "aligned-"); got != want {
						t.Fatalf("descriptor mask = %t, want %t:\n%s", got, want, body)
					}
				}
				run := physicalRCRun(t, gcc, runner, dir, "byte-view-"+mode, target, output)
				got, err := run.CombinedOutput()
				if err != nil {
					t.Fatalf("runtime: %v\n%s", err, got)
				}
				assertBalancedCensus(t, string(got))
			})
		}
	}
}
