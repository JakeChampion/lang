package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `?` on an `@try` enum, both shapes, both paths. Before the marker, `?` was
// hardwired to Option and Result by name; a user enum of identical shape got
// `error[E042]: ? operator requires an Option or Result value`. See
// docs/TRY.md and #9302.
//
// The two shapes exercise the two lowerings, which is the whole reason
// TryKind exists: a payloadless failure variant (MyOpt's Gone) BUILDS a fresh
// tag-1 value, while one carrying a payload (Outcome's Bad) FORWARDS the
// source value unchanged. Both the success and failure path of each, because
// the failure path is the one that early-returns.
const tryMarkerSrc = `@try
enum MyOpt[T] { Here(T), Gone }

@try
enum Outcome[T, E] { Good(T), Bad(E) }

function pick(m: MyOpt[i32]): MyOpt[i32] {
    var v: i32 = m?;
    return Here(v + 1);
}

function step(o: Outcome[i32, string]): Outcome[i32, string] {
    var v: i32 = o?;
    return Good(v * 2);
}

function main(): i32 {
    var a: i32 = 0;
    match (pick(Here(7)))  { Here(v) => { a = a + v; },  Gone => { a = a + 100; } }   // 8
    match (pick(Gone))     { Here(v) => { a = a + v; },  Gone => { a = a + 10; } }    // 10
    match (step(Good(11))) { Good(v) => { a = a + v; },  Bad(e) => { a = a + 100; } } // 22
    match (step(Bad("xy"))) { Good(v) => { a = a + v; }, Bad(e) => { a = a + e.len(); } } // 2
    return a;   // 8 + 10 + 22 + 2 = 42
}
`

// errdefer fires on the failure path of `?` for a marked enum exactly as it
// does for Option and Result — the IR gate now asks Info.TryShapes instead of
// comparing against the two builtin names.
const tryMarkerErrdeferSrc = `@try
enum MyOpt[T] { Here(T), Gone }

function inner(m: MyOpt[i32]): MyOpt[i32] {
    errdefer { print("rollback"); }
    var v: i32 = m?;
    return Here(v);
}

function main(): i32 {
    match (inner(Here(3))) { Here(v) => {}, Gone => { return 90; } }   // no rollback
    match (inner(Gone))    { Here(v) => { return 91; }, Gone => {} }   // rollback
    return 7;
}
`

func runInterpExitCode(t *testing.T, src string) (string, int) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return out.String() + errb.String(), cmd.ProcessState.ExitCode()
}

func TestInterpTryMarker(t *testing.T) {
	out, code := runInterpExitCode(t, tryMarkerSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestX86_64TryMarker(t *testing.T) {
	out, code := compileAndRunX86_64(t, tryMarkerSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64TryMarker(t *testing.T) {
	out, code := compileAndRunArm64(t, tryMarkerSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMTryMarker(t *testing.T) {
	if code := runWasm(t, tryMarkerSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}

func TestInterpTryMarkerErrdefer(t *testing.T) {
	out, code := runInterpExitCode(t, tryMarkerErrdeferSrc)
	if code != 7 {
		t.Errorf("exit = %d, want 7\n%s", code, out)
	}
	if got := bytes.Count([]byte(out), []byte("rollback")); got != 1 {
		t.Errorf("rollback printed %d times, want exactly 1 (failure path only)\n%s", got, out)
	}
}

func TestX86_64TryMarkerErrdefer(t *testing.T) {
	out, code := compileAndRunX86_64(t, tryMarkerErrdeferSrc)
	if code != 7 {
		t.Errorf("exit = %d, want 7\n%s", code, out)
	}
	if got := bytes.Count([]byte(out), []byte("rollback")); got != 1 {
		t.Errorf("rollback printed %d times, want exactly 1 (failure path only)\n%s", got, out)
	}
}
