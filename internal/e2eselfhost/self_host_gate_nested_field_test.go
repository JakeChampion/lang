package e2eselfhost

import "testing"

// A read through a struct-typed field, then a method on what it reads. The
// pre-codegen gate (asmcore.infer_expr_type) used to drop a struct-typed
// field to unknown and then type the next field by its NAME across every
// struct, so `t.memo.names` read as `Rows.names: string` and `.find` typed as
// the string method, refusing the program with E003 (#11066). Rows is
// declared first on purpose: the by-name scan answers with the first struct.
const gateNestedFieldSrc = `struct Rows { names: string }

struct Index { keys: string[] }

function (ix: Index) find(name: string): i32 {
  var i: i32 = 0;
  while (i < ix.keys.len()) {
    if (ix.keys[i] == name) {
      return i;
    }
    i = i + 1;
  }
  return 0 - 1;
}

function (s: string) find(needle: string): Option[i32] {
  if (s == needle) {
    return Some(0);
  }
  return None;
}

struct Memo { names: Index }

struct Table { memo: Memo }

function main(): i32 {
  var t: Table = Table { memo: Memo { names: Index { keys: ["a", "b", "c"] } } };
  var at: i32 = t.memo.names.find("c");
  return at;
}
`

func TestSelfHostGateFieldThroughStructField(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "nested_field", gateNestedFieldSrc); code != 2 {
		t.Errorf("t.memo.names.find(\"c\") exited %d, want 2 (the index of \"c\" in Index.keys)", code)
	}
}
