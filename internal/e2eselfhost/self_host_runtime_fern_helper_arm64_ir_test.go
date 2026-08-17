package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2649 — the arm64 sibling of TestSelfHostRuntimeHelperStrToI32IsFernIR.
//
// The syscall leaves that have reached arm64 as Fern runtime functions:
// random_bytes over the __syscall3 sub-floor, the fs leaves (read_file /
// write_file / remove_file / temp_dir / stat) over __syscall3 + __syscall4 +
// __raw_scratch, and env over __raw_environ — each lowered through the IR
// pipeline by
// asm_arm64_ir.emit_ir_runtime_fern_fn. The register-ABI hand-asm they replace —
// including two ~30-line getrandom / getentropy bodies forked on `darwin` — is
// gone.
//
// Behaviour is covered under qemu by TestSelfHostAsmIRArm64Path/random-bytes-*
// and TestSelfHostIoErrorIRArm64. This test is the emission lock-in and runs on
// every x86 lane: the emitted aarch64 asm must define each Fern-compiled symbol,
// must NOT define the hand-asm one, and must contain the __syscall3 op's number
// load — the same instruction darwinize keys its Mach-O rewrite off.
func TestSelfHostRuntimeHelperSyscallLeavesAreFernArm64IR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_arm64_rb")

	// One program touching every migrated leaf, so a single emit covers them all.
	prog := "function main(): i32 {\n" +
		"    var b: string = random_bytes(8);\n" +
		"    match (write_file(\"/tmp/fern_lockin.txt\", \"x\")) { Ok(_) => {}, Err(_) => { return 1; } }\n" +
		"    match (read_file(\"/tmp/fern_lockin.txt\")) { Ok(_) => {}, Err(_) => { return 2; } }\n" +
		"    match (remove_file(\"/tmp/fern_lockin.txt\")) { Ok(_) => {}, Err(_) => { return 3; } }\n" +
		"    match (temp_dir(\"lockin\")) { Ok(_) => {}, Err(_) => { return 4; } }\n" +
		"    match (env(\"PATH\")) { Some(_) => {}, None => {} }\n" +
		"    match (stat(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 5; } }\n" +
		"    var xs: i32[] = [1, 2];\n" +
		"    if (xs.reverse().concat(xs).len() != 4) { return 6; }\n" +
		"    if (xs[0:1].len() != 1) { return 7; }\n" +
		"    if (now_unix_ms() < (1577836800000 as i64)) { return 8; }\n" +
		"    if (now_ns() < (0 as i64)) { return 9; }\n" +
		"    match (read_dir(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 10; } }\n" +
		"    match (remove_dir_all(\"/tmp/fern_lockin_nodir\")) { Ok(_) => {}, Err(_) => { return 11; } }\n" +
		// open_writer is BOUND, not matched: Result[Writer, IoError] destructuring
		// is outside the IR subset, and emitting __fern_open_fd is all this needs.
		"    var w = open_writer(\"/tmp/fern_lockin_w.txt\");\n" +
		"    if (random_i32() == 0) { return 12; }\n" +
		// The two socket leaves that take only an fd. The probe is emitted, not
		// run, so a bogus fd is fine — this only has to make the ops lower.
		"    if (tcp_close(999) == 0) { return 13; }\n" +
		"    if (tcp_accept(999) >= 0) { return 14; }\n" +
		"    sleep_ms(1);\n" +
		"    var pfds: i32[] = [];\n" +
		"    if (poll(pfds, 0) != 0 - 1) { return 15; }\n" +
		"    if (proc_waitpid(0 - 1) == 0) { return 16; }\n" +
		"    if (timer_fd(1) < 0) { return 17; }\n" +
		"    if (tcp_listen(0) < 0) { return 18; }\n" +
		"    if (tcp_connect(2130706433, 1) == 0) { return 19; }\n" +
		"    if (tcp_recv(999, 4).len() != 0) { return 20; }\n" +
		"    var av: string[] = [];\n" +
		"    if (proc_exec(\"/nonexistent\", av) == 0) { return 21; }\n" +
		"    if (proc_fork() == 123456) { return 22; }\n" +
		"    if (tcp_send(999, \"x\") >= 0) { return 23; }\n" +
		// The four stdout/stderr leaves. Called for effect, not tested — the
		// probe is emitted rather than run, and all this has to do is make the
		// ops lower so the helpers are emitted.
		"    print_str(\"x\");\n" +
		"    print_int(1);\n" +
		"    putchar(65);\n" +
		"    eprint(\"x\");\n" +
		// The two stdin leaves. Emitted, not run, so the empty stdin of a build
		// machine is irrelevant — this only has to make the ops lower.
		"    if (read_int() < (0 as i64)) { return 24; }\n" +
		"    if (read_all_stdin().len() != 0) { return 25; }\n" +
		"    return b.len();\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "rb_ir.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)

	for _, leaf := range []string{"random_bytes", "read_file", "write_file", "remove_file", "temp_dir", "env", "stat",
		"arr_reverse", "arr_concat", "arr_slice",
		// The clocks (#2649): now_unix_ms / now_ns are Fern on every native
		// target. monotonic_ns is NOT in this list — it is Fern on Linux but
		// hand-asm on Darwin (mrs cntvct_el0 is not a syscall), so the "hand-asm
		// is back" arm would be wrong for it. The Darwin test below covers that
		// case from the other side.
		"now_unix_ms", "now_ns",
		// The directory pair (#2649) — the last two off the shape-diverging
		// list. Darwin's getdirentries64 4th out-param is __syscall4 and its
		// dirent name offset is a direntoff row, so no per-target body.
		"read_dir", "remove_dir_all",
		// The CSPRNG i32 and the Reader/Writer file opener (#2649). open_fd
		// carries the Darwin open-flag translation the hand-asm did inline; it
		// has to stay a run-time check because irlower picks the flags and has
		// no target, so the Linux emit here simply has no translation to make.
		"random_i32", "open_fd",
		// The socket leaves that take only an fd (#2649).
		"tcp_close", "tcp_accept",
		// sleep_ms is the first __syscall5 consumer — but only on Darwin, which
		// sleeps with select(2). This Linux emit reaches it through nanosleep,
		// so the assertion here is just that the helper is Fern at all; the
		// Darwin test below is what pins the five-argument path.
		"sleep_ms",
		// poll reaches ppoll through __syscall5 on this target; x86-64 uses
		// three-argument poll(2). Both are Fern.
		"poll",
		// wait4 plus the shell-style status decode. Four arguments on every
		// target and only the number moves, so no per-target fork.
		"proc_waitpid",
		// timerfd_create + timerfd_settime over an itimerspec in __fern_scratch.
		"timer_fd",
		// socket + bind + listen over a byte-built sockaddr_in. Its first two
		// bytes are the one per-target difference (XNU has sin_len).
		"tcp_listen",
		// Same sockaddr_in as tcp_listen with the address filled in. Migrating
		// this fixed arm64-darwin, which had been issuing Linux connect (203)
		// through the Linux trap because darwin_sysno has no row for it.
		"tcp_connect",
		// read(2) into a fresh buffer, boxed by __raw_string — the floor goes
		// this direction, which is why recv migrates and send cannot.
		"tcp_recv",
		// execve over an argv built from the args array. The one leaf whose
		// contract is that it does NOT return on success.
		"proc_exec",
		// clone(SIGCHLD, 0, 0, 0, 0) via __syscall5 — arm64 Linux has no bare
		// fork(2). Fern on THIS target only; the Darwin test below pins the
		// opposite, since XNU marks the child in x1 and no __syscall* sees it.
		"proc_fork",
		// write(2) straight out of the string's own buffer. The last leaf, and
		// the one whose blocker turned out not to exist: __raw_data needed no
		// op of its own, since a string value already IS its box pointer.
		"tcp_send",
		// The stdout/stderr four (#2649): all plain write(2), so unlike the fs
		// family they carry no per-target constant beyond the syscall number.
		// print_int is the widest of them — one i64 helper serving both
		// op_print_int and op_print_i64 — and the reason the group needed
		// __raw_scratch: putchar and print_int staged their bytes in a stack
		// slot, which is the one thing a Fern helper cannot name.
		"print_str", "print_int", "putchar", "eprint_str",
		// The stdin pair (#2649), read(2) to the stdout four's write(2). Neither
		// returns an Option, so neither carries a box-layout question: read_int
		// parses out of __raw_scratch and read_all_stdin boxes with __raw_string.
		// arm64 shed more here than the other targets — the dead AST
		// __fern_read_all_stdin body and its __fern_read_all_stdin_rc IR twin both
		// went with the migration.
		"read_int", "read_all_stdin"} {
		if !strings.Contains(asm, "__fn___fern_"+leaf+":") {
			t.Errorf("__fn___fern_%s not defined — the Fern helper did not lower", leaf)
		}
		if !strings.Contains(asm, "bl __fn___fern_"+leaf) {
			t.Errorf("op_%s does not call the stack-ABI Fern symbol", leaf)
		}
		if strings.Contains(asm, "\n__fern_"+leaf+":") {
			t.Errorf("the register-ABI hand-asm __fern_%s is back", leaf)
		}
	}
	// monotonic_ns is Fern on arm64 LINUX (clock_gettime 113); only the Darwin
	// form stays hand-written, so this leg gets the full three-way assertion
	// while the shared loop above skips it.
	//
	// Note the probe never calls monotonic_ns() directly — temp_dir()'s body
	// does. That is deliberate: the clocks are emitted by
	// emit_rt_clock_and_print, which runs BEFORE the fs bundle, so a need
	// marked while lowering temp_dir's own body would arrive too late. This
	// assertion is what pins op_temp_dir marking it up front.
	if !strings.Contains(asm, "__fn___fern_monotonic_ns:") {
		t.Error("__fn___fern_monotonic_ns not defined — the Fern clock did not lower on arm64 Linux")
	}
	if strings.Contains(asm, "\n__fern_monotonic_ns:") {
		t.Error("the register-ABI hand-asm __fern_monotonic_ns is back on arm64 Linux")
	}
	// The clocks are heap-independent, so their scratch buffer cannot ride the
	// fs runtime's gate — a pure-scalar timing program would reference a slot
	// nothing defined. It is emitted by emit_rt_clock_and_print now.
	if strings.Count(asm, "__fern_scratch: .skip 256") != 1 {
		t.Errorf("want exactly one __fern_scratch definition, got %d", strings.Count(asm, "__fern_scratch: .skip 256"))
	}
	// The fs leaves call a Fern __fern_io_error bundled with them, rather than
	// inlining the five-way errno classification — the "dependencies are the
	// call graph" shape #2649 is aiming at. open_fd was the last leaf holding
	// the register-ABI hand-asm sibling alive; it is deleted now.
	if !strings.Contains(asm, "bl __fn___fern_io_error") {
		t.Error("the migrated fs leaves do not call the bundled Fern __fern_io_error")
	}
	// env has no syscall at all: it reads __fern_envp through the __raw_environ
	// op. The .bss slot must still be emitted — _start's save is gated on `heap`,
	// not on `env`, so a heap program that never calls env() stores here too.
	// arr_reverse / arr_concat / arr_slice allocate their fresh box through
	// __raw_arr_box, which is the one array primitive that stays a call.
	if !strings.Contains(asm, "bl __fern_arr_box") {
		t.Error("__raw_arr_box did not emit the __fern_arr_box call")
	}
	// stat writes into the fixed .bss scratch through __raw_scratch, so both the
	// slot and the op's address materialisation have to be there.
	if !strings.Contains(asm, "__fern_scratch: .skip 256") {
		t.Error("the __fern_scratch .bss slot is missing — stat has nowhere to land")
	}
	if !strings.Contains(asm, "adrp x0, __fern_scratch\n    add x0, x0, :lo12:__fern_scratch\n") {
		t.Error("__raw_scratch did not emit the arm64 scratch-buffer address")
	}
	if !strings.Contains(asm, "__fern_envp: .quad 0") {
		t.Error("the __fern_envp .bss slot went with the hand-asm __fern_env")
	}
	if !strings.Contains(asm, "adrp x0, __fern_envp\n    add x0, x0, :lo12:__fern_envp\n    ldr x0, [x0]\n") {
		t.Error("__raw_environ did not emit the arm64 envp load")
	}
	// write_file's O_CREAT mode arg makes it the __syscall4 user; its number
	// load rides the same darwinize marker as __syscall3's.
	if !strings.Contains(asm, "    ldr x3, [sp], #16\n    ldr x2, [sp], #16\n    ldr x1, [sp], #16\n    ldr x0, [sp], #16\n    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall4 did not emit the arm64 5-pop + svc sequence")
	}
	// The __syscall3 op's number load. darwinize rewrites exactly this line to
	// `ldr x16, ...` and flips the following trap, so a change to the operand
	// order here silently breaks the Mach-O path — pin the instruction.
	if !strings.Contains(asm, "    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall3 did not emit the `ldr x8` + `svc #0` sequence darwinize matches")
	}
}

