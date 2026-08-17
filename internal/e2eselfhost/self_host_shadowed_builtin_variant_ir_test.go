package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A user enum may name a variant `Ok`, `Some`, `Err` or `None`. The self-host
// match lowering used to decide "is this a builtin pattern" from the arm's NAME
// alone (`is_builtin_pattern`), with no reference to the scrutinee's enum, so a
// user variant sharing one of those names was routed to the Option/Result
// tag-test shape. That failed two different ways:
//
//   - PAYLOAD-carrying names (`Ok(n)` / `Some(n)` / `Err(n)`) tried to recover
//     an Option/Result type off the scrutinee slot, found none, and bailed the
//     whole module. With the AST emitter retired that is a hard compile error.
//   - `None` carries no payload, so it skipped the recovery and reached a
//     hardcoded builtin tag (`None`/`Err` => 1). A user `None` sitting at any
//     other index then never matched: it compiled clean and returned the wrong
//     answer, with no diagnostic at all.
//
// So these cases pin BOTH failure modes. runCaptureStrictIR fails the bail (a
// silent fall-through cannot pass), and every case is checked against the
// interpreter oracle, which is the only thing that would have caught the
// `None` miscompile.
//
// The genuine-builtin cases are the control in the other direction: the fix
// separates the two by variant OWNERSHIP (the parser gives each user variant a
// struct with `enum_owner`; the builtins have none), so a change that made
// everything take the user-enum path would fail these instead.
var selfHostShadowedBuiltinVariantCases = []struct {
	name string
	src  string
}{
	// Payload-carrying shadows — each bailed the module before the fix.
	{"user-ok", `enum E { Ok(i32), Bad(i32) }
function main(): i32 { var e: E = E.Ok(9); match (e) { Ok(n) => { return n; }, _ => { return 1; } } }`},
	{"user-some", `enum E { Some(i32), Nope }
function main(): i32 { var e: E = E.Some(9); match (e) { Some(n) => { return n; }, _ => { return 1; } } }`},
	{"user-err", `enum E { Err(i32), Fine }
function main(): i32 { var e: E = E.Err(9); match (e) { Err(n) => { return n; }, _ => { return 1; } } }`},

	// The silent one: `E.None` is index 0, but the builtin table forced the
	// tag to 1, so this arm never fired and control fell to the wildcard —
	// self-host returned 7 where native returns 5.
	{"user-none-at-index-0", `enum E { None, Bad(i32), Third(i32) }
function main(): i32 { var e: E = E.None; match (e) { None => { return 5; }, Bad(n) => { return 6; }, _ => { return 7; } } }`},
	// Same shape with the shadowing variant away from index 0, so a fix that
	// merely flipped the hardcoded tag would still fail.
	{"user-none-at-index-2", `enum E { Aa(i32), Bb(i32), None }
function main(): i32 { var e: E = E.None; match (e) { None => { return 5; }, Aa(n) => { return 6; }, _ => { return 7; } } }`},

	// The other binding sites reach the same lowering.
	{"if-let-user-ok", `enum E { Ok(i32), Bad(i32) }
function main(): i32 { var e: E = E.Ok(9); if let Ok(n) = e { return n; } return 1; }`},
	{"let-else-user-ok", `enum E { Ok(i32), Bad(i32) }
function main(): i32 { var e: E = E.Ok(9); let Ok(n) = e else { return 1; }; return n; }`},
	{"match-expr-user-ok", `enum E { Ok(i32), Bad(i32) }
function main(): i32 { var e: E = E.Ok(9); var r: i32 = match (e) { Ok(n) => n, _ => 1 }; return r; }`},
	// A shadowing variant nested inside another user enum.
	{"nested-user-ok", `enum Inner { Ok(i32), Bad(i32) }
enum Outer { Sm(Inner), Nn(i32) }
function main(): i32 { var o: Outer = Outer.Sm(Inner.Ok(9)); match (o) { Sm(Ok(n)) => { return n; }, _ => { return 1; } } }`},

	// Controls: the real builtins must keep the tag-test path.
	{"builtin-result", `function f(x: i32): Result[i32, string] { if (x > 0) { return Ok(x); } return Err("neg"); }
function main(): i32 { match (f(9)) { Ok(v) => { return v; }, Err(e) => { return 1; } } }`},
	{"builtin-result-err", `function f(x: i32): Result[i32, string] { if (x > 0) { return Ok(x); } return Err("neg"); }
function main(): i32 { match (f(0 - 1)) { Ok(v) => { return v; }, Err(e) => { return 6; } } }`},
	{"builtin-bool", `function main(): i32 { var b: boolean = true; match (b) { true => { return 8; }, _ => { return 2; } } }`},
}

func TestSelfHostShadowedBuiltinVariantIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range selfHostShadowedBuiltinVariantCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, prog)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
