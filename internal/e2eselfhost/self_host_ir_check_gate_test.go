package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRCheckGate pins #4327: the x86-64 driver's IR fast-path used to
// return emitted assembly BEFORE the asmcore.check_module abort ran, so every
// IR-eligible module skipped type checking entirely — type-invalid programs
// compiled cleanly into wrong binaries. The gate now runs ahead of the IR
// branch (mirroring the arm64 driver's ordering): ill-typed input exits 1
// with a diagnostic and emits nothing, and valid input still routes through
// the IR fast-path (the .Lir label marker).
func TestSelfHostIRCheckGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, src string) ([]byte, []byte, int) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, _ := cmd.Output()
		return out, stderr.Bytes(), cmd.ProcessState.ExitCode()
	}

	rejects := []struct {
		name string
		src  string
	}{
		// The issue's repro: an Option-returning call in i32 return position.
		// IR-eligible, so pre-fix it skipped the check and the binary exited
		// with an Option-box pointer fragment.
		{"option-as-i32-return", "function find(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; }\nfunction main(): i32 { return find(3); }"},
		// Annotated-var mismatch (native E003) — same silent-compile class.
		{"option-as-i32-var", "function find(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; }\nfunction main(): i32 { var x: i32 = find(3); return 0; }"},
		// Assignment mismatch (native E003; the asmcore guard's code was
		// renumbered E004 -> E003 in this fix).
		{"option-as-i32-assign", "function find(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; }\nfunction main(): i32 { var x = 1; x = find(3); return 0; }"},
		// Bare-map_new chain with a key-kind mismatch (native E038/E003 class):
		// a NUMBER key dispatched through the string-keyed map_new() chain
		// (and the map_new_i32 mirror) used to sail through check and
		// miscompile — the emitted binary SIGSEGV'd. check_map_new_chain now
		// rejects both directions; a kind-CONSISTENT chain (the Map{…} literal
		// desugar) stays accepted — see the accept case below.
		{"mapnew-chain-num-key", "function main(): i32 { var mm: Map[i32, i32] = map_new(8).insert(1, 10); return mm.len(); }"},
		{"mapnew-i32-chain-str-key", `function main(): i32 { var mm: Map[string, i32] = map_new_i32(8).insert("a", 1); return mm.len(); }`},
		{"mapnew-chain-num-key-deep", "function main(): i32 { var mm: Map[i32, i32] = map_new(8).insert(1, 10).insert(2, 20); return mm.len(); }"},
	}
	for _, tc := range rejects {
		t.Run("reject-"+tc.name, func(t *testing.T) {
			out, errOut, code := run(t, tc.src)
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			if !strings.Contains(string(errOut), "error[") {
				t.Errorf("stderr = %q, want a coded diagnostic", errOut)
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for an ill-typed program, want 0", len(out))
			}
		})
	}

	t.Run("accept-map-literal-desugar", func(t *testing.T) {
		// The Map{…} literal desugars to the SAME chain shape with a
		// kind-consistent ctor (map_new_i32 for number keys) — it must stay
		// accepted and correct (the chain gate flags only mismatched kinds).
		out, errOut, code := run(t, `function main(): i32 { var m: Map[i32, i32] = Map { 1: 40, 2: 2 }; return m.get_or(1, 0) + m.get_or(2, 0); }`)
		if code != 0 {
			t.Fatalf("driver exited %d (stderr %q), want 0 — Map literal desugar false-positived the chain gate", code, errOut)
		}
		progBin := buildBin(t, gcc, dir, "gate_maplit", string(out))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("Map-literal program exited %d, want 42", c)
		}
	})

	t.Run("accept-still-ir-routed", func(t *testing.T) {
		out, errOut, code := run(t, "function main(): i32 { var a = [40, 2]; return a[0] + a[1]; }")
		if code != 0 {
			t.Fatalf("driver exited %d (stderr %q), want 0", code, errOut)
		}
		// `.Lir` is the x86 IR emitter's label prefix; its presence proves the
		// module still takes the IR fast-path with the gate ahead of it.
		if !bytes.Contains(out, []byte(".Lir")) {
			t.Fatal("emitted asm has no .Lir marker — valid module no longer routes through the IR path")
		}
		progBin := buildBin(t, gcc, dir, "gate_ok", string(out))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("valid program exited %d, want 42", c)
		}
	})
}
