package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// lentOwnerCallSrc hands a call a value it owns at its last use together with
// a borrow of that value: a match payload (`columns(done, mt, recv)`, the
// checker's `literal_insert_columns` shape) and a field (`relabel(done,
// b.inner, b)`). Each callee consumes the owner, since one path returns it, and
// on the other releases it before reading the borrow. The typed lowering moved
// the owner's unit into the call, so the callee read freed memory (#10832).
const lentOwnerCallSrc = `struct TU { reason: string }
struct TI { w: i32 }
struct TM { key: Ty, value: Ty }
type Ty = TU | TI | TM;
struct Boxed { inner: Ty, tag: string }

function is_unknown(t: Ty): boolean {
  match (t) {
    TU(_) => { return true; },
    _ => { return false; }
  }
}

function width(t: Ty): i32 {
  match (t) {
    TI(i) => { return i.w; },
    TM(m) => { return width(m.key) * 100 + width(m.value); },
    TU(u) => { return u.reason.len(); }
  }
}

function make(k: i32, v: i32): Ty {
  if (k < 0) { return TM { key: TU { reason: "key" }, value: TI { w: v } }; }
  return TM { key: TI { w: k }, value: TI { w: v } };
}

function columns(done: boolean, mt: TM, recv: Ty): Ty {
  if (done) { return recv; }
  var ik: Ty = mt.key;
  var iv: Ty = mt.value;
  if (is_unknown(ik)) { ik = TI { w: 8 }; }
  if (is_unknown(iv)) { iv = TI { w: 16 }; }
  return TM { key: ik, value: iv };
}

function typed(done: boolean, k: i32, v: i32): Ty {
  var recv: Ty = make(k, v);
  match (recv) {
    TM(mt) => { return columns(done, mt, recv); },
    _ => {}
  }
  return TU { reason: "not a map" };
}

function relabel(done: boolean, inner: Ty, b: Boxed): Boxed {
  if (done) { return b; }
  var fresh: Ty = TM { key: TI { w: 1 }, value: TU { reason: "zz" } };
  return Boxed { inner: TI { w: width(inner) + width(fresh) }, tag: "relabelled" };
}

function boxed(done: boolean, w: i32): Boxed {
  var b: Boxed = Boxed { inner: TM { key: TI { w: w }, value: TU { reason: "abc" } }, tag: "fresh" };
  return relabel(done, b.inner, b);
}

function main(): i32 {
  var a: Ty = typed(false, 0 - 1, 32);
  var churn: Ty[] = [TU { reason: "abcdefgh" }, TU { reason: "ijklmnop" }, TI { w: 1 }];
  if (churn.len() != 3) { return 9; }
  if (width(a) != 832) { return 1; }
  if (width(typed(true, 4, 5)) != 405) { return 2; }
  var r: Boxed = boxed(false, 7);
  var churn2: Boxed[] = [Boxed { inner: TI { w: 2 }, tag: "x" }];
  if (churn2.len() != 1) { return 9; }
  if (width(r.inner) != 805 || r.tag.len() != 10) { return 3; }
  if (boxed(true, 7).tag != "fresh") { return 4; }
  return 42;
}
`

func TestSelfHostLentOwnerCallIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if got := interpExit(t, buildLangBinForInterp(t), lentOwnerCallSrc); got != 42 {
		t.Fatalf("interpreter = %d, want 42", got)
	}
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", lentOwnerCallSrc)); code != 42 {
		t.Fatalf("exit %d, want 42 (the code names the failing step)", code)
	}
	// Nothing is recycled under the quarantine, so a touch of a released
	// block exits 124 instead of reading whatever reused it.
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", lentOwnerCallSrc, "FERN_RC_FREE_DEBUG=1")); code != 42 {
		t.Fatalf("quarantine run: exit %d, want 42 (124 is a use after free)", code)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", lentOwnerCallSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 42 {
		t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostLentOwnerCallIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", lentOwnerCallSrc)); code != 42 {
		t.Fatalf("arm64: exit %d, want 42 (the code names the failing step)", code)
	}
}

func TestSelfHostLentOwnerCallWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", lentOwnerCallSrc)); code != 42 {
		t.Fatalf("wasm: exit %d, want 42 (the code names the failing step)", code)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", lentOwnerCallSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 42 {
		t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
