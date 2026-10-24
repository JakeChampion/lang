package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArm64UnsignedDivIR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc_arm64")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	prog := "import \"std/test\";\n" +
		"@noinline function udiv_probe(a: u64, b: u64): u64 { return a / b; }\n" +
		"@noinline function umod_probe(a: u64, b: u64): u64 { return a % b; }\n" +
		"@noinline function sdiv_probe(a: i32, b: i32): i32 { return a / b; }\n" +
		"function main(): i32 {\n" +
		"    let q: u64 = udiv_probe(10 as u64, 3 as u64);\n" +
		"    let m: u64 = umod_probe(10 as u64, 3 as u64);\n" +
		"    let s: i32 = sdiv_probe(9, 2);\n" +
		"    return (q as i32) + (m as i32) + s;\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "u64_div_ast.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, stdlibRoot, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)
	if len(asm) == 0 {
		t.Fatal("self-host emitted 0 bytes")
	}

	udivBody := arm64FnBody(t, asm, "__fn_udiv_probe")
	if !matchShape(udivBody, `udiv x[0-9]+, x[0-9]+, x[0-9]+`) {
		t.Errorf("u64 `/` did not emit udiv (the umax-prints-\"/\" bug); body:\n%s", udivBody)
	}
	if matchShape(udivBody, `sdiv x[0-9]+, x[0-9]+, x[0-9]+`) {
		t.Errorf("u64 `/` still emits signed sdiv; body:\n%s", udivBody)
	}

	umodBody := arm64FnBody(t, asm, "__fn_umod_probe")
	if !matchShape(umodBody, `udiv x[0-9]+, x[0-9]+, x[0-9]+`) {
		t.Errorf("u64 `%%` did not emit udiv; body:\n%s", umodBody)
	}

	sdivBody := arm64FnBody(t, asm, "__fn_sdiv_probe")
	if !matchShape(sdivBody, `sdiv x[0-9]+, x[0-9]+, x[0-9]+`) {
		t.Errorf("i32 `/` should still emit sdiv; body:\n%s", sdivBody)
	}
	if matchShape(sdivBody, `udiv x[0-9]+, x[0-9]+, x[0-9]+`) {
		t.Errorf("i32 `/` wrongly emits unsigned udiv; body:\n%s", sdivBody)
	}
}

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
		if strings.HasSuffix(trimmed, ":") && strings.HasPrefix(trimmed, "__fn_") && trimmed != label+".r:" && trimmed != label+".k:" {
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
