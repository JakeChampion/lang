package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// u64UnsignedCases are the shared #2904 programs exercised on every backend
// (x86-64 here, arm64 + wasm in their siblings). Each one is bit-63-sensitive,
// so its answer differs between the signed and unsigned op — proving both that
// the unsigned op is selected AND that the program took the IR path (the AST
// fallback on these routes has no u64 unsigned handling).
var u64UnsignedCases = []struct {
	name string
	src  string
}{
	// Unsigned `>`: 2^63 (bit 63 set) > 1 is TRUE unsigned, FALSE signed (2^63
	// reads as a negative i64). Returns 7 iff the unsigned compare is selected.
	{"gt-u", `function main(): i32 { var one: u64 = 1 as u64; var big: u64 = one << 63; if (big > one) { return 7; } return 0; }`},
	// Unsigned `<`: 1 < 2^63 is TRUE unsigned (signed: 1 < negative is FALSE).
	{"lt-u", `function main(): i32 { var one: u64 = 1 as u64; var big: u64 = one << 63; if (one < big) { return 7; } return 0; }`},
	// Logical `>>`: 2^63 >> 62 == 2 (unsigned); a signed >> sign-fills to a
	// large value whose low bits differ. Returns 2.
	{"shr-logical", `function main(): i32 { var one: u64 = 1 as u64; var big: u64 = one << 63; var sh: u64 = big >> 62; return sh as i32; }`},
	// Unsigned `/`: 2^63 / 2 == 2^62 (unsigned). Signed idiv of the negative-
	// looking 2^63 differs. Compared against 1<<62.
	{"div-u", `function main(): i32 { var one: u64 = 1 as u64; var big: u64 = one << 63; var q: u64 = big / (2 as u64); if (q == (one << 62)) { return 9; } return 0; }`},
	// Unsigned `%`: (2^63 + 5) % (2^63) == 5 unsigned. Build the dividend by
	// addition so it is a genuine bit-63-set u64.
	{"rem-u", `function main(): i32 { var one: u64 = 1 as u64; var big: u64 = one << 63; var n: u64 = big + (5 as u64); var r: u64 = n % big; return r as i32; }`},
	// u64 PARAM + RETURN through a called function (unsigned max), then a
	// logical shift on the result kept in a u64 local. Exercises the i64-domain
	// param/return signature path plus expr_is_u64 on the local.
	{"param-ret", `function umax(a: u64, b: u64): u64 { if (a > b) { return a; } return b; } function main(): i32 { var one: u64 = 1 as u64; var big: u64 = one << 63; var m: u64 = umax(big, one); var sh: u64 = m >> 62; return sh as i32; }`},
	// Combined: unsigned compare + logical shift + unsigned div in one program
	// (the #2904 repro), summed as 1 + 10 + 100 = 111.
	{"combined", `function main(): i32 {
		var one: u64 = 1 as u64;
		var big: u64 = one << 63;
		var r: i32 = 0;
		if (big > one) { r = r + 1; }
		var sh: u64 = big >> 62;
		if (sh == (2 as u64)) { r = r + 10; }
		var q: u64 = big / (2 as u64);
		if (q == (one << 62)) { r = r + 100; }
		return r;
	}`},
}

// TestSelfHostU64UnsignedIR proves the self-hosted x86-64 IR path treats u64 as
// UNSIGNED in the 64-bit (i64) domain (#2904): its `<`/`>`/`<=`/`>=` compares,
// `>>`, and `/`/`%` select the unsigned ops (lt_u / shr_u / div_u / …) instead
// of the signed ones, which are wrong once bit 63 is set. u64 piggybacks the
// i64 lowering (same 8-byte slots / register width); only signedness differs,
// and there is no wrap step (i64 ops are already full-width).
//
// The oracle is the NATIVE compiler (compileAndRunX86_64), NOT the AST path:
// for a bit-63-set program the signed-vs-unsigned answer differs, so IR ==
// native also proves the program took the IR path rather than the AST fallback
// (which has no u64 unsigned handling on this route).
func TestSelfHostU64UnsignedIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	for _, tc := range u64UnsignedCases {
		t.Run(tc.name, func(t *testing.T) {
			_, want := compileAndRunX86_64(t, tc.src) // native = the correct oracle
			if got := emitAndRunIR(t, tc.src); got != want {
				t.Errorf("self-host IR u64 %q: exit = %d, want %d (native)", tc.name, got, want)
			}
		})
	}
}
