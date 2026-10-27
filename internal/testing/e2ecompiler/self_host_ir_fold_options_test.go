package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const irFoldOptionContract = `import "./ir";
import "./util";

function constant64(value: i64): ir.Op { return ir.op_const_i64_text(util.i64_to_string(value)); }

function marked(op: ir.Op): ir.Op { return ir.Op { ...op, source: 9000000000i64, decl: 37 }; }

function equal(a: ir.Op[], b: ir.Op[]): boolean {
  if (a.len() != b.len()) { return false; }
  for i in 0..a.len() {
    let x=a[i]; let y=b[i];
    if (x.kind_tag != y.kind_tag || x.i32_imm != y.i32_imm || x.i64_imm != y.i64_imm || f64_bits(x.f64_imm) != f64_bits(y.f64_imm)
      || x.width != y.width || x.unsigned != y.unsigned || x.str != y.str || x.source != y.source || x.decl != y.decl) { return false; }
  }
  return true;
}

function fold64(a: i64, b: i64, verb: string, unsigned: boolean): ir.Op[] {
  return ir.fold_const_binaries([marked(constant64(a)), marked(constant64(b)), marked(ir.op_bin(verb,64,unsigned))]);
}

function contract(): i32 {
  for value in [-256,-1,0,1,255,256,2147483647] {
    if (!equal(ir.fold_const_binaries([marked(ir.op_const_i32(value)),marked(ir.op_int_cast("u8"))]), [ir.op_const_i32(value & 255)])) { return 1; }
    if (!equal(ir.fold_const_binaries([marked(ir.op_const_i32(value)),marked(ir.op_int_cast("i32"))]), [ir.op_const_i32(value)])) { return 2; }
    let unsigned=[marked(ir.op_const_i32(value)),marked(ir.op_u32_wrap())];
    let want=if(value<0){unsigned}else{[ir.op_const_i32(value)]};
    if (!equal(ir.fold_const_binaries(unsigned),want)) { return 3; }
    let cast=[marked(ir.op_const_i32(value)),marked(ir.op_int_cast("u32"))];
    let want_cast=if(value<0){cast}else{[ir.op_const_i32(value)]};
    if (!equal(ir.fold_const_binaries(cast),want_cast)) { return 4; }
  }
  if (!equal(fold64(9000000000i64,7i64,"add",false),[ir.op_const_i64_text("9000000007")])) { return 5; }
  if (!equal(fold64(-9000000000i64,7i64,"mul",false),[ir.op_const_i64_text("-63000000000")])) { return 6; }
  if (!equal(fold64(-16i64,66i64,"shr_s",false),[ir.op_const_i64_text("-4")])) { return 7; }
  if (!equal(fold64(1i64,65i64,"shl",true),[ir.op_const_i64_text("2")])) { return 8; }
  if (!equal(fold64(-1i64,-1i64,"eq",true),[ir.op_const_i32(1)])) { return 9; }
  if (!equal(fold64(-1i64,0i64,"lt_s",false),[ir.op_const_i32(1)])) { return 10; }
  let minimum=-9223372036854775807i64-1i64;
  if (!equal(fold64(minimum,-1i64,"rem_s",false),[ir.op_const_i64_text("0")])) { return 11; }
  for verb in ["div_s","rem_s"] {
    let ops=[marked(constant64(7i64)),marked(constant64(0i64)),marked(ir.op_bin(verb,64,false))];
    if (!equal(ir.fold_const_binaries(ops),ops)) { return 12; }
  }
  let overflow=[marked(constant64(minimum)),marked(constant64(-1i64)),marked(ir.op_bin("div_s",64,false))];
  if (!equal(ir.fold_const_binaries(overflow),overflow)) { return 13; }
  let unsigned_div=[marked(constant64(-1i64)),marked(constant64(3i64)),marked(ir.op_bin("div_u",64,true))];
  if (!equal(ir.fold_const_binaries(unsigned_div),unsigned_div)) { return 14; }
  let unreadable=[marked(ir.op_const_i64_text("not-a-number")),marked(constant64(1i64)),marked(ir.op_bin("add",64,false))];
  if (!equal(ir.fold_const_binaries(unreadable),unreadable)) { return 15; }
  let prefix=marked(ir.op_load_local(3)); let suffix=marked(ir.op_return());
  let identity=[prefix,marked(ir.op_const_i32(1)),marked(ir.op_bin("mul",0,false)),suffix];
  let alias=identity;
  let removed=ir.reduce_strength_ex(identity);
  if (!removed.changed || !equal(removed.ops,[prefix,suffix]) || !equal(alias,[prefix,marked(ir.op_const_i32(1)),marked(ir.op_bin("mul",0,false)),suffix])) { return 16; }
  let unchanged=[prefix,marked(ir.op_const_i32(7)),marked(ir.op_bin("add",0,false)),suffix];
  let declined=ir.reduce_strength_ex(unchanged);
  if (declined.changed || !equal(declined.ops,unchanged)) { return 17; }
  let zero=ir.reduce_strength_ex([prefix,ir.op_const_i32(0),ir.op_bin("mul",0,false),suffix]);
  if (!zero.changed || !equal(zero.ops,[prefix,ir.op_drop(),ir.op_const_i32(0),suffix])) { return 18; }
  let power=ir.reduce_strength_ex([prefix,ir.op_const_i32(8),ir.op_bin("mul",0,false),suffix]);
  if (!power.changed || !equal(power.ops,[prefix,ir.op_const_i32(3),ir.op_bin("shl",0,false),suffix])) { return 19; }
  let wide=[prefix,marked(ir.op_const_i32(1)),marked(ir.op_bin("mul",64,false)),suffix];
  let retained=ir.reduce_strength_ex(wide);
  if (retained.changed || !equal(retained.ops,wide)) { return 20; }
  return 0;
}

function main(): i32 { return contract(); }
`

func TestSelfHostIRFoldOptions(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ir.fern")
	writeResolveProject(t, dir, map[string]string{"ir_fold_options.fern": irFoldOptionContract})
	t.Run("Go-oracle", func(t *testing.T) {
		oracle := buildLangBinForInterp(t)
		out, err := exec.Command(oracle, "-interp", filepath.Join(dir, "ir_fold_options.fern")).CombinedOutput()
		if err != nil || len(out) != 0 {
			t.Fatalf("IR fold contract: %v\n%s", err, out)
		}
	})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "ir_fold_options.fern", "ir_fold_options", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Fatalf("IR fold contract: %v\n%s", err, out)
			}
		})
	}
}
