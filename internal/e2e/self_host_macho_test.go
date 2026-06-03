package e2e

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64DarwinBuilds exercises the self-hosted compiler's
// arm64-darwin (Mach-O) target — examples/self_host/fern.fern's
// `-target arm64-darwin`, backed by asm_arm64.darwinize. The driver is
// built with the Go backend (so it runs on the x86-64 test host); its
// OUTPUT is arm64-apple-darwin assembly, which we cross-link with
// clang + lld's Mach-O backend and assert is a well-formed arm64 Mach-O
// executable.
//
// This is the structural gate, the sibling of the Go backend's
// TestArm64DarwinBuilds off Apple Silicon: qemu-aarch64 only speaks the
// Linux ABI, so we can't execute the Mach-O here. We don't need to —
// the self-host arm64 emitter is byte-for-byte parity-tested against the
// Go arm64 backend (TestSelfHostStage2FixedPointArm64), and darwinize()
// only reskins the assembler dialect (@PAGE/@PAGEOFF addressing, Mach-O
// sections, _main entry) and remaps the number-compatible syscalls
// (read/write/close/openat/lseek/exit/mmap) to the BSD vector with
// `svc #0x80` — the same transforms the Go backend applies inline. The
// macOS arm64 runtime of this exact instruction shape is therefore
// covered transitively by TestArm64DarwinBuilds, which executes on the
// macOS arm64 CI runner.
//
// Supported surface is the core language; the ABI-divergent syscalls
// (clock_gettime/getrandom/fstat/subprocess) have no number-only Darwin
// form and are out of scope — see the darwinize doc comment.
func TestSelfHostArm64DarwinBuilds(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The driver takes host filesystem paths as argv; a qemu
		// runner wouldn't see them. Native-host-only, like the CLI
		// driver test.
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH; skipping arm64-darwin self-host e2e")
	}
	// Cross-compiling Mach-O from a Linux host needs lld's Mach-O
	// backend (the host clang defaults to ELF).
	if _, err := exec.LookPath("ld.lld"); err != nil {
		t.Skip("lld not on PATH; skipping arm64-darwin self-host e2e")
	}

	// Stage the full self-host project (lexer/parser/asm via the helper,
	// plus the rest of the modules fern.fern imports) and build the CLI.
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "asm_arm64.fern", "wasm.fern", "checker.fern", "interp.fern", "printer.fern", "fern.fern"} {
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
		name string
		src  string
	}{
		// Plain integer return — exercises only the exit syscall.
		{"exit_42", `function main(): i32 { return 42; }`},
		// Arithmetic — register ops, no runtime.
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`},
		// Control flow + recursion.
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`},
		// String concat — exercises the heap (.bss bump allocator) +
		// the @PAGE/@PAGEOFF addressing of a runtime-built string.
		{"concat", `function main(): i32 { var s: string = "hello, " + "world!"; return s.len(); }`},
		// Stdout — print lowers to the write syscall (64 -> 4) and a
		// .rodata (__TEXT,__const) string literal.
		{"print", `function main(): i32 { print("hi"); return 0; }`},
		// Struct + receiver method dispatch.
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }`},
		// Arrays — literal, index, length, loop.
		{"array_sum", `function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`},
		// Option payload + match — exercises the enum-box runtime.
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			asmPath := filepath.Join(dir, c.name+".s")
			if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", asmPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host emit failed: %v\n%s", err, out)
			}

			binPath := filepath.Join(dir, c.name+".bin")
			args := []string{
				"--target=arm64-apple-darwin",
				"-fuse-ld=lld",
				"-nostdlib",
				"-Wl,-arch,arm64",
				asmPath,
				"-o", binPath,
			}
			if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
				t.Fatalf("clang Mach-O link failed: %v\n%s", err, out)
			}

			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := macho.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("output is not a parseable Mach-O: %v", err)
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}
		})
	}
}
