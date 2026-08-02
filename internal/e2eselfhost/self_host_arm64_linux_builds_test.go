package e2eselfhost

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64LinuxBuilds is the arm64-Linux flagship: the self-host CLI
// (examples/self_host/fern.fern, `-target arm64`) now emits a runnable static
// ELF **in-process** — asm_arm64 / ssa_arm64 produce the GAS text and
// arm64_native + elf.fern assemble + link it, with no `.s` + gcc/ld step (the
// flip, mirroring arm64-darwin). Unlike the darwin path (which can only be
// executed on a macOS runner), the arm64-Linux output runs on the Linux CI
// box under qemu-aarch64 — so this EXECUTES each program and checks its exit
// code, the decisive end-to-end check of the flipped path.
//
// The CLI is built with the Go x86-64 backend so it runs on the host; it then
// emits arm64 ELF binaries straight to `-o`, which qemu-aarch64 runs.
func TestSelfHostArm64LinuxBuilds(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-linux self-host e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asmcore.fern", "util.fern", "flatten.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "checker.fern", "interp.fern", "printer.fern", "astwalk.fern", "ssa.fern", "ssa_arm64.fern", "ssa_x86.fern", "ssa_wasm.fern", "watbin.fern", "constfold.fern", "arm64_native.fern", "elf.fern", "fern.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"exit_42", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`, 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55},
		{"loop_sum", `function main(): i32 { var s: i32 = 0; var i: i32 = 1; while (i <= 10) { s = s + i; i = i + 1; } return s; }`, 55},
		{"concat", `function main(): i32 { var s: string = "hello, " + "world!"; return s.len(); }`, 13},
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0},
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }`, 12},
		{"array_sum", `function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`, 42},
		{"enum", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r*r*3; }, Square(w) => { return w*w; } } } function main(): i32 { return area(Circle(2)) + area(Square(3)); }`, 21},
		{"floats", `function main(): i32 { var x: f64 = 3.5; var y: f64 = 2.0; var z: f64 = x*y + x/y - x; if (z > 5.0) { return 7; } return 1; }`, 7},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, c.name+".bin")
			if out, err := exec.Command(fernBin, "-target", "arm64", "-o", binPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host emit (arm64 ELF) failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := elf.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("output is not a parseable ELF: %v", err)
			}
			if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
				t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
			}
			if err := os.Chmod(binPath, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			cmd := exec.Command("qemu-aarch64", binPath)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != c.wantExit {
				t.Errorf("self-host arm64 %q exit = %d, want %d", c.name, code, c.wantExit)
			}
		})
	}
}
