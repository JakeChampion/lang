package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// examples/self_host/flatten.fern ports the qualified-name rewriting
// half of internal/modload into the self-host pipeline: a
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
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "flatten.fern")
	prog, _, err := modload.Load(filepath.Join(dir, "flatten.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port flatten assertion %d failed", code)
	}
}
