package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostIRCheckGate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, src string, env ...string) ([]byte, []byte, int) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		cmd.Env = append(os.Environ(), env...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, _ := cmd.Output()
		return out, stderr.Bytes(), cmd.ProcessState.ExitCode()
	}

	rejects := []struct {
		name string
		src  string
	}{
		{"option-as-i32-return", "function find(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; }\nfunction main(): i32 { return find(3); }"},
		{"option-as-i32-var", "function find(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; }\nfunction main(): i32 { let x: i32 = find(3); return 0; }"},
		{"option-as-i32-assign", "function find(i: i32): Option[i32] { if (i > 0) { return Some(i); } return None; }\nfunction main(): i32 { let x = 1; x = find(3); return 0; }"},
		{"mapnew-chain-num-key", "function main(): i32 { let mm: Map[i32, i32] = map_new(8).insert(1, 10); return mm.len(); }"},
		{"mapnew-i32-chain-str-key", `function main(): i32 { let mm: Map[string, i32] = __map_new_i32(8).insert("a", 1); return mm.len(); }`},
		{"mapnew-chain-num-key-deep", "function main(): i32 { let mm: Map[i32, i32] = map_new(8).insert(1, 10).insert(2, 20); return mm.len(); }"},
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
		_, errOut, _ := run(t, `function main(): i32 { let m: Map[i32, i32] = Map { 1: 40, 2: 2 }; return m.get_or(1, 0) + m.get_or(2, 0); }`)
		if strings.Contains(string(errOut), "error[") {
			t.Fatalf("stderr = %q — Map literal desugar false-positived the chain gate", errOut)
		}
	})

	t.Run("accept-still-ir-routed", func(t *testing.T) {
		out, errOut, code := run(t, "function main(): i32 { let a = [40, 2]; return a[0] + a[1]; }")
		if code != 0 {
			t.Fatalf("driver exited %d (stderr %q), want 0", code, errOut)
		}
		if !bytes.Contains(out, []byte(".Lssa_")) {
			t.Fatal("emitted asm has no .Lssa_ label — valid module no longer emits through the register path")
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

func TestSelfHostIRCheckGateRunsOnce(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte("function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(2, 3); }"))
	cmd.Env = append(os.Environ(), "FERN_ENTRY_CHECK_TRACE=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("asm_run: %v\n%s", err, stderr.String())
	}
	if n := strings.Count(stderr.String(), "entry check\n"); n != 1 {
		t.Fatalf("the entry checks ran %d times, want 1\n%s", n, stderr.String())
	}
}
