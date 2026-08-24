package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// structLenMethodIRCases pin the #3478 fix: a user-defined receiver method named
// `len` on a struct must shadow the builtin `.len()`, not be intercepted as a
// string/array length read. Pre-fix, irlower.fern intercepted *every* zero-arg
// `.len()` call and — for a struct receiver, which isn't a string — fell through
// to `op_arr_len`, reading the struct box as an array header (garbage: x86-64
// returned 26, the local lookup-string length; wasm returned 0). The two cases
// have identical bodies (`return b.items.len();`) and differ only in the method
// NAME (`len` vs `count`); pre-fix `len` diverged from `count` on the self-host
// IR path while both are correct natively. Pinned to the `"ir"` path; hardcoded
// expectations verified against the native x86-64 backend.
const structLenMethodIRPrelude = `struct Box { items: string[] }
function helper(s: string): string {
    var alpha: string = "abcdefghijklmnopqrstuvwxyz";
    return slice_unchecked(alpha, 0, 1) + s;
}
function (b: Box) add(x: string): Box { return Box { ...b, items: b.items.append(helper(x)) }; }
function (b: Box) len(): i32 { return b.items.len(); }
function (b: Box) count(): i32 { return b.items.len(); }
`

var structLenMethodIRCases = []struct {
	name string
	main string
	want int
}{
	// user method named `len` must shadow the builtin (#3478): two adds -> 2.
	{"len-method", `var b: Box = Box { items: [] }; b = b.add("a"); b = b.add("b"); return b.len();`, 2},
	// control: a differently-named method with the same body is unaffected.
	{"count-method", `var b: Box = Box { items: [] }; b = b.add("a"); b = b.add("b"); return b.count();`, 2},
	// the builtin array `.len()` on a real array still works (no regression).
	{"builtin-arr-len", `var a: i32[] = [10, 20, 30]; return a.len();`, 3},
	// the builtin string `.len()` still works (no regression).
	{"builtin-str-len", `var s: string = "hello"; return s.len();`, 5},
}

func structLenMethodIRSrc(mainBody string) string {
	return structLenMethodIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostStructLenMethodIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostStructLenMethodIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range structLenMethodIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(structLenMethodIRSrc(tc.main))
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

// TestSelfHostStructLenMethodIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStructLenMethodIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host struct-len-method wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structLenMethodIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(structLenMethodIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "structlen_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("struct-len-method wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
