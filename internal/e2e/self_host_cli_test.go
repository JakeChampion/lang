package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostCLIX86_64 exercises examples/self_host/fern.fern — the
// unified self-hosted `fern` CLI driver. Unlike the single-mode
// `*_run.fern` shims (asm_load_run = emit, checker_run = check), this is
// ONE binary that parses argv flags and dispatches mode + output. The
// test builds it with the Go backend, then drives each mode:
//
//   - default            emit x86-64 asm to stdout; assemble + run.
//   - `-o OUT`           emit to a file; assemble that file + run.
//   - `-check` (ok)      well-typed program exits 0.
//   - `-check` (bad)     ill-typed program exits 1.
//   - usage / errors     no-arg → 2, unknown flag → 2, missing file → 1.
//
// It is the first parity slice toward retiring cmd/fern: the
// flag-dispatch seam the -target / -interp / -fmt modes plug into next.
func TestSelfHostCLIX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The driver takes host filesystem paths as argv; a qemu runner
		// wouldn't see the same paths. Native-only, like the file driver.
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t) // lexer.fern, parser.fern, asm.fern
	for _, name := range []string{"flatten.fern", "asm_arm64.fern", "wasm.fern", "checker.fern", "interp.fern", "printer.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern", "fern.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Build the CLI driver with the Go backend.
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// runDriver runs the CLI with args and returns (stdout, exitcode).
	runDriver := func(t *testing.T, args ...string) ([]byte, int) {
		t.Helper()
		cmd := exec.Command(fernBin, args...)
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	t.Run("emit-stdout", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret42.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, srcPath)
		if code != 0 {
			t.Fatalf("emit exited %d, want 0", code)
		}
		if len(asm) == 0 {
			t.Fatal("emit produced 0 bytes of asm")
		}
		progBin := buildBin(t, gcc, dir, "ret42", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-to-file", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret7.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { var x = 3; var y = 4; return x + y; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		outPath := filepath.Join(dir, "ret7.s")
		stdout, code := runDriver(t, "-o", outPath, srcPath)
		if code != 0 {
			t.Fatalf("`-o` emit exited %d, want 0", code)
		}
		if len(stdout) != 0 {
			t.Errorf("`-o` emit wrote %d bytes to stdout, want 0 (asm goes to the file)", len(stdout))
		}
		asm, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read -o output: %v", err)
		}
		if len(asm) == 0 {
			t.Fatal("`-o` output file is empty")
		}
		progBin := buildBin(t, gcc, dir, "ret7", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 7 {
			t.Errorf("file-emitted program exited %d, want 7", c)
		}
	})

	t.Run("emit-ssa", func(t *testing.T) {
		// -ssa routes an in-subset program through AST → SSA → optimise →
		// regalloc → emit. Assemble + run; exit code is the program's value.
		srcPath := filepath.Join(dir, "ssa_prog.fern")
		src := "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } " +
			"function main(): i32 { var s = 0; var i = 0; while (i < 10) { s = s + fib(i); i = i + 1; } return s; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("-ssa emit exited %d, want 0", code)
		}
		progBin := buildBin(t, gcc, dir, "ssa_prog", string(asm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 88 {
			t.Errorf("-ssa emitted program exited %d, want 88 (sum of fib(0..9))", c)
		}
	})

	t.Run("emit-ssa-array", func(t *testing.T) {
		// An array program is now in the SSA subset on x86-64: -ssa compiles
		// it through the heap-aware backend (not a fallback). Run it.
		srcPath := filepath.Join(dir, "ssa_arr.fern")
		src := "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }\n"
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		ssaAsm, code := runDriver(t, "-ssa", srcPath)
		if code != 0 {
			t.Fatalf("-ssa emit exited %d, want 0", code)
		}
		astAsm, _ := runDriver(t, srcPath)
		if string(ssaAsm) == string(astAsm) {
			t.Error("-ssa fell back to AST for an array program (expected the SSA heap backend)")
		}
		progBin := buildBin(t, gcc, dir, "ssa_arr", string(ssaAsm))
		cmd := exec.Command(progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 75 {
			t.Errorf("-ssa array program exited %d, want 75", c)
		}
	})

	t.Run("ssa-fallback", func(t *testing.T) {
		// A program outside the SSA subset (a float local) must fall back to
		// the AST emitter: -ssa output is byte-identical to the default.
		srcPath := filepath.Join(dir, "fallback.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { var x = 1.5; return 5; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		withSSA, code1 := runDriver(t, "-ssa", srcPath)
		astOnly, code2 := runDriver(t, srcPath)
		if code1 != 0 || code2 != 0 {
			t.Fatalf("emit exited %d / %d, want 0", code1, code2)
		}
		if string(withSSA) != string(astOnly) {
			t.Errorf("-ssa did not fall back cleanly for an out-of-subset program (output differs from AST)")
		}
	})

	t.Run("check-ok", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ok.fern")
		if err := os.WriteFile(srcPath, []byte("function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(2, 3); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-check", srcPath)
		if code != 0 {
			t.Errorf("-check on well-typed program exited %d, want 0", code)
		}
	})

	t.Run("check-bad", func(t *testing.T) {
		// Arity mismatch — the self-host checker rejects it (mirrors
		// checker.fern's own mt6 self-test).
		srcPath := filepath.Join(dir, "bad.fern")
		if err := os.WriteFile(srcPath, []byte("function add(x: i32, y: i32): i32 { return x + y; } function main(): i32 { return add(1); }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-check", srcPath)
		if code != 1 {
			t.Errorf("-check on ill-typed program exited %d, want 1", code)
		}
	})

	t.Run("interp", func(t *testing.T) {
		// -interp evaluates via the tree-walker; the program's i32
		// result becomes the exit code (mirrors interp_run.fern).
		srcPath := filepath.Join(dir, "interp_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { var x = 5; var y = 8; return x * y - 1; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		_, code := runDriver(t, "-interp", srcPath)
		if code != 39 {
			t.Errorf("-interp program exited %d, want 39", code)
		}
	})

	t.Run("fmt", func(t *testing.T) {
		// -fmt formats the entry file to stdout. We assert it produces
		// non-empty output and is IDEMPOTENT (formatting the formatted
		// output is a fixed point) — validating the formatter is wired
		// + stable without pinning its exact style.
		srcPath := filepath.Join(dir, "messy.fern")
		messy := "function   main( ):i32{var x=1;var y=2;return x+y;}\n"
		if err := os.WriteFile(srcPath, []byte(messy), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out1, code := runDriver(t, "-fmt", srcPath)
		if code != 0 {
			t.Fatalf("-fmt exited %d, want 0", code)
		}
		if len(out1) == 0 {
			t.Fatal("-fmt produced empty output")
		}
		// Re-format the formatted output; must be a fixed point.
		src2Path := filepath.Join(dir, "formatted.fern")
		if err := os.WriteFile(src2Path, out1, 0o644); err != nil {
			t.Fatalf("write formatted: %v", err)
		}
		out2, code2 := runDriver(t, "-fmt", src2Path)
		if code2 != 0 {
			t.Fatalf("second -fmt exited %d, want 0", code2)
		}
		if string(out1) != string(out2) {
			t.Errorf("-fmt is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
		}
		// And the formatted output must still be well-typed.
		_, checkCode := runDriver(t, "-check", src2Path)
		if checkCode != 0 {
			t.Errorf("formatted output failed -check (exit %d)", checkCode)
		}
	})

	t.Run("emit-target-arm64", func(t *testing.T) {
		// The driver (x86-64 host binary) emits arm64 asm under
		// -target arm64; cross-assemble it and run under qemu.
		arm64gcc, qemu := arm64Tooling(t) // skips if cross toolchain absent
		srcPath := filepath.Join(dir, "arm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		asm, code := runDriver(t, "-target", "arm64", srcPath)
		if code != 0 {
			t.Fatalf("-target arm64 emit exited %d, want 0", code)
		}
		if len(asm) == 0 {
			t.Fatal("-target arm64 produced 0 bytes of asm")
		}
		progBin := buildBinArm64(t, arm64gcc, dir, "arm_prog", string(asm))
		cmd := exec.Command(qemu, progBin)
		_ = cmd.Run()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("arm64-emitted program exited %d, want 42", c)
		}
	})

	t.Run("emit-target-wasm", func(t *testing.T) {
		// The driver emits a WASI WAT module under -target wasm; run it
		// directly with wasmtime and check exit code + stdout.
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH; skipping -target wasm")
		}
		srcPath := filepath.Join(dir, "wasm_prog.fern")
		if err := os.WriteFile(srcPath, []byte("function main(): i32 { print(\"hi from wasm\"); return 6 * 7; }\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		wat, code := runDriver(t, "-target", "wasm", srcPath)
		if code != 0 {
			t.Fatalf("-target wasm emit exited %d, want 0", code)
		}
		if len(wat) == 0 {
			t.Fatal("-target wasm produced 0 bytes")
		}
		watPath := filepath.Join(dir, "wasm_prog.wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", watPath)
		out, _ := cmd.Output()
		if c := cmd.ProcessState.ExitCode(); c != 42 {
			t.Errorf("wasm-emitted program exited %d, want 42\n--- WAT ---\n%s", c, wat)
		}
		if string(out) != "hi from wasm\n" {
			t.Errorf("wasm-emitted program stdout = %q, want %q", string(out), "hi from wasm\n")
		}
	})

	t.Run("unknown-target-exits-2", func(t *testing.T) {
		srcPath := filepath.Join(dir, "ret42.fern")
		_, code := runDriver(t, "-target", "riscv", srcPath)
		if code != 2 {
			t.Errorf("unknown -target exited %d, want 2", code)
		}
	})

	t.Run("no-arg-exits-2", func(t *testing.T) {
		_, code := runDriver(t)
		if code != 2 {
			t.Errorf("no-arg driver exited %d, want 2", code)
		}
	})

	t.Run("unknown-flag-exits-2", func(t *testing.T) {
		_, code := runDriver(t, "-nope", filepath.Join(dir, "ret42.fern"))
		if code != 2 {
			t.Errorf("unknown-flag driver exited %d, want 2", code)
		}
	})

	t.Run("missing-file-exits-1", func(t *testing.T) {
		_, code := runDriver(t, filepath.Join(dir, "does-not-exist.fern"))
		if code != 1 {
			t.Errorf("missing-file driver exited %d, want 1", code)
		}
	})
}
