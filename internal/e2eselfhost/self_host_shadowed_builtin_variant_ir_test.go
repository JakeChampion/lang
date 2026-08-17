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

	// Qualified construction of a shadowing payload-less variant. This is the
	// oracle-checkable half of the construction story — native accepts it
	// because both sides name the enum.
	{"qualified-user-none-idx-2", `enum E { Aa(i32), Bb(i32), None }
function main(): i32 { var e: E = E.None; match (e) { E.None => { return 7; }, _ => { return 4; } } }`},
	{"qualified-user-none-idx-0", `enum E { None, Aa(i32), Bb(i32) }
function main(): i32 { var e: E = E.None; match (e) { E.None => { return 7; }, _ => { return 4; } } }`},
	// Genuine Option `None` in the same positions, so the reordering below
	// cannot have stolen the builtin's path.
	{"builtin-none-return", `function f(): Option[i32] { return None; }
function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 7; } } }`},
	{"builtin-none-var-init", `function main(): i32 { var o: Option[i32] = None; match (o) { Some(v) => { return v; }, None => { return 7; } } }`},

	// The control that matters most, and the one the first cut of this fix
	// did not have: a GENUINE Option matched while a shadowing user enum is
	// merely DECLARED elsewhere in the program.
	//
	// Deciding the builtin-vs-user question by variant ownership alone is not
	// enough — ownership is a property of the name across the whole module, so
	// one `enum O2 { Some(i32), None }` anywhere sent every `Some` arm down the
	// user path, including these, which then answered wrong with no diagnostic.
	// The scrutinee is what settles it: a real Option/Result local carries an
	// `opt_type` and no struct type.
	{"genuine-option-with-shadow-declared", `enum O2 { Some(i32), None }
function main(): i32 { var r: Option[i32] = Option.Some(3); match (r) { Some(v) => { return v; }, None => { return 99; } } }`},
	{"genuine-option-none-with-shadow-declared", `enum O2 { Some(i32), None }
function main(): i32 { var r: Option[i32] = Option.None; match (r) { Some(v) => { return v; }, None => { return 99; } } }`},
	// The constructions are qualified because `R2` makes the bare names
	// ambiguous (E036) — the arm patterns need no qualifying, since the
	// scrutinee's type settles them, which is the asymmetry under test.
	{"genuine-result-with-shadow-declared", `enum R2 { Ok(i32), Err(i32) }
function f(x: i32): Result[i32, string] { if (x > 0) { return Result.Ok(x); } return Result.Err("neg"); }
function main(): i32 { match (f(4)) { Ok(v) => { return v; }, Err(e) => { return 99; } } }`},
	// Both kinds of enum matched in ONE program, so the two paths have to
	// coexist rather than one winning globally.
	{"user-enum-and-genuine-option-together", `enum O2 { Some(i32), None }
function main(): i32 { var o: O2 = O2.None; var r: Option[i32] = Option.Some(3); var t: i32 = 0;
    match (o) { O2.Some(n) => { t = n; }, O2.None => { t = 7; } }
    match (r) { Some(v) => { t = t + v; }, None => { t = t + 99; } }
    return t; }`},
}

// Bare-name construction of a shadowing payload-less variant — `var e: E = None`
// where the user enum declares `None`.
//
// The ident lowering tested `id.name == "None"` BEFORE the declared-struct case,
// the inverse of the call arm's order, so this built an Option box with tag 1
// instead of the user's variant. It then matched no variant index at all and the
// program silently returned the wildcard's value.
//
// These cannot be oracle-checked: native REJECTS a bare colliding name outright
// (`E036: variant "None" is declared in multiple enums … qualify the reference`),
// so there is no native answer to compare against. The self-host computes the
// same E036 but drops it, so it compiles the program instead. The build gate
// now enforces every coded diagnostic EXCEPT the partial-port rules measured to
// false-positive (#6961), and E036 is one of those exclusions: it still misreads
// a derive-synthesised `Status.default()` in `conformance/cases/derive_default`
// as a bad qualified variant. Making E036 gate means fixing that misreading and
// deleting its line from `is_partial_checker_gap_code` (checker.fern).
//
// Until then the value it produces should at least be the USER's variant, which
// is what these pin. When E036 starts gating, these become compile errors and
// this test moves to asserting the diagnostic.
var selfHostBareShadowedConstructionCases = []struct {
	name string
	src  string
	exit int
}{
	{"bare-user-none-idx-2", `enum E { Aa(i32), Bb(i32), None }
function main(): i32 { var e: E = None; match (e) { E.None => { return 7; }, _ => { return 4; } } }`, 7},
	{"bare-user-none-idx-0", `enum E { None, Aa(i32), Bb(i32) }
function main(): i32 { var e: E = None; match (e) { E.None => { return 7; }, _ => { return 4; } } }`, 7},
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

func TestSelfHostBareShadowedConstructionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range selfHostBareShadowedConstructionCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (the USER enum's variant, not the builtin Option)", tc.name, code, tc.exit)
			}
		})
	}
}
