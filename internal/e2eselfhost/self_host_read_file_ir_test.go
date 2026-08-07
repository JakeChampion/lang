package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// localLabelRe matches a self-host control-flow label — the x86-64 `.Lir_*`
// emitter (asm_ir.fern) and the arm64 `.Lira_*` emitter (asm_arm64_ir.fern).
var localLabelRe = regexp.MustCompile(`\.Lira?_[A-Za-z0-9_.]+`)

// assertNoDanglingLocalLabels fails if the emitted asm references a `.Lir_*` /
// `.Lira_*` control-flow label it never defines — the exact dangling-label link
// failure of issue #4442 (`undefined reference to .Lir_main_13`), caught here as
// a clear test error naming the label instead of a downstream gcc/ld crash. A
// definition is a line `<label>:`; every other occurrence is a reference.
func assertNoDanglingLocalLabels(t *testing.T, ctx string, asm []byte) {
	t.Helper()
	defined := map[string]bool{}
	referenced := map[string]bool{}
	for _, line := range strings.Split(string(asm), "\n") {
		trimmed := strings.TrimSpace(line)
		if lbl, ok := strings.CutSuffix(trimmed, ":"); ok && localLabelRe.MatchString(lbl) && !strings.ContainsAny(lbl, " \t") {
			defined[lbl] = true
			continue
		}
		for _, m := range localLabelRe.FindAllString(line, -1) {
			referenced[m] = true
		}
	}
	var dangling []string
	for r := range referenced {
		if !defined[r] {
			dangling = append(dangling, r)
		}
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		t.Fatalf("%s: dangling local label(s) referenced but never defined: %v", ctx, dangling)
	}
}

// readFileIRCases exercise the `read_file(path)` builtin through the IR path on
// x86-64, arm64, and wasm (the wasm IR path opens the path under preopen fd 3 —
// run with `--dir`). read_file lowers to a value IR op that pops the path string box and
// calls each backend's read_file helper — `__fn___fern_read_file` on the two
// register backends, the WAT `$__fern_read_file` on wasm — pushing a fresh
// Result[string, IoError] box — so `match (read_file(p)) { Ok(s) => …, Err(e)
// => … }` lowers like any other Result (the Result type is recognised by
// opt_ret_type's read_file fallback).
//
// The harness writes "hello" (5 bytes, no newline) to rf_data.txt in the run
// directory. `len` returns the Ok contents' length (exercising the str-tracking
// that makes `.len()` dispatch to str_len); `echo` writes the contents; `missing`
// reads a non-existent file and takes the Err arm.
var readFileIRCases = []struct {
	name, src, wantOut string
	wantExit           int // used when wantOut == ""
}{
	{"len", `function main(): i32 { match (read_file("rf_data.txt")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } return 0; }`, "", 5},
	{"echo", `function main(): i32 { match (read_file("rf_data.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 0; }`, "hello", 0},
	{"missing", `function main(): i32 { match (read_file("rf_nope.txt")) { Ok(s) => { return 0; }, Err(e) => { return 42; } } return 0; }`, "", 42},
	{"bind", `function main(): i32 { var r = read_file("rf_data.txt"); match (r) { Ok(s) => { return s.len(); }, Err(e) => { return 7; } } return 0; }`, "", 5},
}

func writeRFData(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "rf_data.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write rf_data.txt: %v", err)
	}
}

func TestSelfHostReadFileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range readFileIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			// Both register backends now serve read_file as a Fern runtime
			// function (#2649): x86-64 first, arm64 in #6352, so op_read_file
			// calls the stack-ABI __fn___fern_read_file on each. wasm keeps its
			// WAT helper $__fern_read_file — see the wasm leg below.
			if !bytes.Contains(asm, []byte("call __fn___fern_read_file")) {
				t.Fatalf("%s: no call to __fn___fern_read_file — did not lower through the IR path", tc.name)
			}
			assertNoDanglingLocalLabels(t, tc.name, asm)
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Dir = dir
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			_ = cmd.Run()
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("%s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("%s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

// TestSelfHostReadFileIRWasm runs the same cases through the wasm IR backend
// under wasmtime, granting the run directory as preopen fd 3 (`--dir=.::/`).
// read_file now lowers on the wasm IR path: wasm_ir emits `call $__fern_read_file`
// and wasm_ir_run pulls in the path_open / fd_read / fd_close imports + the
// readfile_func helper (the runtime the AST path already used).
func TestSelfHostReadFileIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range readFileIRCases {
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
			if !bytes.Contains(wat, []byte("call $__fern_read_file")) {
				t.Fatalf("%s: no `call $__fern_read_file` — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "rf_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
			run.Dir = dir
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_file wasm IR %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_file wasm IR %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

func TestSelfHostReadFileIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range readFileIRCases {
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
			// __fn___fern_read_file, not __fern_read_file: #6352 moved arm64's
			// read_file to a Fern runtime function like x86-64's, and a Fern
			// function is emitted under the `__fn_` prefix. This assertion kept
			// the pre-migration name and so went red on every branch.
			if !bytes.Contains(asm, []byte("bl __fn___fern_read_file")) {
				t.Fatalf("%s: no bl __fn___fern_read_file — did not lower through the arm64 IR path", tc.name)
			}
			assertNoDanglingLocalLabels(t, "arm64 "+tc.name, asm)
			bin := buildBinArm64(t, arm64gcc, dir, "rf_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			run.Dir = dir
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_file arm64 %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_file arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}
