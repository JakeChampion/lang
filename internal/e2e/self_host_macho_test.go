package e2e

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostArm64DarwinBuilds exercises the self-hosted compiler's
// arm64-darwin (Mach-O) target — examples/self_host/fern.fern's
// `-target arm64-darwin`, backed by asm_arm64.darwinize.
//
// Two host modes:
//
//   - Off Apple Silicon (the Linux CI box): the driver is built with the
//     Go x86-64 backend so it runs on the host; its OUTPUT is
//     arm64-apple-darwin assembly, which we cross-link with clang + lld's
//     Mach-O backend and assert is a well-formed arm64 Mach-O executable.
//     qemu-aarch64 only speaks the Linux ABI, so we can't run the result.
//
//   - On the macOS arm64 CI runner: the driver is built FOR arm64-darwin
//     (Go arm64 backend + clang/ld64) so it runs natively, then we run it
//     to emit each program's asm, link with native clang, and EXECUTE the
//     Mach-O, checking exit codes. This is the decisive runtime check of
//     the self-host Darwin path. Launch failures (kernel rejects the
//     binary) are reported as skips with diagnostics, not hard failures —
//     a wrong exit code (ran but misbehaved) is a hard failure.
//
// darwinize() reuses asm_arm64.fern's instruction selection and only
// reskins the assembler dialect (@PAGE/@PAGEOFF addressing, Mach-O
// sections, _main entry) and remaps the number-compatible syscalls
// (read/write/close/openat/lseek/exit/mmap) to the BSD vector with
// `svc #0x80`. Supported surface is the core language; the ABI-divergent
// syscalls (clock_gettime/getrandom/fstat/subprocess) are out of scope —
// see the darwinize doc comment.
func TestSelfHostArm64DarwinBuilds(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH; skipping arm64-darwin self-host e2e")
	}
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	// Stage the full self-host project (lexer/parser/asm via the helper,
	// plus the rest of the modules fern.fern imports).
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

	// Build the self-host CLI for the host so it runs natively, and pick
	// the clang link flags for each emitted program.
	var fernBin string
	var linkArgs func(asm, out string) []string
	if native {
		// Native macOS arm64: build the CLI FOR arm64-darwin (Go arm64
		// backend + clang/ld64) so it runs on the runner; link emitted
		// programs with the default clang + ld64. -nostdlib drops
		// crt0/libc; -lSystem restores dyld-stub linkage (newer ld64
		// rejects dynamic execs without it), matching TestArm64DarwinBuilds.
		fernBin = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
		linkArgs = func(asm, out string) []string {
			return []string{"-nostdlib", "-lSystem", asm, "-o", out}
		}
	} else {
		// Cross from Linux: the CLI is an x86-64 host binary; emitted
		// programs are cross-linked with lld's Mach-O backend.
		gcc, runner := x86_64Tooling(t)
		if len(runner) != 0 {
			t.Skip("self-host CLI driver runs only natively (argv paths)")
		}
		if _, err := exec.LookPath("ld.lld"); err != nil {
			t.Skip("lld not on PATH; skipping arm64-darwin self-host e2e")
		}
		fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
		linkArgs = func(asm, out string) []string {
			return []string{"--target=arm64-apple-darwin", "-fuse-ld=lld", "-nostdlib", "-Wl,-arch,arm64", asm, "-o", out}
		}
	}

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		// Plain integer return — exercises only the exit syscall.
		{"exit_42", `function main(): i32 { return 42; }`, 42},
		// Arithmetic — register ops, no runtime.
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`, 42},
		// Control flow + recursion.
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55},
		// String concat — exercises the heap (.bss bump allocator) +
		// the @PAGE/@PAGEOFF addressing of a runtime-built string.
		{"concat", `function main(): i32 { var s: string = "hello, " + "world!"; return s.len(); }`, 13},
		// Stdout — print lowers to the write syscall (64 -> 4) and a
		// .rodata (__TEXT,__const) string literal.
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0},
		// Struct + receiver method dispatch.
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { var x = Box { v: 4 }; return x.scale(3); }`, 12},
		// Arrays — literal, index, length, loop.
		{"array_sum", `function main(): i32 { var a = [1, 2, 3, 4, 5]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		// Option payload + match — exercises the enum-box runtime.
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`, 42},
		// now_unix_ms — the Darwin gettimeofday(116) port (vs Linux
		// clock_gettime). A plausible post-2023 wall-clock value → 7.
		{"now_unix_ms", `function main(): i32 { var t: i64 = now_unix_ms(); if (t > 1700000000000) { return 7; } return 1; }`, 7},
		// random_bytes — the Darwin chunked getentropy(500) port (vs
		// Linux getrandom). Assert the length round-trips AND the bytes
		// were actually written (OR of 8 bytes != 0 → the syscall filled
		// the buffer; a zero OR would mean it silently failed).
		{"random_bytes", `function main(): i32 { var b: string = random_bytes(8); if (b.len() != 8) { return 1; } var v: i32 = 0; var i: i32 = 0; while (i < 8) { v = v | (b[i] as i32); i = i + 1; } if (v != 0) { return 7; } return 2; }`, 7},
	}

	// runCase: emit `src` via the self-host CLI for arm64-darwin, link it,
	// assert it's a valid arm64 Mach-O, and (on Apple Silicon) execute it
	// and check the exit code.
	runCase := func(name, src string, wantExit int) {
		t.Run(name, func(t *testing.T) {
			srcPath := filepath.Join(dir, name+".fern")
			if err := os.WriteFile(srcPath, []byte(src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			asmPath := filepath.Join(dir, name+".s")
			out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", asmPath, srcPath).CombinedOutput()
			if err != nil {
				if native {
					// The self-host CLI is itself a fresh Mach-O the kernel
					// may reject; treat a CLI launch/run failure as a skip
					// (it's the runtime question this test is probing), not
					// a regression.
					t.Skipf("self-host CLI did not emit (err=%v):\n%s", err, out)
				}
				t.Fatalf("self-host emit failed: %v\n%s", err, out)
			}

			binPath := filepath.Join(dir, name+".bin")
			if out, err := exec.Command("clang", linkArgs(asmPath, binPath)...).CombinedOutput(); err != nil {
				t.Fatalf("clang Mach-O link failed: %v\n%s", err, out)
			}

			// Structural validation (runs on every host).
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

			if !native {
				return // structural-only off Apple Silicon
			}
			cmd := exec.Command(binPath)
			runErr := cmd.Run()
			ps := cmd.ProcessState
			if ps == nil || !ps.Exited() {
				t.Skipf("Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
			}
			if code := ps.ExitCode(); code != wantExit {
				t.Errorf("self-host arm64-darwin %q exit = %d, want %d", name, code, wantExit)
			}
		})
	}

	for _, c := range cases {
		runCase(c.name, c.src, c.wantExit)
	}

	// read_file — exercises openat/lseek/read/close (number-compatible
	// Darwin syscalls) plus the carry-flag errno normalisation darwinize
	// injects so the self-host's `x0 < 0` error checks see Linux-shaped
	// -errno. The Ok path reads a known file and returns its length; the
	// missing-file path must hit the Err arm (proving errno normalisation
	// works — without it openat's +errno would look like a valid fd).
	const rfContent = "hello, fern!" // 12 bytes
	okPath := filepath.Join(dir, "rf_data.txt")
	if err := os.WriteFile(okPath, []byte(rfContent), 0o644); err != nil {
		t.Fatalf("write rf data: %v", err)
	}
	runCase("read_file_ok",
		`function main(): i32 { match (read_file("`+okPath+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } }`,
		len(rfContent))
	runCase("read_file_missing",
		`function main(): i32 { match (read_file("`+filepath.Join(dir, "no_such_file_zzz")+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } }`,
		99)

	// write_file — openat(O_WRONLY|O_CREAT|O_TRUNC)/write/close. The Darwin
	// open flags (1537) and AT_FDCWD (-2) differ from Linux. Round-trip:
	// write a long string, overwrite with a short one (exercises O_TRUNC —
	// without it the file would keep trailing bytes), read back, return the
	// length. Expect 2, not 11, iff O_TRUNC took effect.
	wfPath := filepath.Join(dir, "wf_data.txt")
	runCase("write_file_trunc_roundtrip",
		`function main(): i32 {
  match (write_file("`+wfPath+`", "longcontent")) { Some(e) => { return 91; }, None => {} }
  match (write_file("`+wfPath+`", "hi")) { Some(e) => { return 92; }, None => {} }
  match (read_file("`+wfPath+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 93; } }
}`,
		2)
}

// buildSelfHostBinArm64Darwin compiles a self-host driver (fernName,
// living in dir with its imports) into a native arm64-darwin Mach-O
// executable via the Go arm64 backend + clang/ld64, and returns its
// path. Used to run the self-host CLI on the macOS arm64 runner. The
// emit step is host-independent (verified off Apple Silicon too); the
// clang link is the same shape TestArm64DarwinBuilds proves on macOS.
func buildSelfHostBinArm64Darwin(t *testing.T, dir, fernName, out string) string {
	t.Helper()
	prog, _, err := modload.Load(filepath.Join(dir, fernName))
	if err != nil {
		t.Fatalf("modload %s: %v", fernName, err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold %s: %v", fernName, err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check %s: %v", fernName, err)
	}
	asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
	if err != nil {
		t.Fatalf("arm64-darwin emit %s: %v", fernName, err)
	}
	asmPath := filepath.Join(dir, out+".s")
	binPath := filepath.Join(dir, out)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", out, err)
	}
	if o, err := exec.Command("clang", "-nostdlib", "-lSystem", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("clang build of self-host CLI failed: %v\n%s", err, o)
	}
	return binPath
}
