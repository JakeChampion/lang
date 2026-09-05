package e2eselfhost

import (
	"strings"
	"testing"
)

// The self-host half of #8065.
//
// `.L` is ELF's temporary-symbol prefix; Mach-O's is a bare `L`. Every `.L…`
// the self-host arm64 emitter wrote was therefore a REAL Mach-O symbol, and
// Mach-O atomizes a section at each one — so the assembler cannot treat the
// distance across a label as a compile-time constant, and CFI's epilogue
// rule stops assembling. The native emitter got the same fix in its printer;
// this is the self-host's, applied in `darwinize`, which is the pass that
// already reskins the listing for Mach-O and already walks it line by line.
//
// Both directions matter here. The Darwin listing must carry no `.L` outside
// string data, and the Linux listing must still carry them — otherwise this
// would pass by the emitter having stopped using local labels at all.

const darwinLabelSrc = `function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function main(): i32 { return fib(7); }`

// A program whose STRING contains ".L". A blind rewrite corrupts its data,
// and nothing else in the suite would notice.
const darwinLabelStrSrc = `function main(): i32 {
    print(".Lnot_a_label");
    return 0;
}`

func selfHostArm64Asm(t *testing.T, target string) string {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	out := runCaptureEnv(t, runner, bin, []byte(darwinLabelSrc),
		[]string{"PATH=/usr/bin:/bin"}, "-target", target)
	if len(out) == 0 {
		t.Fatalf("self-host emitted 0 bytes for %s", target)
	}
	return string(out)
}

// TestSelfHostArm64DarwinUsesMachOLocalPrefix: no ELF-style local label
// survives into the Mach-O listing.
func TestSelfHostArm64DarwinUsesMachOLocalPrefix(t *testing.T) {
	dar := selfHostArm64Asm(t, "arm64-darwin")
	if n := countOutsideStrings(dar, ".L"); n != 0 {
		t.Errorf("the self-host Darwin listing still has %d ELF-style .L labels outside string data", n)
	}
	if !strings.Contains(dar, "L") {
		t.Fatal("no labels at all in the Darwin listing; the fixture stopped exercising them")
	}
}

// TestSelfHostArm64LinuxKeepsDotL is the anti-vacuity twin: ELF still wants
// the dot, so the rewrite must be Darwin-only.
func TestSelfHostArm64LinuxKeepsDotL(t *testing.T) {
	lin := selfHostArm64Asm(t, "arm64-linux")
	if n := countOutsideStrings(lin, ".L"); n == 0 {
		t.Error("the self-host Linux listing lost its .L labels — the rewrite is not Darwin-only")
	}
}

// TestSelfHostArm64DarwinStringLiteralKeepsDotL is why the scan tracks
// quotes. The `.ascii` payload of a program's own string literal flows
// through the same line loop as every instruction.
func TestSelfHostArm64DarwinStringLiteralKeepsDotL(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	out := runCaptureEnv(t, runner, bin, []byte(darwinLabelStrSrc),
		[]string{"PATH=/usr/bin:/bin"}, "-target", "arm64-darwin")
	if !strings.Contains(string(out), ".Lnot_a_label") {
		t.Error(`the string literal ".Lnot_a_label" was rewritten in the self-host Darwin listing — string data is not a symbol`)
	}
}

// A program whose STRING contains a `:lo12:` operand. This is not contrived:
// the self-host compiler's own emitter literals are exactly this shape, and
// rewriting them is how the arm64-darwin stage-2 compiler came to emit a
// listing 6 bytes out of step at every `:lo12:` (#8400). The `.ascii`
// directive keeps its payload; no `@PAGEOFF` is appended to a data line.
const darwinLo12StrSrc = `function main(): i32 {
    print("    add x9, x9, :lo12:.Lfern_relanchor");
    return 0;
}`

// TestSelfHostArm64DarwinStringLiteralKeepsLo12 is the `:lo12:` twin of
// KeepsDotL: the operand rewrite must skip quoted regions too.
func TestSelfHostArm64DarwinStringLiteralKeepsLo12(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	out := string(runCaptureEnv(t, runner, bin, []byte(darwinLo12StrSrc),
		[]string{"PATH=/usr/bin:/bin"}, "-target", "arm64-darwin"))
	if !strings.Contains(out, `:lo12:.Lfern_relanchor"`) {
		t.Error(`the string literal's ":lo12:" was rewritten in the self-host Darwin listing - string data is not an operand`)
	}
	if n := countOutsideStrings(out, ":lo12:"); n != 0 {
		t.Errorf("the self-host Darwin listing still has %d :lo12: operands outside string data", n)
	}
	if n := countOutsideStrings(out, `"@PAGEOFF`); n != 0 {
		t.Errorf("%d .ascii data lines carry a @PAGEOFF suffix in the self-host Darwin listing", n)
	}
}

// countOutsideStrings counts occurrences of sub that are not inside a
// double-quoted region, line by line. The distinction is the whole point: a
// ".L" inside a quoted operand is a program's string data, not a symbol.
func countOutsideStrings(asm, sub string) int {
	n := 0
	for _, line := range strings.Split(asm, "\n") {
		inStr := false
		for i := 0; i < len(line); i++ {
			if inStr {
				if line[i] == '\\' {
					i++
					continue
				}
				if line[i] == '"' {
					inStr = false
				}
				continue
			}
			if line[i] == '"' {
				inStr = true
				continue
			}
			if strings.HasPrefix(line[i:], sub) {
				n++
			}
		}
	}
	return n
}
