package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostIRExternProbe exercises the program-wide known-symbol set threaded
// into IR eligibility — the foundation of the per-module-compilation epic
// (#3451 step 1 / #3453). Today the driver merges every imported module into one
// translation unit, so every cross-module call is local and eligibility's
// "is this call's target defined here?" check (calls_only_known /
// const_funcs_only_known) always sees it. Per-module, a call from module A to an
// imported function in B targets B's `__fn_*` symbol, which is NOT in A's own
// module — so the bare check would bail A to the AST emitter. Step 1 threads the
// set of symbols defined SOMEWHERE in the loaded import graph into the per-
// function check so such a call is admitted as an extern (resolved at link)
// rather than bailing.
//
// The asm_ir_run `-ir-extern <name>` flag injects a name into that program-wide
// known set, modelling "this symbol is defined in a sibling module". The cases
// pin both directions: a call to an otherwise-unknown name BAILs the module to
// AST when the name is NOT declared extern, and reports `ir` (module: IR) when
// it IS — proving eligibility distinguishes "defined in this module" from
// "known program-wide", which is exactly what per-module emit needs.
func TestSelfHostIRExternProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	// probe runs `<driver> -ir-probe [extra...]` with prog on stdin.
	probe := func(t *testing.T, prog string, extra ...string) string {
		t.Helper()
		args := append([]string{"-ir-probe"}, extra...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q (extra %v): %v", prog, extra, err)
		}
		return string(out)
	}

	t.Run("cross-module-call-admitted-as-extern", func(t *testing.T) {
		// `mystery` is not defined in this module. Without -ir-extern it is an
		// unknown call and the module bails to AST; declaring it extern (defined
		// in a sibling module) admits the call and the module is IR-eligible.
		prog := "function main(): i32 { return mystery(3); }"

		bail := probe(t, prog)
		if !strings.Contains(bail, "main: BAIL call") || !strings.Contains(bail, "module: AST") {
			t.Errorf("without -ir-extern: expected BAIL call / module: AST\n--- report ---\n%s", bail)
		}

		ok := probe(t, prog, "-ir-extern", "mystery")
		if !strings.Contains(ok, "main: ir") || !strings.Contains(ok, "module: IR") {
			t.Errorf("with -ir-extern mystery: expected main: ir / module: IR\n--- report ---\n%s", ok)
		}
	})

	t.Run("extern-set-is-targeted-not-blanket", func(t *testing.T) {
		// Declaring a DIFFERENT symbol extern must not rescue a call to an
		// unrelated unknown name — the known set is matched by name, not a
		// blanket "ignore unknown calls" switch.
		prog := "function main(): i32 { return mystery(3); }"
		rep := probe(t, prog, "-ir-extern", "unrelated")
		if !strings.Contains(rep, "main: BAIL call") || !strings.Contains(rep, "module: AST") {
			t.Errorf("extern of an unrelated name should not admit mystery\n--- report ---\n%s", rep)
		}
	})

	t.Run("local-functions-still-win", func(t *testing.T) {
		// A module that defines its own callees is IR-eligible with no externs —
		// the threading is additive and the empty-known path is unchanged.
		prog := "function add(a: i32, b: i32): i32 { return a + b; }\n" +
			"function main(): i32 { return add(2, 3); }"
		rep := probe(t, prog)
		if !strings.Contains(rep, "add: ir") || !strings.Contains(rep, "main: ir") || !strings.Contains(rep, "module: IR") {
			t.Errorf("self-contained module should be IR with no externs\n--- report ---\n%s", rep)
		}
	})
}
