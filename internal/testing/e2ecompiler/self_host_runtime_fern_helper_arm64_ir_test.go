package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostRuntimeHelperSyscallLeavesAreFernArm64IR(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc_arm64_rb")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	prog := "import \"std/array\";\n" +
		"function main(): i32 {\n" +
		"    let b: u8[] = random_bytes(8);\n" +
		"    match (write_file(\"/tmp/fern_lockin.txt\", \"x\")) { Ok(_) => {}, Err(_) => { return 1; } }\n" +
		"    match (read_file(\"/tmp/fern_lockin.txt\")) { Ok(_) => {}, Err(_) => { return 2; } }\n" +
		"    match (remove_file(\"/tmp/fern_lockin.txt\")) { Ok(_) => {}, Err(_) => { return 3; } }\n" +
		"    match (temp_dir(\"lockin\")) { Ok(_) => {}, Err(_) => { return 4; } }\n" +
		"    match (env(\"PATH\")) { Some(_) => {}, None => {} }\n" +
		"    match (stat(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 5; } }\n" +
		"    let xs: i32[] = [1, 2];\n" +
		"    if (xs.reverse().concat(xs).len() != 4) { return 6; }\n" +
		"    if (xs[0:1].len() != 1) { return 7; }\n" +
		"    if (now_unix_ms() < (1577836800000 as i64)) { return 8; }\n" +
		"    if (now_ns() < (0 as i64)) { return 9; }\n" +
		"    match (read_dir(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 10; } }\n" +
		"    match (remove_dir_all(\"/tmp/fern_lockin_nodir\")) { Ok(_) => {}, Err(_) => { return 11; } }\n" +
		"    match (open_writer(\"/tmp/fern_lockin_w.txt\")) { Ok(_) => {}, Err(_) => { return 26; } }\n" +
		"    if (random_i32() == 0) { return 12; }\n" +
		"    if (tcp_close(999) == 0) { return 13; }\n" +
		"    if (tcp_accept(999) >= 0) { return 14; }\n" +
		"    sleep_ms(1);\n" +
		"    let pfds: i32[] = [];\n" +
		"    if (poll(pfds, 0) != 0 - 1) { return 15; }\n" +
		"    if (proc_waitpid(0 - 1) == 0) { return 16; }\n" +
		"    if (timer_fd(1) < 0) { return 17; }\n" +
		"    if (tcp_listen(0) < 0) { return 18; }\n" +
		"    if (tcp_connect_with([127u8, 0u8, 0u8, 1u8], 1, false) == 0) { return 19; }\n" +
		"    if (tcp_recv_into(999, [0u8, 0u8, 0u8, 0u8]) >= 0) { return 20; }\n" +
		"    let av: string[] = [];\n" +
		"    if (proc_exec(\"/nonexistent\", av) == 0) { return 21; }\n" +
		"    if (proc_fork() == 123456) { return 22; }\n" +
		"    if (tcp_send_bytes(999, [120u8]) >= 0) { return 23; }\n" +
		"    write(\"x\");\n" +
		"    putchar(65);\n" +
		"    eprint(\"x\");\n" +
		"    let rd: Reader = stdin();\n" +
		"    match (rd.read_chunk(64)) { Ok(_) => {}, Err(_) => {} }\n" +
		"    rd.close();\n" +
		"    return b.len();\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "rb_ir.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, stderr, code := runDriverAllowFail(t, x86runner, mmc, "", srcFile, stdlibRoot, "-target", "arm64-linux")
	if code != 0 {
		t.Fatalf("self-host arm64 emit failed: exit %d\n%s", code, stderr)
	}
	asm := string(out)

	for _, leaf := range []string{"random_bytes_u8", "read_file", "write_file", "remove_file", "temp_dir", "env", "stat",
		"arr_slice",
		"now_unix_ms", "now_ns",
		"read_dir", "remove_dir_all",
		"random_i32", "open_res",
		"tcp_close", "tcp_accept",
		"sleep_ms",
		"poll",
		"proc_waitpid",
		"timer_fd",
		"tcp_listen",
		"tcp_connect_with_u8",
		"tcp_recv_into_u8",
		"proc_exec",
		"proc_fork",
		"tcp_send_bytes_u8",
		"print_str", "putchar", "eprint_str",
		"reader_read_chunk", "reader_close"} {
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
	if !strings.Contains(asm, "__fn___fern_monotonic_ns:") {
		t.Error("__fn___fern_monotonic_ns not defined — the Fern clock did not lower on arm64 Linux")
	}
	if strings.Contains(asm, "\n__fern_monotonic_ns:") {
		t.Error("the register-ABI hand-asm __fern_monotonic_ns is back on arm64 Linux")
	}
	if strings.Count(asm, "__fern_scratch: .skip 256") != 1 {
		t.Errorf("want exactly one __fern_scratch definition, got %d", strings.Count(asm, "__fern_scratch: .skip 256"))
	}
	if !strings.Contains(asm, "bl __fn___fern_io_error") {
		t.Error("the migrated fs leaves do not call the bundled Fern __fern_io_error")
	}
	if !strings.Contains(asm, "bl __fern_arr_box") {
		t.Error("__raw_arr_box did not emit the __fern_arr_box call")
	}
	if !strings.Contains(asm, "__fern_scratch: .skip 256") {
		t.Error("the __fern_scratch .bss slot is missing — stat has nowhere to land")
	}
	if !matchShape(asm, `\n    adrp (x[0-9]+), __fern_scratch\n    add \1, \1, :lo12:__fern_scratch\n`) {
		t.Error("__raw_scratch did not emit the arm64 scratch-buffer address")
	}
	if !strings.Contains(asm, "__fern_envp: .quad 0") {
		t.Error("the __fern_envp .bss slot went with the hand-asm __fern_env")
	}
	if !matchShape(asm, `\n    adrp (x[0-9]+), __fern_envp\n    add \1, \1, :lo12:__fern_envp\n    ldr \1, \[\1\]\n`) {
		t.Error("__raw_environ did not emit the arm64 envp load")
	}
	if !matchShape(asm, `\n    (?:mov x3, x[0-9]+|ldr x3, \[sp\], #16)\n    ldr x2, \[sp\], #16\n    ldr x1, \[sp\], #16\n    ldr x0, \[sp\], #16\n    ldr x8, \[sp\], #16\n    svc #0\n`) {
		t.Error("__syscall4 did not emit the arm64 four-argument marshal + number + svc sequence")
	}
	if !strings.Contains(asm, "    ldr x8, [sp], #16\n    svc #0\n") {
		t.Error("__syscall3 did not emit the `ldr x8` + `svc #0` sequence darwinize matches")
	}

	noReader := filepath.Join(t.TempDir(), "no_reader.fern")
	if err := os.WriteFile(noReader, []byte("function main(): i32 { let s: string = \"a\" + \"b\"; print(s); return s.len(); }\n"), 0o644); err != nil {
		t.Fatalf("write no-reader probe: %v", err)
	}
	nrOut, nrErr, nrCode := runDriverAllowFail(t, x86runner, mmc, "", noReader, "-target", "arm64-linux")
	if nrCode != 0 {
		t.Fatalf("self-host arm64 emit (no-reader probe) failed: exit %d\n%s", nrCode, nrErr)
	}
	nrAsm := string(nrOut)
	if !strings.Contains(nrAsm, "__fn___fern_str_concat:") {
		t.Fatal("the no-reader probe did not pull in the heap runtime — it cannot show the gate")
	}
	for _, leaf := range []string{"reader_read_chunk", "reader_close"} {
		if strings.Contains(nrAsm, "__fn___fern_"+leaf+":") {
			t.Errorf("__fn___fern_%s emitted for a program that holds no Reader — the need gate is not being consulted", leaf)
		}
	}
}

func TestSelfHostSyscallLeavesDarwinizedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc_darwin_rb")

	prog := "function main(): i32 {\n" +
		"    let b: u8[] = random_bytes(8);\n" +
		"    match (write_file(\"/tmp/fern_lockin_d.txt\", \"x\")) { Ok(_) => {}, Err(_) => { return 1; } }\n" +
		"    match (read_file(\"/tmp/fern_lockin_d.txt\")) { Ok(_) => {}, Err(_) => { return 2; } }\n" +
		"    match (remove_file(\"/tmp/fern_lockin_d.txt\")) { Ok(_) => {}, Err(_) => { return 3; } }\n" +
		"    match (temp_dir(\"lockin\")) { Ok(_) => {}, Err(_) => { return 4; } }\n" +
		"    match (stat(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 5; } }\n" +
		"    let xs: i32[] = [1, 2];\n" +
		"    if (xs[0:1].len() != 1) { return 6; }\n" +
		"    if (now_unix_ms() < (1577836800000 as i64)) { return 7; }\n" +
		"    if (monotonic_ns() < (0 as i64)) { return 8; }\n" +
		"    if (now_ns() < (0 as i64)) { return 9; }\n" +
		"    match (read_dir(\"/tmp\")) { Ok(_) => {}, Err(_) => { return 10; } }\n" +
		"    match (remove_dir_all(\"/tmp/fern_lockin_nodir\")) { Ok(_) => {}, Err(_) => { return 11; } }\n" +
		"    match (open_writer(\"/tmp/fern_lockin_w.txt\")) { Ok(_) => {}, Err(_) => { return 26; } }\n" +
		"    if (random_i32() == 0) { return 12; }\n" +
		"    if (tcp_close(999) == 0) { return 13; }\n" +
		"    if (tcp_accept(999) >= 0) { return 14; }\n" +
		"    sleep_ms(1);\n" +
		"    if (tcp_connect_with([127u8, 0u8, 0u8, 1u8], 1, false) == 0) { return 15; }\n" +
		"    if (tcp_recv_into(999, [0u8, 0u8, 0u8, 0u8]) >= 0) { return 16; }\n" +
		"    if (proc_fork() == 123456) { return 17; }\n" +
		"    write(\"x\");\n" +
		"    putchar(65);\n" +
		"    eprint(\"x\");\n" +
		"    let rd: Reader = stdin();\n" +
		"    match (rd.read_chunk(64)) { Ok(_) => {}, Err(_) => {} }\n" +
		"    rd.close();\n" +
		"    return b.len();\n" +
		"}\n"
	srcFile := filepath.Join(t.TempDir(), "rb_darwin.fern")
	if err := os.WriteFile(srcFile, []byte(prog), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	out, stderr, code := runDriverAllowFail(t, x86runner, mmc, "", srcFile, "-target", "arm64-darwin")
	if code != 0 {
		t.Fatalf("self-host arm64-darwin emit failed: exit %d\n%s", code, stderr)
	}
	asm := string(out)

	for _, c := range []struct {
		what string
		imm  string
	}{
		{"getentropy", "500"},
		{"openat", "463"},
		{"unlinkat", "472"},
		{"O_WRONLY|O_CREAT|O_TRUNC", "1537"},
		{"fstat", "339"},
		{"mkdirat", "475"},
		{"fstatat64", "470"},
		{"st_size offset", "96"},
		{"gettimeofday", "116"},
		{"getdirentries64", "344"},
		{"AT_REMOVEDIR", "128"},
		{"select", "93"},
		{"connect", "98"},
	} {
		if !hasBakedImm(asm, c.imm) {
			t.Errorf("the Darwin %s constant (%s) was not baked into the helper source", c.what, c.imm)
		}
	}
	for _, c := range []struct{ what, imm string }{
		{"O_RDONLY|O_DIRECTORY", "#16, lsl #16"},
	} {
		if !matchShape(asm, `\n    movz x[0-9]+, `+c.imm+`\n`) {
			t.Errorf("the Darwin %s constant (%s) was not baked into the helper source", c.what, c.imm)
		}
	}

	for _, leaf := range []struct{ sym, call, darwin, linux string }{
		{"__fn___fern_print_str", "write", "4", "64"},
		{"__fn___fern_putchar", "write", "4", "64"},
		{"__fn___fern_eprint_str", "writev", "121", "66"},
	} {
		sym := leaf.sym
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Errorf("%s not defined — the Fern helper did not lower for Darwin", sym)
			continue
		}
		if !matchShape(body, pushedImm(leaf.darwin)) {
			t.Errorf("%s does not push Darwin's %s number (%s)", sym, leaf.call, leaf.darwin)
		}
		if matchShape(body, pushedImm(leaf.linux)) {
			t.Errorf("%s pushes Linux's %s number (%s) in Mach-O output", sym, leaf.call, leaf.linux)
		}
		if strings.Contains(asm, "\n"+strings.TrimPrefix(sym, "__fn_")+":") {
			t.Errorf("the register-ABI hand-asm %s is back", strings.TrimPrefix(sym, "__fn_"))
		}
	}
	for _, sym := range []string{"__fn___fern_tcp_recv_into_u8", "__fn___fern_reader_read_chunk"} {
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Errorf("%s not defined — the Fern helper did not lower for Darwin", sym)
			continue
		}
		if !matchShape(body, pushedImm("3")) {
			t.Errorf("%s does not push Darwin's read number (3)", sym)
		}
		if matchShape(body, pushedImm("63")) {
			t.Errorf("%s pushes Linux's read number (63) in Mach-O output", sym)
		}
		if strings.Contains(asm, "\n"+strings.TrimPrefix(sym, "__fn_")+":") {
			t.Errorf("the register-ABI hand-asm %s is back", strings.TrimPrefix(sym, "__fn_"))
		}
	}
	{
		const sym = "__fn___fern_reader_close"
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Errorf("%s not defined — the Fern helper did not lower for Darwin", sym)
		} else {
			if !matchShape(body, pushedImm("6")) {
				t.Errorf("%s does not push Darwin's close number (6)", sym)
			}
			if matchShape(body, pushedImm("57")) {
				t.Errorf("%s pushes Linux's close number (57) in Mach-O output", sym)
			}
		}
		if strings.Contains(asm, "\n__fern_reader_close:") {
			t.Error("the register-ABI hand-asm __fern_reader_close is back")
		}
	}
	if strings.Contains(asm, "ldr x8, [sp], #16") {
		t.Error("darwinize left the __syscall3 number load on x8 (Linux form) in Mach-O output")
	}
	if !strings.Contains(asm, "    ldr x16, [sp], #16\n    svc #0x80\n") {
		t.Error("darwinize did not rewrite the __syscall3 sequence to the Darwin trap")
	}
	if !strings.Contains(asm, "    svc #0x80\n    b.cc ") {
		t.Error("darwinize did not emit the errno normalisation after the generic syscall")
	}
	for _, status := range []string{"125", "134"} {
		if !strings.Contains(asm, "    mov x0, #"+status+"\n    b __fern_report\n") {
			t.Errorf("the exit(%s) abort path does not branch to __fern_report", status)
		}
	}
	if !strings.Contains(asm, "    mov x0, #2\n    mov x16, #4\n    svc #0x80\n") {
		t.Error("__fern_report's write(2) still traps through the Linux vector on Mach-O")
	}
	if !strings.Contains(asm, "    mov x0, x19\n    mov x16, #1\n    svc #0x80\n") {
		t.Error("__fern_report's exit still traps through the Linux vector on Mach-O")
	}
	if strings.Contains(asm, "mov x8, #94") {
		t.Error("an exit kept Linux's number register in Mach-O output")
	}
	if !strings.Contains(asm, "__fn___fern_arr_slice:") {
		t.Error("__fn___fern_arr_slice not defined — the Fern helper did not lower for Darwin")
	}
	if strings.Contains(asm, "\n__fern_arr_slice:") {
		t.Error("the register-ABI hand-asm __fern_arr_slice is back")
	}
	for _, leaf := range []string{"now_unix_ms", "now_ns"} {
		if !strings.Contains(asm, "__fn___fern_"+leaf+":") {
			t.Errorf("__fn___fern_%s not defined — the Fern clock did not lower for Darwin", leaf)
		}
	}
	if !strings.Contains(asm, "__fn___fern_proc_fork:") {
		t.Error("__fn___fern_proc_fork not defined for Darwin")
	}
	if !strings.Contains(asm, "    cbz x1, Lpf_done\n") {
		t.Error("Darwin proc_fork lost the x1 child/parent discrimination — the reason it is not Fern")
	}
	if !strings.Contains(asm, "    mov x16, #2\n    svc #0x80\n") {
		t.Error("Darwin proc_fork is not trapping to BSD fork (2) through the Darwin vector")
	}
	if arm64Imm(asm, "220") {
		t.Error("Darwin proc_fork pushed Linux's clone number (220) in Mach-O output")
	}
	if !strings.Contains(asm, "    mrs x9, cntvct_el0\n    mrs x10, cntfrq_el0\n") {
		t.Error("Darwin monotonic_ns is not reading the architectural counter")
	}
	if strings.Contains(asm, "mov x8, #113") {
		t.Error("Linux's clock_gettime (113) reached the number register in Mach-O output")
	}
	for _, leaf := range []string{"now_unix_ms", "now_ns", "monotonic_ns"} {
		if arm64Imm(extractFuncBody(asm, "__fn___fern_"+leaf), "113") {
			t.Errorf("Darwin %s issued Linux's clock_gettime (113)", leaf)
		}
	}
	if body := extractFuncBody(asm, "__fn___fern_arr_slice"); body != "" {
		if !strings.Contains(body, "bl __fern_slice_abort\n") {
			t.Error("arr_slice's range check does not abort through __fern_slice_abort")
		}
		if matchShape(body, pushedImm("93")) || matchShape(body, pushedImm("1")) {
			t.Error("arr_slice pushes an exit number: its abort is the shared trap, not a syscall of its own")
		}
	}
	if !strings.Contains(asm, "__fern_slice_abort:\n") {
		t.Error("__fern_slice_abort not defined for Darwin")
	}
}

func hasBakedImm(asm, imm string) bool {
	n := regexp.QuoteMeta(imm)
	for _, form := range []string{`mov x[0-9]+, #` + n, `add x[0-9]+, x[0-9]+, #` + n, `sub x[0-9]+, x[0-9]+, #` + n, `cmp x[0-9]+, #` + n} {
		if regexp.MustCompile(`\n\s+` + form + `\n`).MatchString(asm) {
			return true
		}
	}
	return false
}

func pushedImm(n string) string {
	return `\n    mov (x[0-9]+), #` + regexp.QuoteMeta(n) + `\n(?:.*\n)*?    str \1, \[sp, #-16\]!\n`
}
