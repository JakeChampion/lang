package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// core/int's decimal formatters (`int_to_string` for i32, `__int_to_string_u64`
// behind std/u32 / std/u64 / std/i64's `to_string`) on the self-host IR path.
//
// These functions write the digits backwards into a scratch `u8[]` then copy the
// right-aligned tail into a correctly-sized buffer. They USED to do that copy
// with a raw `__memcpy(buf as usize, scratch as usize + end, n)` — a packed-byte
// copy that is correct on the native / wasm runtimes (whose `u8[]` is one byte
// per element) but MISCOMPILES on the self-hosted runtime, whose `u8[]` stores
// each element in an 8-byte slot. There, `arr as usize` points at the length
// word and the contiguous byte copy reads the wrong memory, so every
// `to_string` produced an EMPTY string (and any program that formatted an
// integer through the self-host compiler silently returned the wrong value).
//
// The fix replaced the `__memcpy` tail with a slot-aware element copy
// (`buf.with(i, scratch[end + i])`), which is layout-agnostic — correct on every
// backend AND lowering on the IR path. These cases pin that: each routes "ir"
// through the self-hosted x86-64 loader (asm_load_run) with the real stdlib as
// the root, and matches the native interpreter (whose builtin formatter is the
// known-correct oracle). Each program makes a SINGLE formatting call (sequencing
// several string compares in one program hits an unrelated wasm-only quirk, cf.
// the sweep in core/int's own tests).
var intToStringIRCases = []struct {
	name string
	src  string
}{
	// i32 int_to_string: length of "425".
	{"i32-len", `import "core/int";
function main(): i32 { return int.int_to_string(425).len(); }`},
	// i32 negative content: "-12345".
	{"i32-neg", `import "core/int";
function main(): i32 { if (int.int_to_string(0 - 12345) == "-12345") { return 42; } return 0; }`},
	// i32 zero content: "0".
	{"i32-zero", `import "core/int";
function main(): i32 { if (int.int_to_string(0) == "0") { return 7; } return 0; }`},
	// u32 to_string at the unsigned max (4294967295 → 10 digits): routes through
	// __int_to_string_u64 via the `(n as i64) & mask` reinterpret.
	{"u32-max-len", `import "std/u32";
function main(): i32 { var n: u32 = 4294967295 as u32; return n.to_string().len(); }`},
	// u32 to_string content for a mid-range value.
	{"u32-content", `import "std/u32";
function main(): i32 { var n: u32 = 305419896 as u32; if (n.to_string() == "305419896") { return 42; } return 0; }`},
}

func TestSelfHostIntToStringIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range intToStringIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "its_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// Oracle: the native interpreter's exit code (its builtin formatter
			// is the known-correct reference).
			_, want := runFixtureInterp(t, entry, "")
			// Loading the stdlib auto-applies treeshake → the merged module fits
			// the budget and routes IR.
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "its_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
