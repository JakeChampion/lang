package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/u64's RECEIVER methods (min / max / clamp / to_string) on the self-host IR
// path. The importless #2904 / u64-IR families exercise the unsigned u64
// operators and inlined free functions, but a program that `import`s std/u64 and
// calls its *methods* used to route the legacy AST fallback: the self-host
// method dispatcher (`expr_recv_prim_type`) classified ANY 64-bit receiver as
// "i64", so a u64 receiver dispatched to a nonexistent "i64.<m>" label and
// bailed the whole module to AST (calls_only_known). The fix adds a u64 branch
// (mirroring method_recv_tyname) and lowers the u64 receiver full-width.
//
// These pin that std/u64's method surface now lowers on IR — each routes "ir"
// through the self-hosted x86-64 loader (asm_load_run) with the real stdlib as
// the root and matches the native interpreter, including high-bit-set values
// (> 2^63) where a signed misread would pick the wrong branch / lose digits.
var u64MethodCases = []struct {
	name string
	src  string
}{
	{"min", `import "std/u64";
function main(): i32 { var a: u64 = 7 as u64; var b: u64 = 3 as u64; return a.min(b) as i32; }`},
	{"max", `import "std/u64";
function main(): i32 { var a: u64 = 7 as u64; var b: u64 = 3 as u64; return a.max(b) as i32; }`},
	// max with a high-bit-set operand (>= 2^63): the internal compare must be
	// unsigned, so it picks `a`; a.max(b) % 100 = ...007 % 100 = 7.
	{"max-highbit", `import "std/u64";
function main(): i32 { var a: u64 = 18000000000000000007 as u64; var b: u64 = 9 as u64; return (a.max(b) % (100 as u64)) as i32; }`},
	// clamp with a high-bit-set hi bound: n (=50) stays within [10, big] → 50,
	// only if the `n > hi` compare is unsigned.
	{"clamp-highbit-hi", `import "std/u64";
function main(): i32 { var hi: u64 = 18000000000000000000 as u64; return (50 as u64).clamp(10 as u64, hi) as i32; }`},
	// to_string just above the u32 range (2^32): 10 digits, all kept.
	{"to_string-2p32", `import "std/u64";
function main(): i32 { var n: u64 = 4294967296 as u64; if (n.to_string() == "4294967296") { return 42; } return 0; }`},
	// to_string of a high-bit-set value (> 2^63, 20 digits) — exact match.
	{"to_string-highbit", `import "std/u64";
function main(): i32 { var n: u64 = 18000000000000000007 as u64; if (n.to_string() == "18000000000000000007") { return 42; } return n.to_string().len(); }`},
	// A USER struct method RETURNING u64, chained directly in an unsigned op
	// where the call is the sole u64 operand (`p.big() >> 57`). expr_is_u64
	// gained an ExprFieldAccess arm (the method sibling of the concrete-free-fn
	// #5172 and IIFE cases) so the shift stays unsigned: 0xF9CCD8A1C5080000 >> 57
	// is 124 unsigned, 252 (sign-extended low byte) signed.
	{"user-method-u64-shift", `struct P { n: u64 }
impl P { function big(self: Self): u64 { return self.n; } }
function main(): i32 { var p: P = P { n: 18000000000000000000 as u64 }; return (p.big() >> 57) as i32; }`},
}

func TestSelfHostU64MethodsIR(t *testing.T) {
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

	for _, tc := range u64MethodCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "u64m_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "u64m_"+tc.name+"_bin", asm)
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
