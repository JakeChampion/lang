package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// pubUseProject is a 3-module program: helpers defines the real symbols,
// facade re-exports them via `pub use`, and main imports facade and calls
// the re-exported names. add5(10) + BONUS(100) = 115.
var pubUseProject = map[string]string{
	"helpers.fern": `pub function add5(n: i32): i32 { return n + 5; }
pub const BONUS: i32 = 100;`,
	"facade.fern": `pub use "./helpers".{add5, BONUS};`,
	"main.fern": `import "./facade";
function main(): i32 { return facade.add5(10) + facade.BONUS; }`,
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// `pub use` re-exports resolve + run end-to-end on the interpreter: a
// consumer calls the re-exported `facade.add5` / `facade.BONUS` and they
// dispatch to the original helpers definitions. See docs/PRELUDE-TO-MODULES.md.
func TestInterpPubUseReexport(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := writeProject(t, pubUseProject)
	cmd := exec.Command(bin, "-interp", filepath.Join(dir, "main.fern"))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 115 {
		t.Errorf("exit = %d, want 115\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// Same program through the native x86-64 backend: re-exports are a
// load-time rewrite, so codegen sees ordinary flat calls.
func TestX86_64PubUseReexport(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeProject(t, pubUseProject)

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
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 115 {
		t.Errorf("native exit = %d, want 115", code)
	}
}

// pubUseTypeProject re-exports a struct, an enum, and a trait through a
// facade, and the consumer uses all the type-position shapes: a struct
// literal + annotation (facade.Point), an enum match (facade.Shape), a
// trait impl method call (p.area()), and a `dyn facade.Trait` dispatch.
// 42 (6*7) + 40 (Square(10)*4) + 42 (dyn) = 124. See docs/PRELUDE-TO-MODULES.md.
var pubUseTypeProject = map[string]string{
	"orig.fern": `pub struct Point { x: i32, y: i32 }
pub enum Shape { Circle(i32), Square(i32) }
pub trait Area { function area(self: Self): i32; }
impl Area for Point { function area(self: Self): i32 { return self.x * self.y; } }`,
	"facade.fern": `pub use "./orig".{Point, Shape, Area};`,
	"main.fern": `import "./facade";
function describe(s: facade.Shape): i32 { return match (s) { Circle(r) => r, Square(w) => w * 4 }; }
function dynArea(a: dyn facade.Area): i32 { return a.area(); }
function main(): i32 {
    var p: facade.Point = facade.Point { x: 6, y: 7 };
    return p.area() + describe(Square(10)) + dynArea(p);
}`,
}

func TestInterpPubUseTypeReexport(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := writeProject(t, pubUseTypeProject)
	cmd := exec.Command(bin, "-interp", filepath.Join(dir, "main.fern"))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 124 {
		t.Errorf("exit = %d, want 124\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64PubUseTypeReexport(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeProject(t, pubUseTypeProject)

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
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 124 {
		t.Errorf("native exit = %d, want 124", code)
	}
}
