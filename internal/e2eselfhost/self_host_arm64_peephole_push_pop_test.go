package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPeepholePushPopArm64 pins asm_arm64_ir.fern's
// peephole_push_pop_arm64, the twin of the x86-64 pass this file mirrors.
//
// Same postcondition as the x86-64 test, and for the same reason it is stated as
// a property of the whole emitted module rather than a golden sequence: no
// adjacent `str …, [sp, #-16]!` / `ldr …, [sp], #16` pair may survive, AND the
// program must still exit with the value the unfolded path produced. Either
// assertion alone is easy to satisfy wrongly — an emitter that dropped both
// lines outright would pass the first.
//
// Two hazards are specific to this backend and get their own subtests below:
// the `stp`/`ldp` frame saves, which push and pop register PAIRS through the
// same addressing modes and must never be folded, and darwinize's `ldr x8`
// syscall marker.
func TestSelfHostPeepholePushPopArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(driverBin, "-target", "arm64-linux")
		} else {
			args := append(append([]string{}, x86runner[1:]...), driverBin, "-target", "arm64-linux")
			cmd = exec.Command(x86runner[0], args...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return out
	}

	run := func(t *testing.T, name string, asm []byte) int {
		t.Helper()
		asmPath := filepath.Join(dir, name+".s")
		binPath := filepath.Join(dir, name)
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(arm64gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		inner := runArm64Bin(qemu, binPath)
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", name)
		}
		return inner.ProcessState.ExitCode()
	}

	// The same five shapes the x86-64 test uses, so a divergence between the two
	// backends shows up as one passing and the other not.
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"arith", "function main(): i32 { return 2 + 3 * 4 - 1; }", 13},
		{"locals", "function main(): i32 { var x = 7; var y = x * 3; return y + 1; }", 22},
		{"loop", "function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + i * 2; i = i + 1; } return s; }", 90},
		{"call", "function add3(a: i32, b: i32, c: i32): i32 { return a + b + c; } function main(): i32 { return add3(20, 15, 7); }", 42},
		{"divmod", "function main(): i32 { var a = 100; var b = 7; return a / b + a % b; }", 16},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := emit(t, tc.src)
			if n, first := adjacentStrLdrArm64(string(asm)); n != 0 {
				t.Errorf("%d adjacent str/ldr stack pair(s) survived the peephole; first at:\n%s", n, first)
			}
			if got := run(t, tc.name, asm); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}

	// The frame saves push and pop register PAIRS through the same `[sp, #-16]!`
	// / `[sp], #16` addressing. Folding one would corrupt every callee-saved
	// register, so the pass matches `str `/`ldr ` exactly and never sees them.
	t.Run("frame-saves-survive", func(t *testing.T) {
		asm := string(emit(t, "function f(a: i32): i32 { var b = a * 2; return b + 1; } function main(): i32 { return f(20); }"))
		if !strings.Contains(asm, "stp x29, x30, [sp, #-16]!") {
			t.Error("no stp frame save left — the pass ate a register-pair push")
		}
		if !strings.Contains(asm, "ldp x29, x30, [sp], #16") {
			t.Error("no ldp frame restore left — the pass ate a register-pair pop")
		}
	})

	// darwinize keys the Mach-O syscall rewrite off a literal
	// `ldr x8, [sp], #16`. Folding that line away would strip the marker
	// silently: the number register would stay x8 and the trap would stay
	// `svc #0` in a Mach-O binary. The pass refuses x8/x16 as fold destinations.
	t.Run("syscall-marker-survives", func(t *testing.T) {
		asm := string(emit(t, `function main(): i32 { return udp_send("127.0.0.1", 9999, "x"); }`))
		if !strings.Contains(asm, "ldr x8, [sp], #16") {
			t.Error("darwinize's syscall number marker was folded away")
		}
	})
}

// adjacentStrLdrArm64 counts stack pushes immediately followed by a stack pop,
// returning the count and the first offending pair for the failure message. The
// suffixes are matched exactly so the stp/ldp frame saves are never counted.
func adjacentStrLdrArm64(asm string) (int, string) {
	lines := strings.Split(asm, "\n")
	n := 0
	first := ""
	for i := 0; i+1 < len(lines); i++ {
		a := strings.TrimSpace(lines[i])
		b := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(a, "str ") && strings.HasSuffix(a, ", [sp, #-16]!") &&
			strings.HasPrefix(b, "ldr ") && strings.HasSuffix(b, ", [sp], #16") {
			n++
			if first == "" {
				first = "  " + a + "\n  " + b
			}
		}
	}
	return n, first
}
