package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPubVisibilityX86_64 pins #6714: a reference to another module's
// non-`pub` function is refused, as it is by native's modload.
//
// The parser used to strip `pub` and discard it ("swallowed without ceremony"),
// so the self-host resolved and CALLED private members of other modules — a
// program native refuses to build produced a working binary here. That is the
// permissive direction, the one that actually blocks "the self-host is the only
// compiler", and unlike the other frontend divergences it carries no
// language-design question: `pub` exists and native enforces it.
//
// Four cases, because the permissive half carries the weight. Rejecting the
// private reference is the fix; ACCEPTING the public one is what proves the
// rule did not simply refuse everything, and accepting a module's calls to its
// OWN private functions is what proves ownership is tracked rather than guessed
// — the first cut got that wrong and rejected checker.fern's internal calls.
//
// The public-reference case doubles as a `is_pub`-survives-the-pipeline check.
// The driver's AST-rewrite passes each rebuild a FuncDecl field by field, and a
// copy site hardcoding `is_pub: false` would drop the bit there and nowhere
// else — the trap #6693 set with `is_const`. It surfaces here as a public
// function suddenly being reported private.
func TestSelfHostPubVisibilityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// The imported module: one exported function, one private, and a private
	// one the module itself calls (so the same-module case is exercised). The
	// types cover #6723 — a public and a private struct, plus an exported enum,
	// whose VARIANTS carry no `pub` of their own and must stay reachable.
	lib := `pub function visible(): i32 { return 1; }
function hidden(): i32 { return 41; }
pub function uses_own_private(): i32 { return hidden() + 1; }
pub struct Open { a: i32 }
struct Secret { b: i32 }
pub enum Shown { One, Two(i32) }
enum Hidden { Alpha, Beta }
pub function own_secret(): i32 { var s: Secret = Secret { b: 5 }; return s.b; }
@must_consume
struct Guarded { d: i32 }
@must_consume
pub struct GuardedOpen { d: i32 }
function eat_guarded(own g: Guarded): i32 { return g.d; }
pub function take_open(own g: GuardedOpen): i32 { return g.d; }
pub function own_guarded(): i32 { return eat_guarded(Guarded { d: 6 }); }
`
	if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write lib.fern: %v", err)
	}

	check := func(t *testing.T, name, src string, wantExit int, wantMsg string) {
		t.Helper()
		path := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		cmd := exec.Command(fernBin, "-check", path)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		_ = cmd.Run()
		code := cmd.ProcessState.ExitCode()
		if code != wantExit {
			t.Errorf("%s: -check exit = %d, want %d\nstderr: %s", name, code, wantExit, errBuf.String())
		}
		if wantMsg != "" && !strings.Contains(errBuf.String(), wantMsg) {
			t.Errorf("%s: stderr missing %q\ngot: %s", name, wantMsg, errBuf.String())
		}
		if wantMsg == "" && strings.Contains(errBuf.String(), "is not exported") {
			t.Errorf("%s: reported a visibility error on a legal program\ngot: %s", name, errBuf.String())
		}
	}

	// REJECT: the private member of an imported module. The message is native's
	// verbatim (uncoded, so it renders with no `error[E…]` tag — matching what
	// native prints for this rule).
	check(t, "private_ref",
		"import \"./lib\";\nfunction main(): i32 { return lib.hidden(); }\n",
		1, "lib.hidden is not exported (declare it as `pub function hidden …` to make it accessible from other modules)")

	// ACCEPT: the exported member. If a rebuild site dropped `is_pub`, this is
	// the case that goes red.
	check(t, "public_ref",
		"import \"./lib\";\nfunction main(): i32 { return lib.visible(); }\n",
		0, "")

	// ACCEPT: a module calling its OWN private function, reached from another
	// module through a public entry point. Ownership must be tracked, not
	// inferred from the mangled name.
	check(t, "own_private",
		"import \"./lib\";\nfunction main(): i32 { return lib.uses_own_private(); }\n",
		0, "")

	// --- #6723: the same rule for TYPES ---------------------------------
	//
	// A qualified type never appears in an expression — it lives in a type-name
	// STRING (a var's declared type, a param/return type, a field type, and a
	// struct literal's type_name). These cases pin that second scan.

	// REJECT: a private struct, named in a type position AND as a literal.
	check(t, "private_type",
		"import \"./lib\";\nfunction main(): i32 { var s: lib.Secret = lib.Secret { b: 7 }; return s.b - 7; }\n",
		1, "lib.Secret is not exported (declare it as `pub struct Secret …` to make it accessible from other modules)")

	// ACCEPT: the exported struct, same two positions.
	check(t, "public_type",
		"import \"./lib\";\nfunction main(): i32 { var s: lib.Open = lib.Open { a: 3 }; return s.a - 3; }\n",
		0, "")

	// REJECT: a private ENUM. The message must name the right keyword — a
	// reader told to write `pub struct Hidden` on an enum is sent nowhere.
	check(t, "private_enum",
		"import \"./lib\";\nfunction main(): i32 { var h: lib.Hidden = lib.Alpha; return 0; }\n",
		1, "is not exported (declare it as `pub enum Hidden …`")

	// ACCEPT: an exported enum's VARIANT. A variant carries no `pub` of its
	// own, so a rule keyed on the variant's flag rather than its enum's would
	// reject every qualified variant construction in the stdlib.
	check(t, "public_enum_variant",
		"import \"./lib\";\nfunction main(): i32 { var s: lib.Shown = lib.Two(4); match (s) { Two(v) => { return v - 4; }, _ => { return 9; } } }\n",
		0, "")

	// ACCEPT: a module using its OWN private type through a public function —
	// the type sibling of the own-private-function case.
	check(t, "own_private_type",
		"import \"./lib\";\nfunction main(): i32 { return lib.own_secret() - 5; }\n",
		0, "")

	// An ATTRIBUTE in front of the declaration used to disable the rule. The
	// `@must_consume` and `@derive` paths each parse their own `pub` and then
	// stamped a hardcoded `is_pub: true`, so a DECORATED private type was
	// reachable from every module while an undecorated one beside it was not.
	// Both directions: the reject proves the flag is read, the accept proves
	// the keyword still reaches the declaration past the attribute.
	//
	// The private case names the type in a PARAMETER position rather than
	// binding a value: a marked local carries the E067 obligation too, and a
	// second diagnostic would let the case pass on the wrong error. `own` is
	// the declared sink, so the signature costs no obligation of its own.
	check(t, "private_must_consume_type",
		"import \"./lib\";\nfunction peek(own g: lib.Guarded): i32 { return g.d; }\nfunction main(): i32 { return 0; }\n",
		1, "lib.Guarded is not exported (declare it as `pub struct Guarded …` to make it accessible from other modules)")

	check(t, "public_must_consume_type",
		"import \"./lib\";\nfunction main(): i32 { return lib.take_open(lib.GuardedOpen { d: 3 }) - 3; }\n",
		0, "")

	// The module's own private `@must_consume` type, reached through a public
	// entry point — the decorated sibling of own_private_type.
	check(t, "own_private_must_consume_type",
		"import \"./lib\";\nfunction main(): i32 { return lib.own_guarded() - 6; }\n",
		0, "")

	// REJECT on the COMPILE path too, not just `-check`: native refuses to
	// BUILD such a program, and emitting a binary anyway is the divergence this
	// closes. A build that succeeds here would mean the rule guards only the
	// checker while codegen still resolves the private symbol.
	src := "import \"./lib\";\nfunction main(): i32 { return lib.hidden(); }\n"
	path := filepath.Join(dir, "private_build.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := filepath.Join(dir, "private_build.bin")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, path)
	var buildErr strings.Builder
	cmd.Stderr = &buildErr
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("compile of a private cross-module reference exited 0, want non-zero (native refuses to build it)")
	}
	// The exit code alone would pass on ANY refusal — an unknown target name
	// among them, which is how a stale `-target x86-64` would keep this case
	// green while proving nothing (#6635 renamed the targets under it). Assert
	// the reason, not just the failure.
	if !strings.Contains(buildErr.String(), "is not exported") {
		t.Errorf("compile failed for the wrong reason — want the visibility diagnostic, got: %s", buildErr.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("compile emitted %s for a program native refuses to build", out)
	}
}
