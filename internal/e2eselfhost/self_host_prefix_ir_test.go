package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// prefixIRCases exercise the Eq-driven `starts_with` / `ends_with` array
// prefix/suffix checks through the self-host IR path on x86-64 + wasm.
// The bare driver resolves no imports, so the surface is inlined: an `Eq`
// trait + i32 / string primitive impls and the `[T: Eq]` bodies. Both
// compare with the `==` operator (scalar compare / `str_eq`, not a method
// call), so they monomorphise and lower cleanly per element type on both
// backends. Each case returns a small deterministic int (<= 125, wasm
// exit-code safe), oracle-checked against the interpreter. #2689.
const prefixIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function starts_with[T: Eq](xs: T[], prefix: T[]): boolean {
    if (prefix.len() > xs.len()) { return false; }
    var i: i32 = 0;
    while (i < prefix.len()) {
        if (xs[i] != prefix[i]) { return false; }
        i = i + 1;
    }
    return true;
}
pub function ends_with[T: Eq](xs: T[], suffix: T[]): boolean {
    var n: i32 = xs.len();
    var m: i32 = suffix.len();
    if (m > n) { return false; }
    var i: i32 = 0;
    while (i < m) {
        if (xs[n - m + i] != suffix[i]) { return false; }
        i = i + 1;
    }
    return true;
}
`

var prefixIRCases = []struct {
	name string
	main string
	want int
}{
	// starts_with i32: match + non-match + too-long -> 1 + 2 + 4 = 7.
	{"starts-i32", `var a: i32[] = [1, 2, 3, 4, 5]; var r: i32 = 0; if (starts_with(a, [1, 2, 3])) { r = r + 1; } if (!starts_with(a, [1, 3])) { r = r + 2; } if (!starts_with([1, 2], [1, 2, 3])) { r = r + 4; } return r;`, 7},
	// ends_with i32: match + non-match -> 1 + 2 = 3.
	{"ends-i32", `var a: i32[] = [1, 2, 3, 4, 5]; var r: i32 = 0; if (ends_with(a, [4, 5])) { r = r + 1; } if (!ends_with(a, [3, 5])) { r = r + 2; } return r;`, 3},
	// string element prefix via str_eq -> 8 + 4 = 12.
	{"starts-string", `var ss: string[] = ["a", "b", "c", "d"]; var r: i32 = 0; if (starts_with(ss, ["a", "b"])) { r = r + 8; } if (!starts_with(ss, ["a", "c"])) { r = r + 4; } return r;`, 12},
	// string element suffix via str_eq -> 9.
	{"ends-string", `var ss: string[] = ["a", "b", "c", "d"]; var r: i32 = 0; if (ends_with(ss, ["c", "d"])) { r = r + 8; } if (!ends_with(ss, ["b", "d"])) { r = r + 1; } return r;`, 9},
}

func prefixIRSrc(mainBody string) string {
	return prefixIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostPrefixIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostPrefixIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range prefixIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(prefixIRSrc(tc.main))
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

// TestSelfHostPrefixIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostPrefixIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host prefix wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range prefixIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(prefixIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "prefix_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("prefix wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
