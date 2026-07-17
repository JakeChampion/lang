package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArm64UnsignedDivAST pins the arm64 AST backend's signed-vs-
// unsigned division selection. asm_arm64.fern's ExprBinary emitter used to
// emit a bare `sdiv` for every `/` and `%`, regardless of operand type — so a
// u64 value with bit 63 set (e.g. umax() = (0 as u64) - (1 as u64)) divided as
// a negative i64: 18446744073709551615 / 10 gave 0 instead of the unsigned
// quotient, and `__int_to_string_u64`'s digit loop printed a single "/"
// (= '0' + (-1 % 10)). The x86 AST backend (asm.fern emit_divmod) and the
// arm64 IR path (ir_bin_asm div_u) already picked udiv from the operand tags;
// this is the arm64 AST sibling.
//
// The bug is AST-path-only: a small program routes the IR path (which already
// emitted udiv), so it surfaces only once a program is large enough to bail to
// the AST emitter — which is why importing std/test (dragging the whole runner
// over the IR budget) is what exposed it in the u64 std-test. This test forces
// the AST path the same way and asserts the emitted aarch64 for a u64 `/` / `%`
// is `udiv`, and that a signed i32 `/` still uses `sdiv`. It is a pure emission
// check (the driver runs on the x86 host and prints aarch64 asm), so it needs
// no qemu and runs on every x86 CI lane.
func TestSelfHostArm64UnsignedDivAST(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "treeshake.fern", "asm.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// The `import "std/test"` drags the merged module over the IR budget, so
	// every function here lowers on the AST arm64 backend — the path the fix
	// touches. The u64 helpers give named symbols to anchor the asm scan; the
	// i32 helper is the signed control.
	// main references all three so the default stdlib-root treeshake prune
	// keeps them reachable (an unreferenced helper is pruned before codegen).
	prog := "import \"std/test\";\n" +
		"function udiv_probe(a: u64, b: u64): u64 { return a / b; }\n" +
		"function umod_probe(a: u64, b: u64): u64 { return a % b; }\n" +
		"function sdiv_probe(a: i32, b: i32): i32 { return a / b; }\n" +
		"function main(): i32 {\n" +
		"    var q: u64 = udiv_probe(10 as u64, 3 as u64);\n" +
		"    var m: u64 = umod_probe(10 as u64, 3 as u64);\n" +
		"    var s: i32 = sdiv_probe(9, 2);\n" +
		"    return (q as i32) + (m as i32) + s;\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "u64_div_ast.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, stdlibRoot, "-target", "arm64").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)
	if len(asm) == 0 {
		t.Fatal("self-host emitted 0 bytes")
	}

	udivBody := arm64FnBody(t, asm, "__fn_udiv_probe")
	if !strings.Contains(udivBody, "udiv x0, x0, x1") {
		t.Errorf("u64 `/` did not emit udiv (the umax-prints-\"/\" bug); body:\n%s", udivBody)
	}
	if strings.Contains(udivBody, "sdiv x0, x0, x1") {
		t.Errorf("u64 `/` still emits signed sdiv; body:\n%s", udivBody)
	}

	umodBody := arm64FnBody(t, asm, "__fn_umod_probe")
	if !strings.Contains(umodBody, "udiv x2, x0, x1") {
		t.Errorf("u64 `%%` did not emit udiv; body:\n%s", umodBody)
	}

	// Signed control: an i32 `/` must still take the signed sdiv path.
	sdivBody := arm64FnBody(t, asm, "__fn_sdiv_probe")
	if !strings.Contains(sdivBody, "sdiv x0, x0, x1") {
		t.Errorf("i32 `/` should still emit sdiv; body:\n%s", sdivBody)
	}
	if strings.Contains(sdivBody, "udiv x0, x0, x1") {
		t.Errorf("i32 `/` wrongly emits unsigned udiv; body:\n%s", sdivBody)
	}
}

// arm64FnBody returns the emitted lines of the function labelled `label` (up to
// the next `__fn_` label or `.weak` directive) so an assertion scans one
// function's body rather than the whole module.
func arm64FnBody(t *testing.T, asm, label string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == label+":" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("function %s not found in emitted asm", label)
	}
	var b strings.Builder
	for i := start + 1; i < len(lines); i++ {
		ln := lines[i]
		trimmed := strings.TrimSpace(ln)
		if strings.HasSuffix(trimmed, ":") && strings.HasPrefix(trimmed, "__fn_") {
			break
		}
		if strings.HasPrefix(trimmed, ".weak") || strings.HasPrefix(trimmed, ".globl") {
			break
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	return b.String()
}
