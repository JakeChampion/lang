package e2eselfhost

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"testing"
)

// Fourth self-host milestone after the lexer (#609), parser (#611 /
// #617) and checker (#619). `examples/self_host/interp.fern` is a
// tree-walking interpreter written in lang — it imports `./lexer`
// and `./parser`, evaluates the Stmt[] tree from
// `parser.parse_program(toks)`, and produces a runtime Value
// (VInt / VBool / VString / VErr). This completes the
// lexer → parser → interp vertical slice in lang: the port can
// now actually *run* lang programs end-to-end, from a thin
// self-validating driver.
//
// Scope (matches the parser's): i32 / bool / string values, var
// binding via a flat env, arithmetic / comparison / logical /
// string-concat ops over the parser's op set, integer literal
// parsing from the lexer's TokNumber text.
//
// The .fern file's main() runs seven sub-checks: arithmetic +
// ident lookup, string concat, logical + equality, unary minus,
// integer division, division-by-zero VErr propagation, and
// undefined-identifier VErr propagation. Exit code 0 means every
// assertion passed; non-zero codes identify which arm failed.

func writeSelfHostInterpProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp.fern")
	return dir
}

func TestSelfHostInterpX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	dir := writeSelfHostInterpProject(t)
	binPath := buildSelfHostBinFor(t, dir, "interp.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port interp assertion %d failed", code)
	}
}

func TestSelfHostInterpArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	dir := writeSelfHostInterpProject(t)
	binPath := buildSelfHostBinFor(t, dir, "interp.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port interp assertion %d failed", code)
	}
}
