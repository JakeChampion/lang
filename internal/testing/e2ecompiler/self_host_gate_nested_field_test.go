package e2ecompiler

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
  let i: i32 = 0;
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
  let t: Table = Table { memo: Memo { names: Index { keys: ["a", "b", "c"] } } };
  let at: i32 = t.memo.names.find("c");
  return at;
}
`

// The same method-on-a-field shape through a user variant's payload binding.
// The gate never bound `ix`, so `ix.names` fell to the by-name scan and typed
// as `Rows.names: string` (E003 on `at`). The binding is typed from the
// variant's declaration now, and an object the gate cannot type reads as
// unknown rather than as whichever struct spells the field first.
const gateVariantPayloadFieldSrc = `struct Rows { names: string }

struct Keys { items: string[] }

function (k: Keys) find(name: string): i32 {
  let i: i32 = 0;
  while (i < k.items.len()) {
    if (k.items[i] == name) {
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

struct Index { names: Keys }

enum Holder { Named(Index), Plain }

function lookup(h: Holder, name: string): i32 {
  match (h) {
    Holder.Named(ix) => {
      let at: i32 = ix.names.find(name);
      return at;
    },
    Holder.Plain => {
      return 0 - 2;
    }
  }
}

function main(): i32 {
  let h: Holder = Holder.Named(Index { names: Keys { items: ["a", "b", "c"] } });
  return lookup(h, "c");
}
`

func TestSelfHostGateFieldThroughVariantPayload(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "variant_payload_field", gateVariantPayloadFieldSrc); code != 2 {
		t.Errorf("ix.names.find(\"c\") through a Holder.Named payload exited %d, want 2 (the index of \"c\" in Keys.items)", code)
	}
}

func TestSelfHostGateFieldThroughStructField(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	if code := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "nested_field", gateNestedFieldSrc); code != 2 {
		t.Errorf("t.memo.names.find(\"c\") exited %d, want 2 (the index of \"c\" in Index.keys)", code)
	}
}
