package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

// compiler/flatten.fern ports the qualified-name rewriting
// half of internal/pkg/modload into the self-host pipeline: a
// cross-module reference `mod.name` is rewritten to the flat mangled
// name `mod__name` across call / type / pattern / field positions.
// This is the foundation for letting a multi-module program (the
// compiler itself) lower to a single flat namespace the asm emitter
// understands.
//
// flatten.fern's main() flattens a module that references ./lexer
// three ways (qualified type, qualified call, qualified variant
// pattern) and asserts each reads as the flat `lexer__*` form, that a
// plain field access (`p.x`) is left untouched, and the
// rewrite_type_name edge cases (array suffix, non-imported prefix).
// Exit 0 means every assertion held. The file imports ./parser
// (which imports ./lexer), so all three are copied into the temp
// dir for modload to resolve.
func TestSelfHostFlattenX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "flatten.fern")
	binPath := buildSelfHostBinFor(t, dir, "flatten.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port flatten assertion %d failed", code)
	}
}
