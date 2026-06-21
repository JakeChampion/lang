package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// closureArrayAppendIRCases pin building a closure array with `.append` starting
// from an EMPTY literal — `var fns: (() => i32)[] = []; fns = fns.append(() => …)`
// — on the self-host IR path (x86-64 + wasm). This is the #3556 fix: the empty
// closure-array literal carries the type annotation `(() => i32)[]` (coarsed to
// "fn[]" by parse_type_name), but `is_closurearr` was only derived from a
// non-empty literal's first element, so the slot was left unmarked and a later
// `fns[i]()` read the element as a raw value instead of unpacking the closure box
// — trapping (IR) / returning garbage (AST). irlower now marks the slot from the
// "fn[]" annotation too, so the append + indexed call work.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
var closureArrayAppendIRCases = []struct {
	name string
	main string
	want int
}{
	// empty init, one capturing append, indexed call.
	{"append-one", `function main(): i32 { var n = 4; var fns: (() => i32)[] = []; fns = fns.append(() => n); return fns[0](); }`, 4},
	// two appends, sum both: 4 + 5 = 9.
	{"append-two-sum", `function main(): i32 { var n = 4; var fns: (() => i32)[] = []; fns = fns.append(() => n); fns = fns.append(() => n + 1); return fns[0]() + fns[1](); }`, 9},
	// one-arg capturing closures appended: (a)=>a+b and (a)=>a*b, b=10 -> 15+20 = 35.
	{"append-two-arg", `function main(): i32 { var b = 10; var fns: ((i32) => i32)[] = []; fns = fns.append((a: i32) => a + b); fns = fns.append((a: i32) => a * b); return fns[0](5) + fns[1](2); }`, 35},
	// .len() of the appended array plus a call: 2*10 + 1 = 21.
	{"append-len-and-call", `function main(): i32 { var n = 1; var fns: (() => i32)[] = []; fns = fns.append(() => n); fns = fns.append(() => n); return fns.len() * 10 + fns[1](); }`, 21},
	// regression: a non-empty closure-array literal still works.
	{"literal-still-works", `function main(): i32 { var n = 4; var fns: (() => i32)[] = [() => n]; return fns[0](); }`, 4},
	// regression: literal init followed by an append.
	{"literal-then-append", `function main(): i32 { var n = 4; var fns: (() => i32)[] = [() => n]; fns = fns.append(() => n + 1); return fns[0]() + fns[1](); }`, 9},
}

func closureArrayAppendIRSrc(mainBody string) string {
	return mainBody + "\n"
}

// TestSelfHostClosureArrayAppendIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostClosureArrayAppendIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range closureArrayAppendIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(closureArrayAppendIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostClosureArrayAppendIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostClosureArrayAppendIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-array-append wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range closureArrayAppendIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(closureArrayAppendIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "closure_array_append_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("closure-array-append wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