// TestSelfHostSyscallLeavesDarwinizedArm64 pins the Mach-O half of the same
// migration. darwinize cannot remap a syscall number it only sees at runtime
// (its rule needs a literal `mov x8, #N`), so the generic __syscall3 /
// __syscall4 ops instead carry the target's number in the Fern source, and
// darwinize rewrites only the number register and the trap. Without that rule
// the Mach-O binary would issue a Linux `svc #0` with a Linux number.
//
// It also pins the sticky-`pend_sys` fix: darwinize used to reset its pending
// syscall on EVERY line, so it only rewrote an `svc` that came IMMEDIATELY
// after the number load. The two abort paths load their exit status in between,
// which left exit(125) / exit(134) trapping through the Linux vector on XNU.
func TestSelfHostSyscallLeavesDarwinizedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc_darwin_rb")

	prog := "function main(): i32 {\n" +
		"    var b: string = random_bytes(8);\n" +
		"    match (write_file(\"/tmp/fern_lockin_d.txt\", \"x\")) { Ok(_) => {}, Err(_) => { return 1; } }\n" +
		"    match (read_file(\"/tmp/fern_lockin_d.txt\")) { Ok(_) => {}, Err(_) => { return 2; } }\n" +
		"    match (remove_file(\"/tmp/fern_lockin_d.txt\")) { Ok(_) => {}, Err(_) => { return 3; } }\n" +
		"    match (temp_dir(\"lockin\")) { Ok(_) => {}, Err(_) => { return 4; } }\n" +
		"    match (stat(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 5; } }\n" +
		"    var xs: i32[] = [1, 2];\n" +
		"    if (xs[0:1].len() != 1) { return 6; }\n" +
		"    if (now_unix_ms() < (1577836800000 as i64)) { return 7; }\n" +
		"    if (monotonic_ns() < (0 as i64)) { return 8; }\n" +
		"    if (now_ns() < (0 as i64)) { return 9; }\n" +
		"    match (read_dir(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 10; } }\n" +
		"    match (remove_dir_all(\"/tmp/fern_lockin_nodir\")) { Ok(_) => {}, Err(_) => { return 11; } }\n" +
		// open_writer is BOUND, not matched: Result[Writer, IoError] destructuring
		// is outside the IR subset, and emitting __fern_open_fd is all this needs.
		"    var w = open_writer(\"/tmp/fern_lockin_w.txt\");\n" +
		"    if (random_i32() == 0) { return 12; }\n" +
		// The two socket leaves that take only an fd. The probe is emitted, not
		// run, so a bogus fd is fine — this only has to make the ops lower.
		"    if (tcp_close(999) == 0) { return 13; }\n" +
		"    if (tcp_accept(999) >= 0) { return 14; }\n" +
		"    sleep_ms(1);\n" +
		// connect is the one socket number darwin_sysno cannot remap, so the
		// Darwin emit has to carry it from asmcore.sysno — assert below.
		"    if (tcp_connect(2130706433, 1) == 0) { return 15; }\n" +
		"    if (tcp_recv(999, 4).len() != 0) { return 16; }\n" +
		"    if (proc_fork() == 123456) { return 17; }\n" +
		// The four stdout/stderr leaves, called for effect so the helpers are
		// emitted and their write(2) number can be inspected below.
		"    print_str(\"x\");\n" +
		"    print_int(1);\n" +
		"    putchar(65);\n" +
		"    eprint(\"x\");\n" +
		// The two stdin leaves, likewise called for effect so their read(2)
		// number can be inspected below.
		"    if (read_int() < (0 as i64)) { return 18; }\n" +
		"    if (read_all_stdin().len() != 0) { return 19; }\n" +
		"    return b.len();\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "rb_darwin.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, err := exec.Command(mmc, srcFile, "-target", "arm64-darwin").Output()
	if err != nil {
		t.Fatalf("self-host arm64-darwin emit failed: %v", err)
	}
	asm := string(out)

	// Each leaf's Darwin number / flag-set, straight out of asmcore.sysno /
	// oflag. These reach the syscall as an operand, so a wrong entry cannot be
	// caught anywhere downstream — the Linux number would just be issued
	// against the BSD vector (openat 56 is Darwin's `chdir`).
	for _, c := range []struct {
		what string
		imm  string
	}{
		{"getentropy", "500"},
		{"openat", "463"},
		{"unlinkat", "472"},
		{"O_WRONLY|O_CREAT|O_TRUNC", "1537"},
		{"lseek", "199"},
		{"mkdirat", "475"},
		{"fstatat64", "470"},
		{"st_size offset", "96"},
		// gettimeofday, the clocks' Darwin stand-in: XNU has no clock_gettime
		// syscall, and this number reaches the trap as a pushed operand, so
		// darwinize never sees it and a wrong sysno row is invisible elsewhere.
		{"gettimeofday", "116"},
		{"getdirentries64", "344"},
		{"AT_REMOVEDIR", "128"},
		// select, XNU's stand-in for nanosleep — and the only reason
		// __syscall5 exists. Linux sleeps through nanosleep, which takes two.
		{"select", "93"},
		// connect. darwin_sysno cannot remap this one — it has no 203 row — so
		// before the Fern migration the Mach-O emit carried Linux's number and
		// Linux's trap. This assertion is the regression guard for that.
		{"connect", "98"},
	} {
		if !strings.Contains(asm, "mov x0, #"+c.imm+"\n") {
			t.Errorf("the Darwin %s constant (%s) was not baked into the helper source", c.what, c.imm)
		}
	}
	// Constants above 65535 do not reach `mov x0, #N` at all:
	// emit_const_i32_push only takes that path for 0..65535 (a single movz),
	// and puts everything wider in the literal pool. O_DIRECTORY is the one
	// operand in this helper set that crosses the line.
	//
	// It is also the one whose value is NOT shared across targets — 0x100000 on
	// XNU, 65536 on x86-64 Linux, 16384 on arm64 Linux. Every wrong choice is a
	// silent wrong FLAG rather than a rejected constant: 65536 on Darwin means
	// O_NDELAY|O_ASYNC (the open succeeds, the first read fails), and 65536 on
	// arm64 Linux means O_RDONLY|O_DIRECT (EINVAL on /tmp).
	for _, c := range []struct{ what, imm string }{
		{"O_RDONLY|O_DIRECTORY", "1048576"},
	} {
		if !strings.Contains(asm, "ldr x0, ="+c.imm+"\n") {
			t.Errorf("the Darwin %s constant (%s) was not baked into the helper source", c.what, c.imm)
		}
	}
	// There is deliberately NO matching negative assertion. A bare search for
	// the Linux values cannot work: 65536 is also the getdents read buffer's
	// size, which is target-independent and legitimately present, and 16384
	// turns up elsewhere too. Nothing lexical separates "an open flag" from "a
	// buffer size" once both are just an operand push, so the honest coverage
	// for the wrong-flag case is the arm64 runtime leg (dirs-fern under qemu) —
	// which is in fact what caught the 16384 bug, after this emission test had
	// been green on it.

	// The four stdout/stderr leaves (#2649) all trap through write(2), Darwin
	// number 4. That number cannot join the table above: 4 is an ordinary literal
	// this emit already holds fifteen of, so a file-wide search would pass whatever
	// asmcore.sysno said. Scope it to each helper's own body instead. Like every
	// migrated number it reaches the trap as a pushed operand rather than the
	// `mov x8, #N` darwinize rewrites, so a wrong row is invisible everywhere else —
	// Linux's 64 would simply be issued against the BSD vector.
	for _, sym := range []string{"__fn___fern_print_str", "__fn___fern_print_int", "__fn___fern_putchar", "__fn___fern_eprint_str"} {
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Errorf("%s not defined — the Fern helper did not lower for Darwin", sym)
			continue
		}
		if !strings.Contains(body, "    mov x0, #4\n    str x0, [sp, #-16]!\n") {
			t.Errorf("%s does not push Darwin's write number (4)", sym)
		}
		if strings.Contains(body, "    mov x0, #64\n    str x0, [sp, #-16]!\n") {
			t.Errorf("%s pushes Linux's write number (64) in Mach-O output", sym)
		}
		if strings.Contains(asm, "\n"+strings.TrimPrefix(sym, "__fn_")+":") {
			t.Errorf("the register-ABI hand-asm %s is back", strings.TrimPrefix(sym, "__fn_"))
		}
	}
	// The read(2) users (#2649), Darwin number 3. Same reason for body-scoping,
	// only more so: `mov x0, #3` followed by the operand push occurs seven times
	// across this emit, so a file-wide match would pass whatever asmcore.sysno's
	// `read` row said. The number reaches the trap as a pushed operand rather than
	// the `mov x8, #N` darwinize rewrites, so a wrong row is invisible everywhere
	// else — Linux's 63 would simply be issued against the BSD vector.
	//
	// tcp_recv is in this loop rather than the constant table for the same reason,
	// and it used to assert only that the helper was Fern at all: scoping to the
	// body is what makes its number checkable.
	for _, sym := range []string{"__fn___fern_read_int", "__fn___fern_read_all_stdin", "__fn___fern_tcp_recv"} {
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Errorf("%s not defined — the Fern helper did not lower for Darwin", sym)
			continue
		}
		if !strings.Contains(body, "    mov x0, #3\n    str x0, [sp, #-16]!\n") {
			t.Errorf("%s does not push Darwin's read number (3)", sym)
		}
		if strings.Contains(body, "    mov x0, #63\n    str x0, [sp, #-16]!\n") {
			t.Errorf("%s pushes Linux's read number (63) in Mach-O output", sym)
		}
		if strings.Contains(asm, "\n"+strings.TrimPrefix(sym, "__fn_")+":") {
			t.Errorf("the register-ABI hand-asm %s is back", strings.TrimPrefix(sym, "__fn_"))
		}
	}
	if strings.Contains(asm, "ldr x8, [sp], #16") {
		t.Error("darwinize left the __syscall3 number load on x8 (Linux form) in Mach-O output")
	}
	if !strings.Contains(asm, "    ldr x16, [sp], #16\n    svc #0x80\n") {
		t.Error("darwinize did not rewrite the __syscall3 sequence to the Darwin trap")
	}
	// The generic syscall is fallible, so darwinize must also normalise the
	// carry-flag errno back to Linux's -errno (what `if (r < 0)` in the helper
	// tests). Without it a Darwin failure reads as a positive byte count.
	if !strings.Contains(asm, "    svc #0x80\n    b.cc ") {
		t.Error("darwinize did not emit the errno normalisation after the generic syscall")
	}
	// The abort paths: `mov x8, #93` (exit) -> `mov x16, #1`, then the status
	// load, THEN the trap. Only a sticky pending-syscall survives the line in
	// between, and exit needs no errno normalisation.
	for _, status := range []string{"125", "134"} {
		if !strings.Contains(asm, "    mov x16, #1\n    mov x0, #"+status+"\n    svc #0x80\n") {
			t.Errorf("the exit(%s) abort path still traps through the Linux vector on Mach-O", status)
		}
	}
	// arr_slice's trap is the OTHER exit path: not the hand-asm abort darwinize
	// rewrites, but a Fern __syscall3 whose number arrives on the stack. It
	// therefore goes through the ldr-x16 form above rather than `mov x16, #1`,
	// and it is the reason asmcore.sysno needed an `exit` row at all.
	if !strings.Contains(asm, "__fn___fern_arr_slice:") {
		t.Error("__fn___fern_arr_slice not defined — the Fern helper did not lower for Darwin")
	}
	if strings.Contains(asm, "\n__fern_arr_slice:") {
		t.Error("the register-ABI hand-asm __fern_arr_slice is back")
	}
	// The two wall clocks migrate on Darwin too — gettimeofday is an ordinary
	// syscall. monotonic_ns does NOT: XNU's monotonic clock on Apple Silicon is
	// `mrs cntvct_el0` scaled by `cntfrq_el0`, not a syscall, and Fern has no
	// system-register-read intrinsic. It keeps the __fn___ name anyway (a
	// zero-arg helper's stack and register conventions coincide), so the call
	// site does not branch on the target — which is exactly what makes the
	// "is it really hand-asm here" question worth pinning.
	for _, leaf := range []string{"now_unix_ms", "now_ns"} {
		if !strings.Contains(asm, "__fn___fern_"+leaf+":") {
			t.Errorf("__fn___fern_%s not defined — the Fern clock did not lower for Darwin", leaf)
		}
	}
	// proc_fork is the second helper after monotonic_ns that stays hand-written
	// on Darwin for want of an expressible form rather than a syscall number.
	// darwinize would have supplied the carry-flag errno fold for free; what it
	// cannot supply is x1, which is the ONLY thing distinguishing the child from
	// the parent. It keeps the __fn___ name so the call site does not branch.
	if !strings.Contains(asm, "__fn___fern_proc_fork:") {
		t.Error("__fn___fern_proc_fork not defined for Darwin")
	}
	if !strings.Contains(asm, "    cbz x1, .Lpf_done\n") {
		t.Error("Darwin proc_fork lost the x1 child/parent discrimination — the reason it is not Fern")
	}
	if !strings.Contains(asm, "    mov x16, #2\n    svc #0x80\n") {
		t.Error("Darwin proc_fork is not trapping to BSD fork (2) through the Darwin vector")
	}
	// clone is Linux-only. If the Fern body were selected for Darwin, 220 would
	// arrive as a pushed operand the way every other migrated number does.
	if strings.Contains(asm, "    mov x0, #220\n    str x0, [sp, #-16]!\n") {
		t.Error("Darwin proc_fork pushed Linux's clone number (220) in Mach-O output")
	}
	if !strings.Contains(asm, "    mrs x9, cntvct_el0\n    mrs x10, cntfrq_el0\n") {
		t.Error("Darwin monotonic_ns is not reading the architectural counter")
	}
	if strings.Contains(asm, "mov x8, #113") || strings.Contains(asm, "mov x0, #113\n    str x0, [sp, #-16]!") {
		t.Error("a clock issued Linux's clock_gettime (113) in Mach-O output")
	}
	// The exit NUMBER is a pushed operand, so a wrong asmcore.sysno row is
	// invisible everywhere else: darwinize never sees it (it rewrites `mov x8`,
	// not a stack push), and the Linux leg would stay green. Pin the pair —
	// Darwin's exit is 1, and 93 (Linux's) must not be what gets pushed.
	if !strings.Contains(asm, "    mov x0, #1\n    str x0, [sp, #-16]!\n    mov x0, #134\n") {
		t.Error("arr_slice's trap does not push Darwin's exit number (1) ahead of status 134")
	}
	if strings.Contains(asm, "    mov x0, #93\n    str x0, [sp, #-16]!\n    mov x0, #134\n") {
		t.Error("arr_slice's trap pushes Linux's exit number (93) in Mach-O output")
	}
}
