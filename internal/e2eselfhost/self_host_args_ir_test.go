package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// argsIRCases exercise the `args()` builtin through the IR path on x86-64,
// arm64, and wasm. args() lowers to a value IR op that calls each backend's
// __fern_args helper, pushing a fresh string[] — argv[0] first. On the register
// backends the helper reads the __fern_argc / __fern_argv globals the entry's
// _start saves from the initial stack; on wasm it reads the wasi args_get
// import. `argc` returns a.len(); `index` returns a[1].len() (exercising the
// str-array tracking that makes a[i] read a string box and .len() dispatch to
// str_len).
var argsIRCases = []struct {
	name, src string
	extraArgs []string
	wantExit  int
}{
	{"argc-0", `function main(): i32 { var a: string[] = args(); return a.len(); }`, nil, 1},
	{"argc-2", `function main(): i32 { var a: string[] = args(); return a.len(); }`, []string{"x", "y"}, 3},
	{"index", `function main(): i32 { var a: string[] = args(); if (a.len() < 2) { return 0; } var f: string = a[1]; return f.len(); }`, []string{"hello"}, 5},
}

// argAtIRCases exercise `arg_at(i)` — the O(1) single-argv read — through the IR
// path on the REGISTER backends (#3457). It is a dedicated op rather than a
// desugar to `args()[i]` precisely because of the loop case below: args()
// allocates the whole argv vector, so the desugar would make an n-argument loop
// quadratic where the AST path is linear. The op calls the same register-ABI
// __fern_arg_at helper the AST emitters do, which the `args` need already emits.
//
// wasm is absent on purpose: it has no arg_at on EITHER path (wasm.fern
// implements args() from args_get but never an indexed read), so an arg_at
// module is deferred to the wasm AST path — pinned by TestSelfHostArgAtDefersWasm.
var argAtIRCases = []struct {
	name, src string
	extraArgs []string
	wantExit  int
}{
	// argv[0] is the program path, so its length is nonzero but not fixed;
	// assert the shape rather than the value.
	{"argv0-nonempty", `function main(): i32 { if (arg_at(0).len() > 0) { return 1; } return 0; }`, nil, 1},
	{"index-1", `function main(): i32 { return arg_at(1).len(); }`, []string{"hello"}, 5},
	{"index-2", `function main(): i32 { return arg_at(2).len(); }`, []string{"x", "abcd"}, 4},
	// The string box is a real string: it compares and concatenates.
	{"compare", `function main(): i32 { if (arg_at(1) == "go") { return 7; } return 3; }`, []string{"go"}, 7},
	{"compare-miss", `function main(): i32 { if (arg_at(1) == "go") { return 7; } return 3; }`, []string{"stop"}, 3},
	{"concat-len", `function main(): i32 { return (arg_at(1) + "!").len(); }`, []string{"ab"}, 3},
	// The case the op exists for: an argv loop. With the args()[i] desugar this
	// would allocate the entire argv per iteration.
	{"loop-sum-lens", `function main(): i32 { var t: i32 = 0; var i: i32 = 1; while (i < args_count()) { t = t + arg_at(i).len(); i = i + 1; } return t; }`, []string{"abc", "defghi"}, 9},
}

func TestSelfHostArgsIRX86_64(t *testing.T) {
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
	for _, tc := range argsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_args")) {
				t.Fatalf("%s: no call to __fern_args — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin, tc.extraArgs...)
			} else {
				cmd = exec.Command(runner[0], append(append(runner[1:], progBin), tc.extraArgs...)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}

// TestSelfHostArgAtIRX86_64 is the arg_at sibling of TestSelfHostArgsIRX86_64.
// The `call __fern_arg_at` assertion is what separates the op from the desugar:
// an `args()[i]` lowering would emit `call __fern_args` and index the result,
// which passes every exit-code case here while allocating argv per read.
func TestSelfHostArgAtIRX86_64(t *testing.T) {
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
	for _, tc := range argAtIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte(".Lir_main_")) {
				t.Fatalf("%s: no `.Lir_main_` label — the module bailed to the AST emitter", tc.name)
			}
			if !bytes.Contains(asm, []byte("call __fern_arg_at")) {
				t.Fatalf("%s: no call to __fern_arg_at — arg_at did not lower to its own op", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin, tc.extraArgs...)
			} else {
				cmd = exec.Command(runner[0], append(append(runner[1:], progBin), tc.extraArgs...)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}

// TestSelfHostArgsIRWasm runs the same cases through the wasm IR backend under
// wasmtime, which supplies argv (argv[0] is the program name). args() now
// lowers on the wasm IR path: wasm_ir emits `call $__fern_args` and wasm_ir_run
// pulls in the wasi args_sizes_get / args_get imports + the args_func helper
// (the runtime the AST path already used) when the module reads argv. The extra
// args sit after the module path on the wasmtime command line.
func TestSelfHostArgsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host args wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range argsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_args")) {
				t.Fatalf("%s: no `call $__fern_args` — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "aw_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", append([]string{"run", watFile}, tc.extraArgs...)...)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("args wasm IR %s: exit %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}

func TestSelfHostArgsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm.fern", "asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range argsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_args")) {
				t.Fatalf("%s: no bl __fern_args — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ar_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin, tc.extraArgs...)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("args arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}

// TestSelfHostArgAtIRArm64 mirrors the x86 leg: same op, same helper, the arm64
// stack ABI (index off the rt-stack into x0, box back in x0). The helper body is
// asm_arm64.emit_runtime's, gated on the same `args` need op_args marks — so a
// missing need shows up here as an undefined reference at link, not a wrong exit.
func TestSelfHostArgAtIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm.fern", "asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range argAtIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_arg_at")) {
				t.Fatalf("%s: no `bl __fern_arg_at` — arg_at did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "aat_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin, tc.extraArgs...)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("arg_at arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}
