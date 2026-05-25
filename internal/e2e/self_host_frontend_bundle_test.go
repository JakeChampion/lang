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

// Real-frontend self-host milestone: bundle the ACTUAL lexer.fern +
// parser.fern (the compiler's own front end — both pure, importing
// only core/no_prelude and using byte builtins, no stdlib) with a
// tiny entry that lexes + parses an embedded program and returns its
// function count. The bundle is fed through bundle_run; the merged
// asm is assembled + run and must return the function count (1).
//
// This exercises the full self-host pipeline on real compiler source:
// flatten + mangle + merge (flatten.fern) and lowering a program that
// uses cross-module structs with string / array fields (Lex { src:
// string, … }, Par { toks: lexer.Token[], … }) — the case that
// required the infer_expr_type struct-field-read fix in asm.fern.
func TestSelfHostFrontendBundleX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run.fern"))
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
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"function main(): i32 {\n" +
		"    var toks: lexer.Token[] = lexer.tokenize(\"function f(): i32 { return 42; }\");\n" +
		"    var mod: parser.Module = parser.parse_module(toks);\n" +
		"    return mod.funcs.len();\n" +
		"}\n"

	var bundle bytes.Buffer
	bundle.WriteString("///MODULE lexer\n")
	bundle.Write(lexerSrc)
	bundle.WriteString("\n///MODULE parser\n")
	bundle.Write(parserSrc)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(entry)

	var dcmd *exec.Cmd
	if len(runner) == 0 {
		dcmd = exec.Command(driverBin)
	} else {
		dcmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	dcmd.Stdin = bytes.NewReader(bundle.Bytes())
	mergedAsm, err := dcmd.Output()
	if err != nil {
		t.Fatalf("run driver: %v", err)
	}
	if len(mergedAsm) == 0 {
		t.Fatal("driver emitted 0 bytes for the frontend bundle")
	}
	t.Logf("merged frontend asm: %d bytes", len(mergedAsm))

	mergedAsmPath := filepath.Join(dir, "merged.s")
	mergedBin := filepath.Join(dir, "merged")
	if err := os.WriteFile(mergedAsmPath, mergedAsm, 0o644); err != nil {
		t.Fatalf("write merged asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", mergedAsmPath, "-o", mergedBin).CombinedOutput(); err != nil {
		t.Fatalf("merged gcc: %v\n%s", err, out)
	}
	var mcmd *exec.Cmd
	if len(runner) == 0 {
		mcmd = exec.Command(mergedBin)
	} else {
		mcmd = exec.Command(runner[0], append(runner[1:], mergedBin)...)
	}
	_, _ = mcmd.CombinedOutput()
	if code := mcmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("bundled lexer+parser frontend returned %d, want 1 (one function parsed)", code)
	}
}
