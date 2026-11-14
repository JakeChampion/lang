package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
function f32_elem(): ir.Op { return ir.Op { ...ir.op_arr_get(32), unsigned: true }; }
function a(): ir.Op[] { return [ir.op_load_local(0)]; }
function b(): ir.Op[] { return [ir.op_load_local(1)]; }
function c(): ir.Op[] { return [ir.op_load_local(2)]; }
// check appends: the ops leave an f64; it must equal t or main exits n.
function check(own ops: ir.Op[], expr: ir.Op[], t: string, n: i32): ir.Op[] {
  for o in expr { ops = ops.append(o); }
  return ops.append(f(t)).append(ir.op_float("fne")).append(ir.op_if(0)).append(ir.op_const_i32(n)).append(ir.op_return()).append(ir.op_end());
}
// held_check is check with the array in local 9 released on the way out, so
// the emitted-RC certifier finds no unit left held.
function held_check(own ops: ir.Op[], expr: ir.Op[], t: string, n: i32): ir.Op[] {
  for o in expr { ops = ops.append(o); }
  ops = ops.append(f(t)).append(ir.op_float("fne")).append(ir.op_if(0));
  ops = ops.append(ir.op_load_local(9)).append(ir.op_call_direct("__fern_rc_dec", 1)).append(ir.op_drop());
  return ops.append(ir.op_const_i32(n)).append(ir.op_return()).append(ir.op_end());
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
// Locals 10 and 11 hold lanes of 8 bits: p is 200 with 7 in lane 3, and q
// is 100 with 250 in lane 3.
function vi(k: string): ir.Op { return ir.op_lane(k, 8); }
// icheck appends: the ops leave an i32; it must equal want or main exits n.
function icheck(own ops: ir.Op[], expr: ir.Op[], want: i32, n: i32): ir.Op[] {
  for o in expr { ops = ops.append(o); }
  return ops.append(ir.op_const_i32(want)).append(ir.op_bin("ne", 0, false)).append(ir.op_if(0)).append(ir.op_const_i32(n)).append(ir.op_return()).append(ir.op_end());
}
// ibin is k(x, y) with lane read as an unsigned value.
function ibin(k: string, x: i32, y: i32, lane: i32): ir.Op[] {
  return [ir.op_load_local(x), ir.op_load_local(y), vi(k), ir.op_lane_at("viextractu", 8, lane)];
}
// imask is the mask of the lanes where the compare k(x, y) held.
function imask(k: string, x: i32, y: i32): ir.Op[] {
  return [ir.op_load_local(x), ir.op_load_local(y), vi(k), vi("vibitmask")];
}
// Locals 13 to 16 hold wider lanes. For 16 bits, h is 40000 with 9 in lane
// 5 and k is 30000. For 32 bits, t is 3000000000 with 5 in lane 2 and u is
// 70000.
function wi(k: string, w: i32): ir.Op { return ir.op_lane(k, w); }
// wbin is k(x, y) at the width w, with lane read with its sign.
function wbin(k: string, w: i32, x: i32, y: i32, lane: i32): ir.Op[] {
  return [ir.op_load_local(x), ir.op_load_local(y), wi(k, w), ir.op_lane_at("viextracts", w, lane)];
}
function wmask(k: string, w: i32, x: i32, y: i32): ir.Op[] {
  return [ir.op_load_local(x), ir.op_load_local(y), wi(k, w), wi("vibitmask", w)];
}
// wshift is k(x, count) at the width w, with lane read with its sign.
function wshift(k: string, w: i32, x: i32, count: i32, lane: i32): ir.Op[] {
  return [ir.op_load_local(x), ir.op_const_i32(count), wi(k, w), ir.op_lane_at("viextracts", w, lane)];
}
function wide_checks(own m: ir.Op[]): ir.Op[] {
  m = m.append(ir.op_const_i32(40000)).append(wi("visplat", 16)).append(ir.op_const_i32(9)).append(ir.op_lane_at("vireplace", 16, 5)).append(ir.op_store_local(13));
  m = m.append(ir.op_const_i32(30000)).append(wi("visplat", 16)).append(ir.op_store_local(14));
  m = m.append(ir.op_const_i32(0 - 1294967296)).append(wi("visplat", 32)).append(ir.op_const_i32(5)).append(ir.op_lane_at("vireplace", 32, 2)).append(ir.op_store_local(15));
  m = m.append(ir.op_const_i32(70000)).append(wi("visplat", 32)).append(ir.op_store_local(16));
  // 50..54: lanes of 16 bits.
  m = icheck(m, [ir.op_load_local(13), ir.op_lane_at("viextractu", 16, 0)], 40000, 50);
  m = icheck(m, [ir.op_load_local(13), ir.op_lane_at("viextracts", 16, 7)], 0 - 25536, 50);
  m = icheck(m, [ir.op_load_local(13), ir.op_lane_at("viextractu", 16, 5)], 9, 50);
  m = icheck(m, wbin("viadd", 16, 13, 14, 0), 4464, 51);
  m = icheck(m, wbin("visub", 16, 14, 13, 5), 29991, 51);
  m = icheck(m, wbin("vimul", 16, 13, 14, 5), 7856, 51);
  m = icheck(m, wbin("viaddu", 16, 13, 14, 0), 0 - 1, 51);
  m = icheck(m, wbin("visubu", 16, 13, 14, 5), 0, 51);
  m = icheck(m, wbin("viadds", 16, 14, 14, 0), 32767, 51);
  m = icheck(m, wbin("visubs", 16, 13, 14, 0), 0 - 32768, 51);
  m = icheck(m, wbin("viminu", 16, 13, 14, 0), 30000, 52);
  m = icheck(m, wbin("vimaxu", 16, 13, 14, 0), 0 - 25536, 52);
  m = icheck(m, wbin("vimins", 16, 13, 14, 0), 0 - 25536, 52);
  m = icheck(m, wbin("vimaxs", 16, 13, 14, 0), 30000, 52);
  m = icheck(m, [ir.op_load_local(13), ir.op_load_local(14), wi("viavg", 16), ir.op_lane_at("viextractu", 16, 0)], 35000, 52);
  m = icheck(m, [ir.op_load_local(13), wi("viabs", 16), ir.op_lane_at("viextracts", 16, 0)], 25536, 52);
  m = icheck(m, [ir.op_load_local(14), wi("vineg", 16), ir.op_lane_at("viextracts", 16, 0)], 0 - 30000, 52);
  m = icheck(m, [ir.op_load_local(13), ir.op_const_i32(9), wi("visplat", 16), wi("vieq", 16), wi("vibitmask", 16)], 32, 53);
  m = icheck(m, [ir.op_load_local(13), ir.op_const_i32(9), wi("visplat", 16), wi("vine", 16), wi("vibitmask", 16)], 223, 53);
  m = icheck(m, wmask("viltu", 16, 13, 14), 32, 53);
  m = icheck(m, wmask("vilts", 16, 13, 14), 255, 53);
  m = icheck(m, wmask("vilts", 16, 14, 13), 0, 53);
  m = icheck(m, wshift("vishl", 16, 13, 4, 5), 144, 54);
  m = icheck(m, wshift("vishru", 16, 13, 4, 0), 2500, 54);
  m = icheck(m, wshift("vishrs", 16, 13, 4, 0), 0 - 1596, 54);
  // 55..59: lanes of 32 bits.
  m = icheck(m, [ir.op_load_local(15), ir.op_lane_at("viextracts", 32, 0)], 0 - 1294967296, 55);
  m = icheck(m, [ir.op_load_local(15), ir.op_lane_at("viextractu", 32, 2)], 5, 55);
  m = icheck(m, wbin("viadd", 32, 15, 16, 2), 70005, 56);
  m = icheck(m, wbin("visub", 32, 16, 15, 2), 69995, 56);
  m = icheck(m, wbin("vimul", 32, 15, 16, 2), 350000, 56);
  m = icheck(m, wbin("viminu", 32, 15, 16, 0), 70000, 57);
  m = icheck(m, wbin("vimaxu", 32, 15, 16, 0), 0 - 1294967296, 57);
  m = icheck(m, wbin("vimins", 32, 15, 16, 0), 0 - 1294967296, 57);
  m = icheck(m, wbin("vimaxs", 32, 15, 16, 0), 70000, 57);
  m = icheck(m, [ir.op_load_local(15), wi("viabs", 32), ir.op_lane_at("viextracts", 32, 0)], 1294967296, 57);
  m = icheck(m, [ir.op_load_local(16), wi("vineg", 32), ir.op_lane_at("viextracts", 32, 3)], 0 - 70000, 57);
  m = icheck(m, [ir.op_load_local(15), ir.op_const_i32(5), wi("visplat", 32), wi("vieq", 32), wi("vibitmask", 32)], 4, 58);
  m = icheck(m, [ir.op_load_local(15), ir.op_const_i32(5), wi("visplat", 32), wi("vine", 32), wi("vibitmask", 32)], 11, 58);
  m = icheck(m, wmask("viltu", 32, 15, 16), 4, 58);
  m = icheck(m, wmask("vilts", 32, 15, 16), 15, 58);
  m = icheck(m, wshift("vishl", 32, 15, 4, 2), 80, 59);
  m = icheck(m, wshift("vishru", 32, 15, 4, 0), 187500000, 59);
  m = icheck(m, wshift("vishl", 16, 13, 20, 5), 144, 59);
  m = icheck(m, wshift("vishru", 32, 15, 36, 0), 187500000, 59);
  return icheck(m, wshift("vishrs", 32, 15, 4, 0), 0 - 80935456, 59);
}
function int_checks(own m: ir.Op[]): ir.Op[] {
  m = m.append(ir.op_const_i32(200)).append(vi("visplat")).append(ir.op_const_i32(7)).append(ir.op_lane_at("vireplace", 8, 3)).append(ir.op_store_local(10));
  m = m.append(ir.op_const_i32(100)).append(vi("visplat")).append(ir.op_const_i32(250)).append(ir.op_lane_at("vireplace", 8, 3)).append(ir.op_store_local(11));
  // 40: the lane reads of each sign, and a write to the last lanes.
  m = icheck(m, [ir.op_load_local(10), ir.op_lane_at("viextractu", 8, 15)], 200, 40);
  m = icheck(m, [ir.op_load_local(10), ir.op_lane_at("viextracts", 8, 0)], 0 - 56, 40);
  m = icheck(m, [ir.op_load_local(10), ir.op_lane_at("viextractu", 8, 3)], 7, 40);
  m = icheck(m, [ir.op_load_local(10), ir.op_const_i32(77), ir.op_lane_at("vireplace", 8, 12), ir.op_lane_at("viextractu", 8, 12)], 77, 40);
  m = icheck(m, [ir.op_load_local(10), ir.op_const_i32(77), ir.op_lane_at("vireplace", 8, 12), ir.op_lane_at("viextractu", 8, 11)], 200, 40);
  // 41..42: the arithmetic that wraps, and the arithmetic that saturates.
  m = icheck(m, ibin("viadd", 10, 11, 15), 44, 41);
  m = icheck(m, ibin("viadd", 10, 11, 3), 1, 41);
  m = icheck(m, ibin("visub", 10, 11, 3), 13, 41);
  m = icheck(m, ibin("visub", 10, 11, 0), 100, 41);
  m = icheck(m, ibin("viaddu", 10, 11, 0), 255, 42);
  m = icheck(m, ibin("viaddu", 10, 11, 3), 255, 42);
  m = icheck(m, ibin("visubu", 10, 11, 3), 0, 42);
  m = icheck(m, ibin("visubu", 10, 11, 0), 100, 42);
  m = icheck(m, ibin("viadds", 10, 10, 0), 144, 42);
  m = icheck(m, ibin("viadds", 11, 11, 0), 127, 42);
  m = icheck(m, ibin("visubs", 11, 10, 0), 127, 42);
  m = icheck(m, ibin("visubs", 10, 11, 0), 128, 42);
  // 43: the minimum and the maximum of each sign, and the average.
  m = icheck(m, ibin("vimins", 10, 11, 0), 200, 43);
  m = icheck(m, ibin("vimins", 10, 11, 3), 250, 43);
  m = icheck(m, ibin("viminu", 10, 11, 0), 100, 43);
  m = icheck(m, ibin("viminu", 10, 11, 3), 7, 43);
  m = icheck(m, ibin("vimaxs", 10, 11, 0), 100, 43);
  m = icheck(m, ibin("vimaxs", 10, 11, 3), 7, 43);
  m = icheck(m, ibin("vimaxu", 10, 11, 0), 200, 43);
  m = icheck(m, ibin("vimaxu", 10, 11, 3), 250, 43);
  m = icheck(m, ibin("viavg", 10, 11, 0), 150, 43);
  m = icheck(m, ibin("viavg", 10, 11, 3), 129, 43);
  // 44: the compares, as the mask of the lanes where each held.
  m = icheck(m, imask("vieq", 10, 10), 65535, 44);
  m = icheck(m, imask("vieq", 10, 11), 0, 44);
  m = icheck(m, [ir.op_load_local(10), ir.op_const_i32(200), vi("visplat"), vi("vieq"), vi("vibitmask")], 65527, 44);
  m = icheck(m, [ir.op_load_local(10), ir.op_const_i32(200), vi("visplat"), vi("vine"), vi("vibitmask")], 8, 44);
  m = icheck(m, imask("vilts", 10, 11), 65527, 44);
  m = icheck(m, imask("vilts", 11, 10), 8, 44);
  m = icheck(m, imask("viltu", 10, 11), 8, 44);
  m = icheck(m, imask("viltu", 11, 10), 65527, 44);
  m = icheck(m, imask("viltu", 10, 10), 0, 44);
  // 45: the unary ops.
  m = icheck(m, [ir.op_load_local(11), vi("vineg"), ir.op_lane_at("viextracts", 8, 0)], 0 - 100, 45);
  m = icheck(m, [ir.op_load_local(10), vi("viabs"), ir.op_lane_at("viextracts", 8, 0)], 56, 45);
  m = icheck(m, [ir.op_load_local(10), vi("viabs"), ir.op_lane_at("viextractu", 8, 3)], 7, 45);
  // 47: the shifts of lanes of 8 bits, and a count modulo the lane width.
  m = icheck(m, wshift("vishl", 8, 10, 3, 0), 64, 47);
  m = icheck(m, wshift("vishl", 8, 10, 3, 3), 56, 47);
  m = icheck(m, wshift("vishru", 8, 10, 3, 0), 25, 47);
  m = icheck(m, wshift("vishrs", 8, 10, 3, 0), 0 - 7, 47);
  m = icheck(m, wshift("vishrs", 8, 10, 3, 3), 0, 47);
  m = icheck(m, wshift("vishrs", 8, 10, 0, 0), 0 - 56, 47);
  m = icheck(m, wshift("vishrs", 8, 10, 7, 0), 0 - 1, 47);
  m = icheck(m, wshift("vishrs", 8, 10, 11, 0), 0 - 7, 47);
  // 46: p + q held across a call, so it spills to a slot pair.
  m = m.append(ir.op_load_local(10)).append(ir.op_load_local(11)).append(vi("viadd")).append(ir.op_store_local(12));
  m = m.append(ir.op_call_direct("side", 0)).append(ir.op_drop());
  m = icheck(m, [ir.op_load_local(12), ir.op_lane_at("viextractu", 8, 3)], 1, 46);
  m = icheck(m, [ir.op_load_local(12), ir.op_load_local(10), vi("visubu"), ir.op_lane_at("viextractu", 8, 15)], 0, 46);
  return icheck(m, [ir.op_load_local(12), vi("vibitmask")], 0, 46);
}
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
  // 35: vget and vset on xs = [1.0, 2.0, 3.0, 4.0, 5.0] in local 9, first a
  // group of f64 elements, then of f32 elements that hold their 4-byte bits.
  m = m.append(f("1.0")).append(f("2.0")).append(f("3.0")).append(f("4.0")).append(f("5.0")).append(ir.op_arr_make(5, 64)).append(ir.op_store_local(9));
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(3), v("vget"), ir.op_lane_at("vextract", 64, 1)], "5.0", 35);
  m = m.append(ir.op_load_local(9)).append(ir.op_const_i32(1)).append(ir.op_load_local(0)).append(v("vset"));
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(2), ir.op_arr_get(64)], "-2.5", 35);
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(0), ir.op_arr_get(64)], "1.0", 35);
  m = m.append(ir.op_load_local(9)).append(ir.op_call_direct("__fern_rc_dec", 1)).append(ir.op_drop());
  for bits in [1065353216, 1073741824, 1077936128, 1082130432, 1084227584] {
    m = m.append(ir.op_const_i32(bits));
  }
  m = m.append(ir.op_arr_make(5, 32)).append(ir.op_store_local(9));
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(1), v32("vget"), ir.op_lane_at("vextract", 32, 1)], "3.0", 35);
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(1), v32("vget"), ir.op_lane_at("vextract", 32, 3)], "5.0", 35);
  m = m.append(ir.op_load_local(9)).append(ir.op_const_i32(1)).append(ir.op_load_local(2)).append(v32("vset"));
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(4), f32_elem(), ir.op_f32_from_bits()], "-4.0", 35);
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(2), f32_elem(), ir.op_f32_from_bits()], "1.5", 35);
  m = held_check(m, [ir.op_load_local(9), ir.op_const_i32(0), f32_elem(), ir.op_f32_from_bits()], "1.0", 35);
  m = m.append(ir.op_load_local(9)).append(ir.op_call_direct("__fern_rc_dec", 1)).append(ir.op_drop());
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
  m = int_checks(m);
  m = wide_checks(m);
  m = m.append(ir.op_const_i32(0)).append(ir.op_return());
  let kern: ir.Op[] = [ir.op_load_local(0), ir.op_load_local(0), v("vload"), ir.op_load_local(0), v("vload"), v("vadd"), v("vstore"),
    ir.op_const_i32(0), ir.op_return()];
  let mod = parser.parse_module(lexer.tokenize("@noinline function side(): i32 { return 7; } @noinline function kern(p: usize): i32 { return 0; } function main(): i32 { return side() + kern(0 as usize); }"));
  let d = semlower.driven(mod, av[1]);
  let g = if (av[1] == "wasm32-wasi") {
    let wm = ircore.with_records(wasm_ir.route_normalized(d.full), d.sub);
    let l = match (ircore.gate(wm, d.sub)) { Some(g) => g, None => { return 3; } };
    ircore.Gated { im: wm, stab: irtables.struct_tab(wm.structs), cache: l.cache, physical: l.physical }
  } else { semlower.program(d) };
  let cache: irtables.LowerResult[] = [];
  let i: i32 = 0;
  for r in g.cache {
    let out = r;
    if (i < g.im.funcs.len() && g.im.funcs[i].name == "main") {
      out = irtables.LowerResult { ...r, ops: m, n_params: 0, n_locals: 17, v128_slots: [0, 1, 2, 3, 5, 6, 10, 11, 12, 13, 14, 15, 16], f64_slots: [7, 8] };
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
  } else { print(wasm_ir.emit_ir_module_mode(g.im, cache, 0, asmcore.env_switches(), g.physical, d.sub.rt_lower)); }
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
				for _, want := range []string{"vfmadd213pd", "cmppd $1,", "roundpd $1,", "movupd", "vpaddusb", "vpminsb", "pmovmskb", "pinsrb $12,", "vpmullw", "vpmulld", "vpsraw", "vpsrld", "packsswb", "pinsrw $5,"} {
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

func laneMainText(asm string) string {
	i := strings.Index(asm, "\n__fn_main")
	if i < 0 {
		return asm
	}
	rest := asm[i+1:]
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
