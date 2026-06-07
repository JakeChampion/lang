package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSelfHostX86Capstone is the milestone of the native-binary track: it
// takes the AT&T assembly that the self-hosted compiler (asm.fern) emits
// for a real Fern program, feeds that text through the self-hosted GAS
// front-end (x86_gas.fern) + ELF writer (elf.fern), and runs the resulting
// binary NATIVELY on x86-64 — with no external `as` or `ld` anywhere.
//
// Stage A: build asm_run.fern (the source -> AT&T asm driver) via the Go
// toolchain and capture the asm for a small program.
// Stage B: build a Fern driver that embeds that asm, calls
// x86_gas_assemble, sets the ELF entry to the `_start` label's offset
// (asm.fern emits `__fn_main` before `_start`), and writes the ELF to
// stdout. Compile it through the self-host wasm pipeline and run under
// wasmtime to obtain the ELF bytes.
// Stage C: execute that ELF natively and assert the exit code.
func TestSelfHostX86Capstone(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 capstone")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	names := []string{"lexer.fern", "parser.fern", "asm.fern", "asm_run.fern", "wasm.fern", "wasm_run.fern"}
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// --- Stage A: asm.fern emits AT&T asm for our program. ---
	asmRun := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "asm_run")
	program := "function main(): i32 { var x: i32 = 40; var y: i32 = 2; return x + y; }\n"
	asmText := runCapture(t, gcc, runner, asmRun, []byte(program))
	if len(asmText) == 0 {
		t.Fatal("asm.fern produced no assembly")
	}

	// --- Stage B: the GAS front-end + ELF writer, in Fern. ---
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	enc := mustRead(t, "../../examples/self_host/x86_encode.fern")
	gas := mustRead(t, "../../examples/self_host/x86_gas.fern")
	elf := mustRead(t, "../../examples/self_host/elf.fern")

	// Embed the captured asm as a Fern string literal (escape \ and " then
	// turn newlines into \n; asm.fern's output otherwise has none of those).
	lit := string(asmText)
	lit = strings.ReplaceAll(lit, "\\", "\\\\")
	lit = strings.ReplaceAll(lit, "\"", "\\\"")
	lit = strings.ReplaceAll(lit, "\n", "\\n")

	driver := fmt.Sprintf(`
function main(): i32 {
    var src: string = "%s";
    var a: X86Asm = x86_gas_assemble(src);
    var entry: i32 = x86_label_off(a, "_start");
    var bin: i32[] = elf_static_executable_data_x86_at(a.code, a.rodata, entry);
    write(string_from_bytes(bin));
    return 0;
}
`, lit)

	source := string(enc) + "\n" + string(gas) + "\n" + string(elf) + "\n" + driver
	wat := runCapture(t, gcc, runner, wasmRun, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the capstone driver")
	}
	watPath := filepath.Join(dir, "capstone_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}
	if len(bin) < 4 || bin[0] != 0x7f || bin[1] != 'E' || bin[2] != 'L' || bin[3] != 'F' {
		t.Fatalf("output is not an ELF (bad magic): % x", bin[:min(4, len(bin))])
	}

	// --- Stage C: run the self-assembled binary natively. ---
	binPath := filepath.Join(dir, "capstone")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	got := 0
	if err := exec.Command(binPath).Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run failed (not an exit code): %v\n--- asm ---\n%s", err, asmText)
		}
		got = ee.ExitCode()
	}
	if got != 42 {
		t.Fatalf("exit code = %d, want 42\n--- asm ---\n%s", got, asmText)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
