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
	// one the module itself calls (so the same-module case is exercised).
	lib := `pub function visible(): i32 { return 1; }
function hidden(): i32 { return 41; }
pub function uses_own_private(): i32 { return hidden() + 1; }
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
	cmd := exec.Command(fernBin, "-target", "x86-64", "-o", out, path)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("compile of a private cross-module reference exited 0, want non-zero (native refuses to build it)")
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("compile emitted %s for a program native refuses to build", out)
	}
}
