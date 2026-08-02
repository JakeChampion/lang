package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLocalShadowsImportedDecl is a regression for the module-loader
// (flatten.fern) bug where a local variable shadowing a module-level decl was
// rewritten to the decl's mangled name on import. std/math's `random_int` has
// a local `range` that shadows the module-level `range()` function; when
// std/math is imported (and thus mangled), the local `range` in `u % range`
// was rewritten to the `range()` function value, so the modulo divided by a
// function pointer and `random_int` returned out-of-range garbage. That was
// the root cause of the intermittent fuzz_shrink crash.
//
// `random_int(0, 1)` is deterministic: range == 1, so `u % 1 == 0` and it must
// return 0. With the bug it returns garbage (a nonzero exit). Compiled through
// the self-host file-loading driver (which runs flatten's mangle pass), native.
func TestSelfHostLocalShadowsImportedDecl(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	prog := filepath.Join(t.TempDir(), "shadow_prog.fern")
	src := "" +
		"import \"std/math\";\n" +
		"function main(): i32 { return math.random_int(0, 1); }\n"
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("self-host compile failed: %v (asm %d bytes)", err, len(asm))
	}
	bin := buildBin(t, gcc, dir, "shadow_prog", string(asm))
	// Run several times: random_int draws fresh entropy each call, so a
	// surviving bug (dividing by a stale function pointer) would surface
	// across runs. Correct behaviour returns 0 every time.
	for i := 0; i < 20; i++ {
		c := exec.Command(bin)
		c.Run()
		if code := c.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("random_int(0,1) returned %d, want 0 (local `range` mis-resolved to range() function)", code)
		}
	}
}
