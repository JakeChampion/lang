package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRPerModuleLinkStrings exercises per-module `.rodata` namespacing
// (#3451 step 3 / #3455): two separately-emitted modules that BOTH contain
// string literals. Each module's string pool labels its entries `.S<idx>`
// starting from 0, so without namespacing both would emit `.S0:` into `.rodata`
// and collide (duplicate symbol) when the two units link. asm_ir_run `-ir-ns
// <name>` sets emit_module_ir_unit's str_ns, making the labels `.S<name>_<idx>`
// — per module, collision-free.
//
// Entry A ("entrypoint", len 10) calls library B's blen ("lib", len 3); the
// linked binary exits 10+3 = 13. Both modules allocate their string box via
// __fern_alloc; only the entry emits the shared runtime, so B's __fern_alloc is
// an extern resolved against the entry's copy at link.
func TestSelfHostIRPerModuleLinkStrings(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	emit := func(t *testing.T, prog string, args ...string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit failed (args %v) for %q: %v", args, prog, err)
		}
		return string(out)
	}

	// Library B: defines blen, with its own string literal "lib".
	libAsm := emit(t, "function blen(): i32 { var s = \"lib\"; return s.len(); }", "-ir-unit", "lib", "-ir-ns", "b")
	// Entry A: its own string literal "entrypoint" + a call into B's blen.
	entryAsm := emit(t, "function main(): i32 { var s = \"entrypoint\"; return s.len() + blen(); }", "-ir-unit", "entry", "-ir-ns", "a", "-ir-extern", "blen")

	// Each module's string pool is namespaced — no bare `.S0` collision.
	if !strings.Contains(entryAsm, ".Sa_0:") {
		t.Fatalf("entry string label not namespaced (want .Sa_0:)\n--- entry.s rodata ---\n%s", rodataOf(entryAsm))
	}
	if !strings.Contains(libAsm, ".Sb_0:") {
		t.Fatalf("lib string label not namespaced (want .Sb_0:)\n--- lib.s rodata ---\n%s", rodataOf(libAsm))
	}
	// The two pools must NOT share a label — that is exactly the collision
	// namespacing prevents.
	if strings.Contains(entryAsm, ".Sb_0:") || strings.Contains(libAsm, ".Sa_0:") {
		t.Fatalf("string labels leaked across modules\n--- entry ---\n%s\n--- lib ---\n%s", rodataOf(entryAsm), rodataOf(libAsm))
	}

	entryPath := filepath.Join(dir, "pms_entry.s")
	libPath := filepath.Join(dir, "pms_lib.s")
	binPath := filepath.Join(dir, "pms_prog")
	if err := os.WriteFile(entryPath, []byte(entryAsm), 0o644); err != nil {
		t.Fatalf("write entry.s: %v", err)
	}
	if err := os.WriteFile(libPath, []byte(libAsm), 0o644); err != nil {
		t.Fatalf("write lib.s: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", entryPath, libPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("link two string-using units failed: %v\n%s", err, out)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 13 {
		t.Errorf("linked per-module string binary exit = %d, want 13 (10+3)", code)
	}
}

// rodataOf returns the lines of asm at/after the first `.rodata` directive, for
// compact failure output.
func rodataOf(asm string) string {
	i := strings.Index(asm, ".rodata")
	if i < 0 {
		return "(no .rodata section)"
	}
	return asm[i:]
}
