package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The lane ops (ir.lane_kind_names, docs/SIMD-GENERIC-KERNELS.md §6 slice
// 4b) have no source construct yet, so the program is built from the IR
// constructors and substituted into the lowered cache, as
// self_host_byte_view_rc_test.go does. `main` computes with every op on
// f64x2 and f32x4 values held in locals, reads lanes back with vextract and
// exits with the number of the first check that is not the spec's answer, 0
// when all hold. Check 33 spills lane values across a call, and check 34
// calls a lane function with no f64 value in it. Each target's emitter, allocator and encoder are what the
// exit code proves: a wrong register class, slot size or instruction gives a
// wrong lane, not a diagnostic.
const laneIRDriver = `import "./ir";
import "./irtables";
import "./ircore";
import "./lexer";
import "./parser";
import "./semlower";
import "./asmcore";
import "./asm_ir";
import "./asm_arm64_ir";
import "./wasm_ir";

// Locals 0..3 hold lane values: a = [3.0, -2.5], b = [1.5, 4.0], the f32x4
// c = [1.5, 1.5, 1.5, -4.0], and a scratch.
function v(k: string): ir.Op { return ir.op_lane(k, 64); }
function v32(k: string): ir.Op { return ir.op_lane(k, 32); }
function f(t: string): ir.Op { return ir.op_const_f64_text(t); }
function a(): ir.Op[] { return [ir.op_load_local(0)]; }
function b(): ir.Op[] { return [ir.op_load_local(1)]; }
function c(): ir.Op[] { return [ir.op_load_local(2)]; }
// check appends: the ops leave an f64; it must equal t or main exits n.
function check(own ops: ir.Op[], expr: ir.Op[], t: string, n: i32): ir.Op[] {
  for o in expr { ops = ops.append(o); }
  return ops.append(f(t)).append(ir.op_fbin("fne")).append(ir.op_if(0)).append(ir.op_const_i32(n)).append(ir.op_return()).append(ir.op_end());
}
function bin(k: string, x: ir.Op[], y: ir.Op[], lane: i32): ir.Op[] {
  let out: ir.Op[] = [];
  for o in x { out = out.append(o); }
  for o in y { out = out.append(o); }
  return out.append(v(k)).append(ir.op_lane_at("vextract", 64, lane));
}
function un(k: string, x: ir.Op[], lane: i32): ir.Op[] {
  let out: ir.Op[] = [];
  for o in x { out = out.append(o); }
  return out.append(v(k)).append(ir.op_lane_at("vextract", 64, lane));
}
// sel is vselect(a, b, cmp(a, b)) read at lane: a where the compare held.
function sel(cmp: string, lane: i32): ir.Op[] {
  let out: ir.Op[] = [ir.op_load_local(0), ir.op_load_local(1), ir.op_load_local(0), ir.op_load_local(1), v(cmp)];
  return out.append(v("vselect")).append(ir.op_lane_at("vextract", 64, lane));
}
// logic is vselect(a, b, OP(lt(a, b), gt(a, b))) at lane.
function logic(op: string, lane: i32): ir.Op[] {
  let out: ir.Op[] = [ir.op_load_local(0), ir.op_load_local(1),
    ir.op_load_local(0), ir.op_load_local(1), v("vlt"), ir.op_load_local(0), ir.op_load_local(1), v("vgt"), v(op)];
  return out.append(v("vselect")).append(ir.op_lane_at("vextract", 64, lane));
}
function bin32(k: string, y: ir.Op[], lane: i32): ir.Op[] {
  let out: ir.Op[] = [ir.op_load_local(2)];
  for o in y { out = out.append(o); }
  return out.append(v32(k)).append(ir.op_lane_at("vextract", 32, lane));
}
function un32(k: string, lane: i32): ir.Op[] {
  return [ir.op_load_local(2), v32(k), ir.op_lane_at("vextract", 32, lane)];
}
function splat32(t: string): ir.Op[] { return [f(t), v32("vsplat")]; }
function main(): i32 {
  let av = args();
  let m: ir.Op[] = [f("3.0"), v("vsplat"), f("-2.5"), ir.op_lane_at("vreplace", 64, 1), ir.op_store_local(0),
    f("1.5"), v("vsplat"), f("4.0"), ir.op_lane_at("vreplace", 64, 1), ir.op_store_local(1),
    f("1.5"), v32("vsplat"), f("-4.0"), ir.op_lane_at("vreplace", 32, 3), ir.op_store_local(2)];
  // 1..6: the f64x2 arithmetic on both lanes.
  m = check(m, bin("vadd", a(), b(), 0), "4.5", 1);
  m = check(m, bin("vadd", a(), b(), 1), "1.5", 1);
  m = check(m, bin("vsub", a(), b(), 1), "-6.5", 2);
  m = check(m, bin("vmul", a(), b(), 1), "-10.0", 3);
  m = check(m, bin("vdiv", a(), b(), 0), "2.0", 4);
  m = check(m, bin("vmin", a(), b(), 0), "1.5", 5);
  m = check(m, bin("vmin", a(), b(), 1), "-2.5", 5);
  m = check(m, bin("vmax", a(), b(), 0), "3.0", 6);
  m = check(m, bin("vmax", a(), b(), 1), "4.0", 6);
  // 7..13: the unaries; round is half away from zero (-2.5 -> -3).
  m = check(m, un("vabs", a(), 1), "2.5", 7);
  m = check(m, un("vneg", a(), 0), "-3.0", 8);
  m = check(m, un("vsqrt", b(), 1), "2.0", 9);
  m = check(m, un("vfloor", a(), 1), "-3.0", 10);
  m = check(m, un("vceil", b(), 0), "2.0", 11);
  m = check(m, un("vtrunc", a(), 1), "-2.0", 12);
  m = check(m, un("vround", a(), 1), "-3.0", 13);
  m = check(m, un("vround", b(), 0), "2.0", 13);
  m = check(m, un("vround", a(), 0), "3.0", 13);
  // 14: fma, a * b + a = [7.5, -12.5].
  m = check(m, [ir.op_load_local(0), ir.op_load_local(1), ir.op_load_local(0), v("vfma"), ir.op_lane_at("vextract", 64, 1)], "-12.5", 14);
  m = check(m, [ir.op_load_local(0), ir.op_load_local(1), ir.op_load_local(0), v("vfma"), ir.op_lane_at("vextract", 64, 0)], "7.5", 14);
  // 15..20: the compares through select: a where it held, else b.
  m = check(m, sel("vlt", 1), "-2.5", 15);
  m = check(m, sel("vlt", 0), "1.5", 15);
  m = check(m, sel("vgt", 0), "3.0", 16);
  m = check(m, sel("vgt", 1), "4.0", 16);
  m = check(m, sel("vle", 1), "-2.5", 17);
  m = check(m, sel("vle", 0), "1.5", 17);
  m = check(m, sel("vge", 0), "3.0", 18);
  m = check(m, sel("vge", 1), "4.0", 18);
  m = check(m, sel("veq", 0), "1.5", 19);
  m = check(m, sel("vne", 0), "3.0", 20);
  m = check(m, sel("vne", 1), "-2.5", 20);
  // 21..24: the bitwise ops on the two masks lt (lane 1) and gt (lane 0).
  m = check(m, logic("vand", 0), "1.5", 21);
  m = check(m, logic("vand", 1), "4.0", 21);
  m = check(m, logic("vor", 0), "3.0", 22);
  m = check(m, logic("vor", 1), "-2.5", 22);
  m = check(m, logic("vxor", 1), "-2.5", 23);
  m = check(m, logic("vandnot", 1), "-2.5", 24);
  m = check(m, logic("vandnot", 0), "1.5", 24);
  // 25: store a into a fresh four-word box, 16 bytes in, and load it back.
  m = m.append(ir.op_const_i32(4)).append(ir.op_alloc()).append(ir.op_const_i32(16)).append(ir.op_raw_addr()).append(ir.op_store_local(4));
  m = m.append(ir.op_load_local(4)).append(ir.op_load_local(0)).append(v("vstore"));
  m = check(m, [ir.op_load_local(4), v("vload"), ir.op_lane_at("vextract", 64, 1)], "-2.5", 25);
  m = check(m, [ir.op_load_local(4), v("vload"), ir.op_lane_at("vextract", 64, 0)], "3.0", 25);
  // 34: kern doubles the stored lanes with vload, vadd and vstore alone, a
  // body with no f64 value to seed the float class.
  m = m.append(ir.op_load_local(4)).append(ir.op_call_direct("kern", 1)).append(ir.op_drop());
  m = check(m, [ir.op_load_local(4), v("vload"), ir.op_lane_at("vextract", 64, 0)], "6.0", 34);
  m = check(m, [ir.op_load_local(4), v("vload"), ir.op_lane_at("vextract", 64, 1)], "-5.0", 34);
  // 33: a + b and a * b held across a call, so both spill to a slot pair;
  // then two f64s read out of them held across another call, so they spill
  // where the pairs were.
  m = m.append(ir.op_load_local(0)).append(ir.op_load_local(1)).append(v("vadd")).append(ir.op_store_local(5));
  m = m.append(ir.op_load_local(0)).append(ir.op_load_local(1)).append(v("vmul")).append(ir.op_store_local(6));
  m = m.append(ir.op_call_direct("side", 0)).append(ir.op_drop());
  m = check(m, [ir.op_load_local(5), ir.op_lane_at("vextract", 64, 1)], "1.5", 33);
  m = check(m, [ir.op_load_local(6), ir.op_lane_at("vextract", 64, 0)], "4.5", 33);
  m = m.append(ir.op_load_local(5)).append(ir.op_lane_at("vextract", 64, 0)).append(ir.op_store_local(7));
  m = m.append(ir.op_load_local(6)).append(ir.op_lane_at("vextract", 64, 1)).append(ir.op_store_local(8));
  m = m.append(ir.op_call_direct("side", 0)).append(ir.op_drop());
  m = check(m, [ir.op_load_local(7)], "4.5", 33);
  m = check(m, [ir.op_load_local(8)], "-10.0", 33);
  // 26..31: f32x4 on c = [1.5, 1.5, 1.5, -4.0].
  m = check(m, bin32("vadd", c(), 3), "-8.0", 26);
  m = check(m, bin32("vmul", c(), 0), "2.25", 26);
  m = check(m, bin32("vdiv", splat32("2.0"), 3), "-2.0", 27);
  m = check(m, bin32("vmin", splat32("0.0"), 3), "-4.0", 28);
  m = check(m, bin32("vmax", splat32("0.0"), 3), "0.0", 28);
  m = check(m, un32("vabs", 3), "4.0", 29);
  m = check(m, un32("vneg", 0), "-1.5", 29);
  m = check(m, un32("vfloor", 0), "1.0", 30);
  m = check(m, un32("vround", 0), "2.0", 30);
  m = check(m, [ir.op_load_local(2), ir.op_load_local(2), ir.op_load_local(2), v32("vfma"), ir.op_lane_at("vextract", 32, 3)], "12.0", 31);
  m = check(m, [ir.op_load_local(2), ir.op_load_local(2), ir.op_load_local(2), ir.op_load_local(2), v32("vlt"), v32("vselect"), ir.op_lane_at("vextract", 32, 3)], "-4.0", 31);
  // 32: a lane value held across a loop (a phi on every native), summed.
  m = m.append(ir.op_const_i32(0)).append(ir.op_store_local(4));
  m = m.append(ir.op_load_local(1)).append(ir.op_store_local(3));
  m = m.append(ir.op_loop(0));
  m = m.append(ir.op_load_local(3)).append(ir.op_load_local(0)).append(v("vadd")).append(ir.op_store_local(3));
  m = m.append(ir.op_load_local(4)).append(ir.op_const_i32(1)).append(ir.op_bin("add", 0, false)).append(ir.op_store_local(4));
  m = m.append(ir.op_load_local(4)).append(ir.op_const_i32(3)).append(ir.op_bin("lt_s", 0, false)).append(ir.op_brif(0)).append(ir.op_end());
  m = check(m, [ir.op_load_local(3), ir.op_lane_at("vextract", 64, 0)], "10.5", 32);
  m = check(m, [ir.op_load_local(3), ir.op_lane_at("vextract", 64, 1)], "-3.5", 32);
  m = m.append(ir.op_const_i32(0)).append(ir.op_return());
  let kern: ir.Op[] = [ir.op_load_local(0), ir.op_load_local(0), v("vload"), ir.op_load_local(0), v("vload"), v("vadd"), v("vstore"),
    ir.op_const_i32(0), ir.op_return()];
  let mod = parser.parse_module(lexer.tokenize("@noinline function side(): i32 { return 7; } @noinline function kern(p: usize): i32 { return 0; } function main(): i32 { return side() + kern(0 as usize); }"));
  let d = semlower.driven(mod, av[1]);
  let g = semlower.program(d);
  if (av[1] == "wasm32-wasi") {
    let wm = ircore.with_records(wasm_ir.route_normalized(d.full), d.sub);
    let l = ircore.gate(wm, d.sub);
    g = ircore.Gated { ok: l.ok, im: wm, stab: irtables.struct_tab(wm.structs), cache: l.cache, physical: l.physical };
  }
  if (!g.ok) { return 3; }
  let cache: irtables.LowerResult[] = [];
  let i: i32 = 0;
  for r in g.cache {
    let out = r;
    if (i < g.im.funcs.len() && g.im.funcs[i].name == "main") {
      out = irtables.LowerResult { ...r, ops: m, n_params: 0, n_locals: 9, v128_slots: [0, 1, 2, 3, 5, 6], f64_slots: [7, 8] };
    }
    if (i < g.im.funcs.len() && g.im.funcs[i].name == "kern") {
      out = irtables.LowerResult { ...r, ops: kern, n_params: 1, n_locals: 1 };
    }
    cache = cache.append(out);
    i = i + 1;
  }
  if (av[1] == "x86-64-linux") {
    print(asm_ir.emit_module_ir_unit_flat(g.im, true, false, "", [], g.im.funcs, g.stab, 0, 0 - 1, cache, g.physical, d.sub.rt_lower, 0 as usize, asmcore.env_switches()));
  } else if (av[1] == "arm64-linux") {
    let state = asmcore.new_state();
    state = asmcore.EmitState { ...state, struct_decls: g.stab, funcs: g.im.funcs, rt_lower: d.sub.rt_lower };
    state = asm_arm64_ir.emit_body(g.im, state, false, cache, g.physical);
    state = asm_arm64_ir.emit_ir_runtime(state, false);
    print(state.take_text());
  } else { print(wasm_ir.emit_ir_module_mode(g.im, cache, 0, asmcore.env_switches(), g.physical)); }
  return 0;
}
`

func TestSelfHostLaneIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "lane_ir.fern"), []byte(laneIRDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "lane_ir.fern", "lane-ir")
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			cmd := runX86_64Bin(runner, driver, target)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			output, err := cmd.Output()
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					t.Fatalf("emit: %v\n%s", err, ee.Stderr)
				}
				t.Fatalf("emit: %v", err)
			}
			if target == "x86-64-linux" {
				for _, want := range []string{"vfmadd213pd", "cmppd $1,", "roundpd $3,", "movupd"} {
					if !strings.Contains(string(output), want) {
						t.Errorf("x86-64 text lacks %q", want)
					}
				}
			}
			run := physicalRCRun(t, gcc, runner, dir, "lane-ir", target, output)
			got, err := run.CombinedOutput()
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) && ee.ExitCode() > 0 {
					t.Fatalf("check %d failed on %s\n%s\n%s", ee.ExitCode(), target, got, laneMainText(string(output)))
				}
				t.Fatalf("run: %v\n%s", err, got)
			}
		})
	}
}

// laneMainText is the emitted text of main, from its label to the next
// function's, for a failure report.
func laneMainText(asm string) string {
	i := strings.Index(asm, "\n__fn_main")
	if i < 0 {
		return asm
	}
	rest := asm[i+1:]
	// main's own entries (__fn_main.r, __fn_main.k) stay; the next other
	// function's label ends the text.
	at := 8
	for {
		j := strings.Index(rest[at:], "\n__fn_")
		if j < 0 {
			return rest
		}
		if !strings.HasPrefix(rest[at+j+1:], "__fn_main") {
			return rest[:at+j]
		}
		at = at + j + 1
	}
}
