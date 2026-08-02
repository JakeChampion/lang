package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// Nested-generic receiver-method dispatch (#4878). A method whose receiver
// type is itself a generic-over-a-generic — `(o: Option[Option[T]]) flatten()`
// and `(r: Result[Result[T, E], E]) flatten()` — must register under its base
// namespace ("Option" / "Result") and dispatch at a call site the same as a
// single-generic receiver (`Option[T]`). #4878 reported an E043 ("field access
// on non-struct value") for the *imported* form; that turned out to be a
// stale-embedded-stdlib artifact (the freshly-added combinator wasn't in the
// not-yet-rebuilt binary's `go:embed`ded stdlib), not a dispatch bug — the
// nested receiver resolves fine on every backend once the stdlib is rebuilt.
// These tests pin that: the shipped `std/option` / `std/result` `flatten`
// combinators run end-to-end on the native backends, and an inline nested
// receiver lowers through the self-host IR path.

// stdFlattenSrc imports the shipped combinators and exercises both the present
// (Some/Ok) and short-circuit (None/Err) arms. 7 + 100 + 30 + 5 = 142.
const stdFlattenSrc = `import "std/option";
import "std/result";
function main(): i32 {
    var x: Option[Option[i32]] = Some(Some(7));
    var a: i32 = match (x.flatten()) { Some(v) => v, None => 0 };
    var y: Option[Option[i32]] = None;
    var b: i32 = match (y.flatten()) { Some(v) => v, None => 100 };
    var r: Result[Result[i32, i32], i32] = Ok(Ok(30));
    var c: i32 = match (r.flatten()) { Ok(v) => v, Err(e) => e };
    var r2: Result[Result[i32, i32], i32] = Err(5);
    var d: i32 = match (r2.flatten()) { Ok(v) => v, Err(e) => e };
    return a + b + c + d;
}
`

// TestStdFlattenNative runs the shipped std/option + std/result `flatten`
// combinators on the native interp / x86-64 / wasm backends.
func TestStdFlattenNative(t *testing.T) {
	p := writeIterProg(t, stdFlattenSrc)
	if _, code := runFixtureInterp(t, p, ""); code != 142 {
		t.Errorf("std flatten interp = %d, want 142", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 142 {
		t.Errorf("std flatten x86-64 = %d, want 142", code)
	}
	if code := runWasm(t, stdFlattenSrc); code != 142 {
		t.Errorf("std flatten wasm = %d, want 142", code)
	}
}

// TestStdFlattenArm64 is the arm64 leg (CI-gated; qemu).
func TestStdFlattenArm64(t *testing.T) {
	p := writeIterProg(t, stdFlattenSrc)
	if _, code := runFixtureArm64(t, p, ""); code != 142 {
		t.Errorf("std flatten arm64 = %d, want 142", code)
	}
}

// nestedRecvFlattenPrelude defines flatten inline (local, not imported) so the
// self-host driver — which resolves no stdlib — can compile it. The receiver
// types are the same nested generics as the shipped combinators.
const nestedRecvFlattenPrelude = `pub function (o: Option[Option[T]]) flatten(): Option[T] {
    match (o) { Some(inner) => { return inner; }, None => { return None; } }
}
pub function (r: Result[Result[T, E], E]) flatten(): Result[T, E] {
    match (r) { Ok(inner) => { return inner; }, Err(e) => { return Err(e); } }
}
`

// The main bodies use match *statements* (each arm `return`s), not a match
// *expression* — value-position `match` currently routes the enclosing
// function to the legacy AST backend, which would make the "ir" routing
// assertion below spuriously fail. Statement-style keeps the whole program on
// the IR path so the assertion actually exercises the nested receiver there.
var nestedRecvFlattenCases = []struct {
	name string
	main string
	want int
}{
	// Some(Some(7)).flatten() -> Some(7) -> 7.
	{"opt-some", `function main(): i32 { var x: Option[Option[i32]] = Some(Some(7)); match (x.flatten()) { Some(v) => { return v; }, None => { return 0; } } }`, 7},
	// None.flatten() -> None -> 100.
	{"opt-none", `function main(): i32 { var x: Option[Option[i32]] = None; match (x.flatten()) { Some(v) => { return v; }, None => { return 100; } } }`, 100},
	// Ok(Ok(30)).flatten() -> Ok(30) -> 30.
	{"res-ok", `function main(): i32 { var r: Result[Result[i32, i32], i32] = Ok(Ok(30)); match (r.flatten()) { Ok(v) => { return v; }, Err(e) => { return e; } } }`, 30},
	// Err(5).flatten() -> Err(5) -> 5.
	{"res-err", `function main(): i32 { var r: Result[Result[i32, i32], i32] = Err(5); match (r.flatten()) { Ok(v) => { return v; }, Err(e) => { return e; } } }`, 5},
}

func nestedRecvFlattenProg(mainBody string) string {
	return nestedRecvFlattenPrelude + mainBody + "\n"
}

// TestNestedRecvFlattenNative runs the inline nested-receiver flatten programs
// on the native interp / x86-64 / wasm backends, oracle-checked.
func TestNestedRecvFlattenNative(t *testing.T) {
	for _, tc := range nestedRecvFlattenCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, nestedRecvFlattenProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, nestedRecvFlattenProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostNestedRecvFlattenIRX86_64 routes each inline case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks the
// compiled binary — proving the self-host compiler both dispatches a
// nested-generic receiver method and lowers it through the IR path.
func TestSelfHostNestedRecvFlattenIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedRecvFlattenCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(nestedRecvFlattenProg(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
