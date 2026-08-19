package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Two MODULES declaring one variant name at DIFFERENT ordinals: `Wrap` is
// index 2 in a's Kind and index 0 in b's Kind. Neither module imports the
// other, so under module-scoped resolution (#6951) each module's bare
// `Wrap(v)` is unambiguous where it stands and both reach the IR with no
// qualifier.
//
// That is the shape #6944 measured as unreachable when it closed: it held
// the remaining global `lookupVariant` scans safe on the premise that the
// checker rejects every ambiguous bare name first. Module scoping retires
// the premise, and those scans ran over a Go map — with one still in place
// this program returned 79 or 69 depending on which enum the map handed
// back, varying between compiles of identical source.
//
// The accessors match with BARE arms deliberately: arms already resolve
// against the scrutinee (#6950), so the constructor is the half under test.
const variantOrdinalA = `enum Kind { Pad0, Pad1, Wrap(i32) }
pub function mk(v: i32): Kind { return Wrap(v); }
pub function get(k: Kind): i32 { match (k) { Wrap(v) => { return v; }, _ => { return -1; } } }
`

const variantOrdinalB = `enum Kind { Wrap(i32), Pad1, Pad2 }
pub function mk(v: i32): Kind { return Wrap(v); }
pub function get(k: Kind): i32 { match (k) { Wrap(v) => { return v; }, _ => { return -1; } } }
`

// a's Wrap holds 7 and b's holds 9, so a constructor that built the other
// module's tag falls into that accessor's wildcard and yields -1 there:
// 79 correct, 69 / -1-bearing answers wrong.
const variantOrdinalMain = `import "./a";
import "./b";
function main(): i32 { return a.get(a.mk(7)) * 10 + b.get(b.mk(9)); }
`

const variantOrdinalWant = 79

func writeVariantOrdinalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"a.fern":    variantOrdinalA,
		"b.fern":    variantOrdinalB,
		"main.fern": variantOrdinalMain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestCrossModuleVariantOrdinalEmitIsDeterministic asserts the property the
// bug violates rather than the answer it happens to produce. The miscompile
// is map-order dependent, so a single compile-and-run agrees with the oracle
// often enough that a naive regression test looks green — five builds of this
// program gave 69, 69, 79, 69, 79. Emitting repeatedly in one process and
// requiring byte-identical output fails on every run instead, and needs no
// toolchain.
func TestCrossModuleVariantOrdinalEmitIsDeterministic(t *testing.T) {
	srcPath := filepath.Join(writeVariantOrdinalProject(t), "main.fern")
	first := emitSharedVariantAsm(t, srcPath)
	const runs = 200
	for i := 1; i < runs; i++ {
		if got := emitSharedVariantAsm(t, srcPath); got != first {
			t.Fatalf("emit %d of %d differs from the first: a variant name two MODULES share is being resolved by a map-order scan, so the emitted tag varies per compile", i+1, runs)
		}
	}
}

// …and the answer itself, on both compiled backends. Subject to the same
// one-in-two flakiness when the bug is present, so these are the companion
// assertions and the determinism property above is the gate.
func TestCrossModuleVariantOrdinalX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeVariantOrdinalProject(t)
	asm := lowerVariantOrdinal(t, dir, false)
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != variantOrdinalWant {
		t.Errorf("exit code: got %d, want %d", got, variantOrdinalWant)
	}
}

func TestCrossModuleVariantOrdinalArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	dir := writeVariantOrdinalProject(t)
	asm := lowerVariantOrdinal(t, dir, true)
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != variantOrdinalWant {
		t.Errorf("exit code: got %d, want %d", got, variantOrdinalWant)
	}
}

// lowerVariantOrdinal runs the front end over the project and emits for one
// backend. Same pipeline cmd/fern uses; the sibling cross-module tests in
// this package assemble theirs the same way.
func lowerVariantOrdinal(t *testing.T, dir string, arm64 bool) string {
	t.Helper()
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	var asm string
	if arm64 {
		asm, err = arm64codegen.Emit(prog, info)
	} else {
		asm, err = x86_64.Emit(prog, info)
	}
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}
