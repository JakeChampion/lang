package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// Generic enum with a generic-STRUCT (or generic-ENUM) variant payload —
// `enum E[U] { A(Box[U]) }` (#3693). The native monomorphizer used to leave
// the enum generic while cloning + dropping the generic struct, so the
// post-monomorphization re-check crashed ("variant A payload type Box__i32,
// expected Box[U]" / "unknown struct type Box"). The fix clones generic enums
// with composite payloads per instantiation (E__i32 with payload Box__i32) and
// re-qualifies their variant references, so every backend returns the value.
const genEnumStructPayloadSingle = `
struct Box[T] { v: T }
enum E[U] { A(Box[U]), B }
function main(): i32 {
  var e: E[i32] = A(Box { v: 6 });
  match (e) { A(b) => { return b.v; }, B => { return 0; } }
}
`

// Same enum constructed at TWO different instantiations in one program — the
// clones E__i32 and E__string share variant names, so the construction relies
// on the destination type to disambiguate. Returns 6 + 2 = 8.
const genEnumStructPayloadMulti = `
struct Box[T] { v: T }
enum E[U] { A(Box[U]), B }
function geti(): i32 { var e: E[i32] = A(Box { v: 6 }); match (e) { A(b) => { return b.v; }, B => { return 0; } } }
function gets(): i32 { var e: E[string] = A(Box { v: "hi" }); match (e) { A(b) => { return b.v.len(); }, B => { return 0; } } }
function main(): i32 { return geti() + gets(); }
`

// Generic enum whose payload is another generic ENUM — `A(Opt[U])`. Returns 7.
const genEnumEnumPayload = `
enum Opt[U] { Sm(U), Nn }
enum E[U] { A(Opt[U]), B }
function main(): i32 {
  var e: E[i32] = A(Sm(7));
  match (e) { A(o) => { match (o) { Sm(n) => { return n; }, Nn => { return 1; } } }, B => { return 0; } }
}
`

// Generic enum with a FUNCTION-typed, SELF-referential variant payload —
// `Wait(i32, (i32) => Step[T])` — used in function signatures and
// instantiated. This is std/wasm_reactor's `Step[T]` minimized. It
// re-checks fine while the enum stays generic (lenient unify), but the
// composite-payload cloning (#3693/#3733) over-eagerly cloned it via the
// function boundary and produced a broken clone (`Step[i32]` slots in
// signatures and the `(i32) => Step[i32]` payload weren't rewritten to
// `Step__i32`), crashing the re-check. The fix treats a function
// boundary as opaque for the clone decision (enumNeedsClone), so such
// enums stay generic and work. Returns 6.
const genEnumFnSelfPayload = `
enum Step[T] { Done(T), Wait(i32, (i32) => Step[T]) }
function start(v: i32): Step[i32] {
  function resume(p: i32): Step[i32] { return Done(v); }
  return Wait(0, resume);
}
function main(): i32 {
  match (start(6)) {
    Done(v) => { return v; },
    Wait(tok, r) => { match (r(tok)) { Done(v) => { return v; }, Wait(a, b) => { return 0; } } },
  }
}
`

func TestGenEnumFnSelfPayloadInterp(t *testing.T) {
	if code := genEnumInterpExit(t, genEnumFnSelfPayload); code != 6 {
		t.Errorf("fn-self-payload: interp exit = %d, want 6", code)
	}
}

func TestGenEnumFnSelfPayloadX86_64(t *testing.T) {
	if _, code := compileAndRunX86_64(t, genEnumFnSelfPayload); code != 6 {
		t.Errorf("fn-self-payload: x86-64 exit = %d, want 6", code)
	}
}

func genEnumInterpExit(t *testing.T, src string) int {
	t.Helper()
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(src))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if errb.Len() > 0 {
		t.Logf("interp stderr: %s", errb.String())
	}
	return cmd.ProcessState.ExitCode()
}

func TestGenEnumStructPayloadInterp(t *testing.T) {
	if code := genEnumInterpExit(t, genEnumStructPayloadSingle); code != 6 {
		t.Errorf("single: interp exit = %d, want 6", code)
	}
	if code := genEnumInterpExit(t, genEnumStructPayloadMulti); code != 8 {
		t.Errorf("multi: interp exit = %d, want 8", code)
	}
	if code := genEnumInterpExit(t, genEnumEnumPayload); code != 7 {
		t.Errorf("enum-payload: interp exit = %d, want 7", code)
	}
}

func TestGenEnumStructPayloadX86_64(t *testing.T) {
	if _, code := compileAndRunX86_64(t, genEnumStructPayloadSingle); code != 6 {
		t.Errorf("single: x86-64 exit = %d, want 6", code)
	}
	if _, code := compileAndRunX86_64(t, genEnumStructPayloadMulti); code != 8 {
		t.Errorf("multi: x86-64 exit = %d, want 8", code)
	}
	if _, code := compileAndRunX86_64(t, genEnumEnumPayload); code != 7 {
		t.Errorf("enum-payload: x86-64 exit = %d, want 7", code)
	}
}

func TestGenEnumStructPayloadArm64(t *testing.T) {
	if _, code := compileAndRunArm64(t, genEnumStructPayloadSingle); code != 6 {
		t.Errorf("single: arm64 exit = %d, want 6", code)
	}
	if _, code := compileAndRunArm64(t, genEnumStructPayloadMulti); code != 8 {
		t.Errorf("multi: arm64 exit = %d, want 8", code)
	}
}

func TestGenEnumStructPayloadWASM(t *testing.T) {
	if code := runWasm(t, genEnumStructPayloadSingle); code != 6 {
		t.Errorf("single: wasm exit = %d, want 6", code)
	}
	if code := runWasm(t, genEnumStructPayloadMulti); code != 8 {
		t.Errorf("multi: wasm exit = %d, want 8", code)
	}
}
