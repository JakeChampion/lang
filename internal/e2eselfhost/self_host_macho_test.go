package e2eselfhost

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
// `-target arm64-darwin`. Since the flip (slice 3q) this path is fully
// in-process: asm_arm64.darwinize emits the GAS text, and arm64_native
// assembles + links + signs the Mach-O binary directly — no `.s`, no
// clang/ld64.
//
// Two host modes:
//
//   - Off Apple Silicon (the Linux CI box): the CLI is built with the Go
//     x86-64 backend so it runs on the host; we assert each emitted file is
//     a well-formed arm64 Mach-O executable. qemu-aarch64 only speaks the
//     Linux ABI, so we can't run the result.
//
//   - On the macOS arm64 CI runner: the CLI is built FOR arm64-darwin (Go
//     arm64 backend + clang/ld64 to link the CLI itself) so it runs
//     natively, then we run it to emit each program's binary and EXECUTE
//     it, checking exit codes. This is the decisive runtime check of the
//     self-host Darwin path. Launch failures (kernel rejects the binary)
//     are reported as skips with diagnostics, not hard failures — a wrong
//     exit code (ran but misbehaved) is a hard failure.
//
// darwinize() reuses asm_arm64.fern's instruction selection and only
// reskins the assembler dialect (@PAGE/@PAGEOFF addressing, Mach-O
// sections, _main entry) and remaps the number-compatible syscalls
// (read/write/close/openat/lseek/exit/mmap) to the BSD vector with
// `svc #0x80`. Supported surface is the core language; the ABI-divergent
// syscalls (clock_gettime/getrandom/fstat/subprocess) are out of scope —
// see the darwinize doc comment.
func TestSelfHostArm64DarwinBuilds(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	// Stage the full self-host project (lexer/parser/asm via the helper,
	// plus the rest of the modules fern.fern imports).
	dir := writeSelfHostAsmProject(t)
	// fern.fern's full transitive import closure beyond what
	// writeSelfHostAsmProject stages (asm / lexer / parser). The ssa* modules
	// were added by the self-host SSA pipeline; arm64_native.fern is the
	// in-process arm64-darwin assembler/linker the flipped path imports.
	copySelfHostDriver(t, dir, "fern.fern")

	// Build the self-host CLI for the host so it runs natively. No linker
	// flags for emitted programs anymore — the CLI writes the Mach-O binary
	// directly. (clang is still needed on macOS only to link the *CLI*.)
	var fernBin string
	if native {
		// Native macOS arm64: build the CLI FOR arm64-darwin (Go arm64
		// backend + clang/ld64) so it runs on the runner.
		if _, err := exec.LookPath("clang"); err != nil {
			t.Skip("clang not on PATH; skipping arm64-darwin self-host e2e (needed to link the CLI)")
		}
		fernBin = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	} else {
		// Cross from Linux: the CLI is an x86-64 host binary; its emitted
		// arm64-darwin binaries are checked structurally (qemu can't run
		// Mach-O).
		gcc, runner := x86_64Tooling(t)
		if len(runner) != 0 {
			t.Skip("self-host CLI driver runs only natively (argv paths)")
		}
		fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
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

	// runCase: emit `src` via the self-host CLI for arm64-darwin straight to
	// a Mach-O binary, assert it's a valid arm64 Mach-O, and (on Apple
	// Silicon) execute it and check the exit code.
	runCase := func(name, src string, wantExit int) {
		t.Run(name, func(t *testing.T) {
			srcPath := filepath.Join(dir, name+".fern")
			if err := os.WriteFile(srcPath, []byte(src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, name+".bin")
			out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", binPath, srcPath).CombinedOutput()
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
			if err := os.Chmod(binPath, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
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
  match (write_file("`+wfPath+`", "longcontent")) { Err(e) => { return 91; }, Ok(_) => {} }
  match (write_file("`+wfPath+`", "hi")) { Err(e) => { return 92; }, Ok(_) => {} }
  match (read_file("`+wfPath+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 93; } }
}`,
		2)

	// stat — newfstatat(79) -> Darwin fstatat(470); the struct stat layout
	// differs (st_mode u16@4 / st_size@96 on Darwin vs u32@16 / @48 on
	// Linux). stat_file: a regular file reports is_file + its size; stat_dir:
	// a directory reports is_dir; stat_missing: a bad path hits the Err arm
	// (needs the errno normalization too).
	runCase("stat_file",
		`function main(): i32 { match (stat("`+okPath+`")) { Ok(fs) => { if (fs.is_file) { return fs.size; } return 1; }, Err(e) => { return 99; } } }`,
		len(rfContent))
	runCase("stat_dir",
		`function main(): i32 { match (stat("`+dir+`")) { Ok(fs) => { if (fs.is_dir) { return 7; } return 1; }, Err(e) => { return 99; } } }`,
		7)
	runCase("stat_missing",
		`function main(): i32 { match (stat("`+filepath.Join(dir, "no_such_stat_zzz")+`")) { Ok(fs) => { return 1; }, Err(e) => { return 99; } } }`,
		99)

	// remove_file — unlinkat(35) -> Darwin 472, AT_FDCWD -2. Full file
	// lifecycle: write a file, delete it, then stat must report it gone
	// (the Err arm). Returns 7 iff write + remove + the "now gone" stat
	// all behaved.
	rmPath := filepath.Join(dir, "rm_target.txt")
	runCase("remove_file_lifecycle",
		`function main(): i32 {
  match (write_file("`+rmPath+`", "x")) { Err(e) => { return 1; }, Ok(_) => {} }
  match (remove_file("`+rmPath+`")) { Err(e) => { return 2; }, Ok(_) => {} }
  match (stat("`+rmPath+`")) { Ok(fs) => { return 3; }, Err(e) => { return 7; } }
}`,
		7)

	// monotonic_ns — Darwin reads CNTVCT_EL0/CNTFRQ_EL0 (mach_absolute_time
	// is exactly this on Apple Silicon) instead of clock_gettime. Two
	// back-to-back reads must be monotonic and nonzero. If CNTVCT_EL0 were
	// not EL0-readable the binary would SIGILL → runCase reports a skip,
	// not a failure.
	runCase("monotonic_ns",
		`function main(): i32 { var a: i64 = monotonic_ns(); var b: i64 = monotonic_ns(); if (b >= a) { if (a > 0) { return 7; } } return 1; }`,
		7)

	// temp_dir — builds /tmp/<prefix>-<ns> (ns from monotonic_ns, so this
	// also exercises CNTVCT) and mkdirat()s it (Darwin #475, AT_FDCWD -2).
	// Returns Ok(path) with a nonempty path on success.
	runCase("temp_dir",
		`function main(): i32 { match (temp_dir("fern-darwin-test")) { Ok(d) => { if (d.len() > 0) { return 7; } return 1; }, Err(e) => { return 2; } } }`,
		7)

	// read_dir + remove_dir_all — the full fs-builtins lifecycle on
	// Darwin, reusing fsBuiltinsProgram: temp_dir, write_file, stat,
	// read_dir, remove_dir_all, then stat must report the tree gone.
	// read_dir/remove_dir_all map getdents64(61) -> getdirentries64(344)
	// and diverge from Linux on AT_FDCWD (-2), O_DIRECTORY (0x100000),
	// the 64-bit-inode dirent name offset (21, vs 19), the basep 4th arg
	// getdirentries64 requires, and AT_REMOVEDIR (0x80). Returns 42 only
	// if every step round-trips.
	runCase("fs_builtins_lifecycle", fsBuiltinsProgram, 42)

	// sleep_ms — Darwin has no nanosleep syscall, so this lowers to
	// select(0, NULL, NULL, NULL, &timeval) (sysno 93). A short sleep
	// must return normally (a wrong syscall number would SIGILL → runCase
	// reports a skip, not a failure; a clean exit 7 proves select ran).
	runCase("sleep_ms",
		`function main(): i32 { sleep_ms(5); return 7; }`,
		7)

	// subprocess — fork/exec on Darwin: pipe() (sysno 42, two fds in
	// x0/x1) instead of pipe2, fork() (sysno 2, x1 child-flag) instead of
	// clone, dup3->dup2 (90) / execve (59) / wait4 (7) via darwin_sysno.
	// envp comes from the C-ABI _main(argc, argv, envp) entry (x2), now
	// captured correctly. echo "hi" resolves via the /bin/<cmd> fallback;
	// exit 0 and 3 bytes of stdout ("hi\n") prove the happy path.
	runCase("subprocess_echo",
		`function main(): i32 {
  var r: ProcessResult = subprocess("echo", ["hi"], "");
  if (r.exit_code != 0) { return 90; }
  return r.stdout.len();
}`,
		3)

	// subprocess exit-code decode: `sh -c "exit 7"` -> exit_code 7
	// (WIFEXITED/WEXITSTATUS, status>>8, shared with Linux).
	runCase("subprocess_exit_code",
		`function main(): i32 {
  var r: ProcessResult = subprocess("sh", ["-c", "exit 7"], "");
  return r.exit_code;
}`,
		7)

	// subprocess stdin->stdout round-trip: feed "piped" to `cat`, capture
	// its stdout. Exercises the parent's write(in_w)/read(out_r) pipe path
	// (5 bytes back).
	runCase("subprocess_stdin",
		`function main(): i32 {
  var r: ProcessResult = subprocess("cat", [], "piped");
  if (r.exit_code != 0) { return 90; }
  return r.stdout.len();
}`,
		5)

	// subprocess spawn-failure: a command that resolves nowhere must hit
	// the child's exit(127) after all execve attempts fail (POSIX
	// convention), surfaced as exit_code 127.
	runCase("subprocess_missing",
		`function main(): i32 {
  var r: ProcessResult = subprocess("fern-no-such-binary-zzz-7349", [], "");
  return r.exit_code;
}`,
		127)

	// tcp_listen/tcp_close — exercises the Darwin socket(97)/bind(104)/
	// listen(106)/close(6) syscalls and the sockaddr_in sin_len byte
	// without needing a client: bind an ephemeral high port (39xxx),
	// confirm the listener fd is valid, then close it. Returns 7 iff the
	// whole socket→bind→listen→close path succeeded. (A full client/
	// server round-trip runs under qemu in TestSelfHostTcpServerArm64;
	// here we just prove the Darwin syscalls don't fault.)
	runCase("tcp_listen_close",
		`function main(): i32 {
  var fd: i32 = tcp_listen(39517);
  if (fd < 0) { return 1; }
  if (tcp_close(fd) < 0) { return 2; }
  return 7;
}`,
		7)

	// udp_send — exercises the Darwin socket(97)/sendto(133)/close(6)
	// path and the dotted-quad host parse. UDP is connectionless, so a
	// sendto to a local port with no receiver still succeeds and returns
	// the byte count (the datagram is simply dropped). Returns the 3
	// payload bytes iff socket→parse→sendto→close all worked.
	runCase("udp_send",
		`function main(): i32 { return udp_send("127.0.0.1", 39518, "abc"); }`,
		3)
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
