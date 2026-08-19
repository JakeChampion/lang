package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 64-bit integer formatting with magnitudes > 2^32 on the self-host IR path.
//
// core/int's `__int_to_string_u64` (behind std/i64 / std/u64 `to_string`)
// divides its magnitude by 10 in a loop via `(mag as u64) / (10 as u64)`. The
// self-host IR lowering of `as i64` / `as u64` on a NON-literal operand used the
// 32-bit `lower_expr` path and then `op_int_extend`, which truncated an
// already-64-bit operand to its low 32 bits before re-widening — so any value
// above 2^32 formatted wrong (a high i64 lost its top half; e.g. 9876543210
// printed as if it were 9876543210 mod 2^32). i64/u64 literals were unaffected
// (they take the `const_i64_text` path). The fix routes an operand that is
// already 64-bit (`infer_expr_width == 64`) through `lower_i64` — `i64 as u64`
// is a pure reinterpret, no truncating extend.
//
// These cases pin the IR path: each routes "ir" through the self-hosted x86-64
// loader (asm_load_run) with the real stdlib root and matches the native
// interpreter. (std/u64's `to_string` does not lower — a separate,
// out-of-IR-subset concern — so the high-bit u64 case here calls
// core/int's `__int_to_string_u64` directly, which does lower on the IR path.)
var i64ToStringIRCases = []struct {
	name string
	src  string
}{
	// std/i64 to_string of a large NEGATIVE value (magnitude > 2^32). The sign
	// path plus the >2^32 div/mod chain.
	{"i64-neg-large", `import "std/i64";
function main(): i32 { var n: i64 = 0 - (9876543210 as i64); if (n.to_string() == "-9876543210") { return 42; } return 0; }`},
	// std/i64 to_string of a large POSITIVE value (> 2^32).
	{"i64-pos-large", `import "std/i64";
function main(): i32 { var n: i64 = 9876543210 as i64; if (n.to_string() == "9876543210") { return 42; } return 0; }`},
	// core/int's u64 formatter on a high-bit-set value (> 2^63): `n as i64` is a
	// negative i64 whose bits are the full unsigned magnitude; the unsigned
	// div/mod must keep all 64 bits.
	{"u64-highbit-direct", `import "core/int";
function main(): i32 { var n: u64 = 18000000000000000007 as u64; if (int.__int_to_string_u64(n as i64, 0) == "18000000000000000007") { return 42; } return 0; }`},
	// the underlying `(i64-var as u64) % k` — the exact shape the formatter loop
	// uses. Truncation to 32 bits would give 1286608618 % 7 = 1, not 2.
	{"i64-as-u64-mod", `import "core/int";
function main(): i32 { var m: i64 = 9876543210 as i64; return (m as u64 % (7 as u64)) as i32; }`},
}

func TestSelfHostI64ToStringIR(t *testing.T) {
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

	for _, tc := range i64ToStringIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "i64s_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// Oracle: the native interpreter's exit code.
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "i64s_"+tc.name+"_bin", asm)
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
