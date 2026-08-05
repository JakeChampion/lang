package e2eselfhost

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
)

// TestSelfHostArm64AsmEncodingMatchesNative pins the SELF-HOST in-process arm64
// assembler's instruction encodings to the NATIVE Go assembler's, on identical
// GAS text.
//
// # Why an encoding test, when execution tests exist
//
// #6047: `arm64_gas_mem` read the index REGISTER of `ldrb w0, [x0, x1]` as an
// immediate — the digit lifted out of the register name — so the instruction
// assembled as `ldrb w0, [x0, #1]`. The binary was well-formed and ran; every
// string-copy loop just reloaded the source's second byte forever, so
// `print("Hello, Fern!")` emitted "eeeeeeeeeeeeF": the right LENGTH, the wrong
// bytes. Nothing caught it for as long as it existed, because:
//
//   - the fixture corpus never ran through the self-host arm64 path at all (the
//     arm64 leg of #6005 is what found it), and
//   - TestSelfHostArm64LinuxBuilds, the one test that executes this path, has a
//     `print` case that compared only the EXIT CODE, and skips entirely unless
//     qemu-aarch64 is installed — which the CI job running it does not do.
//
// This test needs no qemu and executes no arm64 code. It compares encodings,
// which is the layer the bug was in, and it fails on any divergence rather than
// only on the ones that happen to change an exit code.
//
// # Why the native assembler is a valid oracle
//
// internal/native/arm64 is what `bin/fern -target arm64` uses in production, and
// TestNativeLinkArm64MatchesGccLink gates it against gcc's own assembly of the
// same text. So "self-host agrees with native" transitively means "self-host
// agrees with gcc", without needing a cross-toolchain on this host.
//
// # Why the snippet avoids labels, adrp and literal pools
//
// The two assemblers lay out images differently (different entry stub, different
// rodata handling), so any address-bearing instruction legitimately differs —
// comparing those would produce noise, not findings. The snippet below is
// deliberately position-independent: registers and immediates only. Add a row
// whenever the emitter learns a new instruction form; both assemblers then have
// to agree about it.
//
// Three oracle limitations shape the snippet, all worth knowing before editing
// it: internal/native/arm64 treats any line starting with '.' as a directive (so
// the branch target is a bare Lend:, not a .L local label), has no movn
// mnemonic, and has no stur/ldur FP form. None is a self-host limitation --
// #6064 tracks closing them so the snippet can cover those forms too.
func TestSelfHostArm64AsmEncodingMatchesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	// Every form here is one the arm64 emitter actually produces. The
	// register-offset byte loads/stores in the middle block are #6047's shape:
	// they are how a string-copy loop indexes its source and destination.
	const snippet = `.text
.globl _start
_start:
    ldrb w0, [x0, x1]
    ldrb w5, [x6, x7]
    strb w2, [x3, x4]
    strb w0, [x1, x2]
    ldrb w0, [x0, #1]
    ldrb w9, [x10, #255]
    strb w2, [x0]
    strb w7, [x8, #16]
    ldr x0, [x0, x1]
    ldr x3, [x4, #8]
    str x2, [x0, #16]
    ldur x1, [x0, #-8]
    stur x1, [x0, #-16]
    add x0, x0, x1
    add x0, x0, #16
    sub x2, x3, #1
    mul x1, x2, x3
    cmp x1, #0
    cmp x1, x2
    mov x0, x1
    lsl x0, x1, #3
    lsr x0, x1, #2
    asr x0, x1, #4
    and x0, x1, x2
    orr x0, x1, x2
    eor x0, x1, x2
    sxtw x0, w0
    and x10, x9, #255
    eor x0, x1, #1
    // 32-bit (W) forms. AArch64 encodes operand width in the instruction, and
    // arm64_gas_reg maps w0 and x0 to the same number, so every one of these was
    // assembled as its 64-bit sibling until #6054: str w wrote 8 bytes where 4
    // were meant, and ldr w / cmp w pulled the neighbouring 4 bytes into the
    // value. Note ldr/str differ from the ALU forms in TWO ways: the size field,
    // and an immediate scaled by 4 rather than 8 -- so ldr w3, [x4, #4] is a
    // legal scaled encoding where the 64-bit form needs the unscaled ldur.
    ldr w0, [sp, #8]
    ldr w3, [x4, #4]
    ldr w5, [x6]
    str w0, [x1]
    str w2, [x0, #8]
    str w7, [sp, #12]
    ldur w1, [x0, #-8]
    stur w1, [x0, #-16]
    cmp w1, #0
    cmp w9, #255
    cmp w1, w2
    add w0, w0, #16
    sub w2, w3, #1
    and w0, w1, w2
    mov w0, w1
    mov w2, #1
    mov w5, #4095
    rev16 w3, w4
    lsr w0, w1, #2
    lsl w0, w1, #3
    asr w0, w1, #4
    and w10, w9, #255
    and w9, w9, #1
    and w0, w1, #31
    eor w9, w9, #1
    cbz w0, Lend
    cbnz w1, Lend
    tbnz w2, #31, Lend
    // #6060: four forms that encoded a DIFFERENT instruction than the source
    // says, plus movn, which had no dispatch branch at all and emitted nothing
    // while arm64_gas_known claimed it was handled.
    movz x5, #0x400, lsl #16
    movz x2, #1, lsl #16
    movk x2, #0xffff, lsl #32
    // No explicit movn row: the NATIVE assembler used as this test's oracle does
    // not implement that mnemonic ("unsupported instruction movn"), so a row for
    // it fails at the oracle rather than testing anything. The self-host side
    // does implement it (#6060 -- it used to emit nothing at all), and the
    // mov x0, #-100 line below reaches the same encoder through the path the
    // emitter actually takes.
    mov x0, #-100
    mov w0, #-100
    mov x1, #-1
    str d8, [sp, #-16]!
    ldr d8, [sp], #16
    ldr d0, [x12, #8]
    // No plain-negative FP offset row (str d0, [x12, #-8]): the oracle rejects it
    // outright ("str FP offset must be a non-negative multiple of 8") because
    // internal/native/arm64 has no stur/ldur FP form. The self-host now does
    // (#6060 -- without it the offset wrapped to +8184 and the writeback was
    // dropped), so that is a gap in the oracle, not in the assembler under test.
    add x0, sp, x0
    add x3, sp, x4
    sub x0, sp, x1
    // #6044: the FP conversion/rounding family. fcvt had no encoder at all (605
    // uses in six fixtures), so -target arm64 refused 60 corpus fixtures --
    // most of them, like fizzbuzz and map_keys, with no float in their source,
    // because the runtime helpers carry one. fneg/fabs/fsqrt/frint* had
    // encoders AND a dispatch branch but were missing from arm64_gas_known, so
    // the program loop refused what the assembler could already encode. The
    // fmov S/W pair fell through to the 64-bit fp->gpr arm: the wrong register
    // file at the wrong width, 121 times in one fixture.
    fcvt s0, d0
    fcvt s3, d4
    fcvt d0, s0
    fcvt d5, s6
    fcvtzs x0, d0
    fcvtzs w5, d6
    frinta d0, d0
    frintm d0, d1
    frintp d2, d3
    frintz d4, d5
    fsqrt d0, d1
    fneg d2, d3
    fabs d4, d5
    fmov s0, w0
    fmov w0, s0
    fmov s3, w4
    fmov d0, x0
    fmov x0, d0
    fmov d1, d2
    fadd d0, d1, d2
    fsub d0, d1, d2
    fmul d0, d1, d2
    fdiv d0, d1, d2
    fcmp d1, d0
    scvtf d0, x0
    // #6051: ucvtf had no encoder or dispatch at all, so "u64 as f64" — which
    // needs it, a signed convert reading a value >= 2^63 as negative — was
    // refused outright by -target arm64. The w-source rows pin the sf bit:
    // scvtf was hardcoded to the X form, the same width class as fcvtzs above.
    // fcvtzu — the INVERSE conversion, which "f64 as u32" lowers to — was
    // missing for the same reason and is fixed in the same pass.
    scvtf d0, w0
    ucvtf d0, x0
    ucvtf d1, w2
    fcvtzu x0, d1
    fcvtzu w5, d6
Lend:
    ret
`

	got := assembleSelfHost(t, buildAsmBenchDriver(t, gcc), runner, snippet)

	text, _, err := nativearm64.AssembleProgram(snippet, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("native assembler rejected the snippet (the oracle must accept it): %v", err)
	}
	var want []uint32
	for i := 0; i+4 <= len(text); i += 4 {
		want = append(want, binary.LittleEndian.Uint32(text[i:]))
	}

	lines := snippetInsns(snippet)
	if len(got) != len(want) {
		t.Fatalf("word count differs: self-host %d, native %d (snippet has %d instructions)", len(got), len(want), len(lines))
	}
	for i := range want {
		if got[i] != want[i] {
			src := "?"
			if i < len(lines) {
				src = lines[i]
			}
			t.Errorf("word %d (%s): self-host %08x, native %08x", i, src, got[i], want[i])
		}
	}
}

// buildAsmBenchDriver builds the in-process-assembler harness. buildSelfHostBin
// caches on the sources, so the second and later callers in a package run pay
// nothing.
func buildAsmBenchDriver(t *testing.T, gcc string) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "arm64_asm_bench_run.fern")
	return buildSelfHostBin(t, gcc, dir, "arm64_asm_bench_run.fern", "arm64_asm_bench")
}

// assembleSelfHost feeds GAS text to the driver and returns the assembled
// words. A refused line (p.unknown) is a finding, not a pass: the driver would
// otherwise report a short word list and every comparison against it would
// misalign. Refusals now cover unresolved LABELS and SYMBOLS as well as unknown
// mnemonics — before that, an unfound branch target was patched as though the
// "not placed" sentinel were an offset, which is the whole reason #6045's
// numeric-local-label bug produced runnable binaries instead of an error.
func assembleSelfHost(t *testing.T, bin string, runner []string, snippet string) []uint32 {
	t.Helper()
	args := []string{"-words"}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
	}
	cmd.Stdin = strings.NewReader(snippet)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("self-host assembler driver failed: %v\nstderr: %s", err, errb.String())
	}
	if refused := asmRefusals(out.String()); len(refused) > 0 {
		t.Fatalf("the self-host assembler REFUSED %d line(s) of the snippet: %v", len(refused), refused)
	}
	return parseAsmWords(t, out.String())
}

// asmRefusals extracts the driver's `unknown=` lines — the assembler's refusal
// list, covering unknown mnemonics, unresolved labels and unresolved symbols.
func asmRefusals(out string) []string {
	var refused []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "unknown=") {
			refused = append(refused, strings.TrimPrefix(ln, "unknown="))
		}
	}
	return refused
}

// refusalsFor assembles a snippet the assembler is EXPECTED to reject and
// returns what it refused.
func refusalsFor(t *testing.T, bin string, runner []string, snippet string) []string {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, "-words")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), "-words")...)
	}
	cmd.Stdin = strings.NewReader(snippet)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("self-host assembler driver failed: %v\nstderr: %s", err, errb.String())
	}
	return asmRefusals(out.String())
}

// TestSelfHostArm64AsmUnresolvedBranchRefused pins the behaviour that made the
// three #6045 bugs survivable in the first place: a branch whose target does not
// resolve used to be patched as though the -1 "not placed" sentinel were an
// offset, so the assembler emitted a well-formed binary that branched into the
// ELF header instead of reporting anything. It must refuse.
//
// Both shapes here are ones the emitter cannot produce, which is the point — the
// guard has to hold for input nobody is currently generating, or it is not a
// guard. `1b` with no preceding `1:` is the subtler of the two: resolving it to
// definition 0 would aim the branch at a label defined LATER in the file, a
// silently wrong target rather than an error.
func TestSelfHostArm64AsmUnresolvedBranchRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	t.Run("undefined named label", func(t *testing.T) {
		got := refusalsFor(t, bin, runner, ".text\n_start:\n    b Lnowhere\n    ret\n")
		if len(got) != 1 || got[0] != "label:Lnowhere" {
			t.Errorf("refusals = %v, want exactly [label:Lnowhere]", got)
		}
	})

	t.Run("backward numeric ref with no definition before it", func(t *testing.T) {
		got := refusalsFor(t, bin, runner, ".text\n_start:\n    b 1b\n1:\n    ret\n")
		if len(got) != 1 || got[0] != "label:1#none" {
			t.Errorf("refusals = %v, want exactly [label:1#none] — a `1b` before any `1:` must not silently resolve to the `1:` that follows", got)
		}
	})
}

// TestSelfHostArm64AsmNumericLocalLabels pins GAS numeric local labels — `1:`
// defined repeatedly, with `1f` / `1b` naming the next / previous definition.
//
// The arm64 emitter writes every bounds check that way:
//
//	cmp x1, x2 / b.lo 1f / b __fern_oob_abort / 1: …
//
// and the in-process assembler did not implement them. `1:` became a label
// literally named "1", so the lookup returned the FIRST definition in the
// program for all of them, and `1f` matched nothing at all and came back as the
// -1 "not placed" sentinel, which the fixup pass then patched as if it were an
// offset. Every array index and string slice therefore branched to `-1 - here`,
// a word inside the ELF header, which is zero, which is UDF #0. 129 of 317
// corpus fixtures died on SIGILL before printing a byte (#6045).
//
// The oracle cannot assemble numeric locals (internal/native/arm64 has no
// notion of them — #6075), so this compares two spellings of the SAME control
// flow instead: one using numeric locals, one using ordinary named labels. The
// named version goes through the oracle, which anchors the expected encodings;
// the numeric version must then produce byte-identical words. That tests the
// semantics — which definition each reference selects, in both directions —
// rather than re-asserting the branch encoder the snippet above already covers.
func TestSelfHostArm64AsmNumericLocalLabels(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	// Two definitions of `1` and one of `2`, exercising: a forward reference
	// resolving past an intervening definition-free stretch, a SECOND forward
	// reference that must pick the second `1:` rather than the first, a
	// backward `2b` in a loop, and a backward `1b` that must select the most
	// recent `1:` and not the earliest.
	const numeric = `.text
.globl _start
_start:
    cmp x1, x2
    b.lo 1f
    b Labort
1:
    add x1, x1, #1
    cmp x1, x2
    b.lo 1f
    b Labort
1:
    add x1, x1, #2
2:
    sub x1, x1, #1
    cbnz x1, 2b
    b 1b
Labort:
    ret
`
	const named = `.text
.globl _start
_start:
    cmp x1, x2
    b.lo Lone
    b Labort
Lone:
    add x1, x1, #1
    cmp x1, x2
    b.lo Ltwo
    b Labort
Ltwo:
    add x1, x1, #2
Lloop:
    sub x1, x1, #1
    cbnz x1, Lloop
    b Ltwo
Labort:
    ret
`

	bin := buildAsmBenchDriver(t, gcc)
	gotNumeric := assembleSelfHost(t, bin, runner, numeric)
	gotNamed := assembleSelfHost(t, bin, runner, named)

	// Anchor: the named spelling must match the oracle, so a bug shared by both
	// spellings cannot pass by cancelling out.
	text, _, err := nativearm64.AssembleProgram(named, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("native assembler rejected the named snippet (the oracle must accept it): %v", err)
	}
	var want []uint32
	for i := 0; i+4 <= len(text); i += 4 {
		want = append(want, binary.LittleEndian.Uint32(text[i:]))
	}
	lines := snippetInsns(named)
	if len(gotNamed) != len(want) {
		t.Fatalf("named snippet word count differs: self-host %d, native %d", len(gotNamed), len(want))
	}
	for i := range want {
		if gotNamed[i] != want[i] {
			src := "?"
			if i < len(lines) {
				src = lines[i]
			}
			t.Errorf("named word %d (%s): self-host %08x, native %08x", i, src, gotNamed[i], want[i])
		}
	}

	if len(gotNumeric) != len(gotNamed) {
		t.Fatalf("numeric-local snippet assembled to %d words, the named equivalent to %d", len(gotNumeric), len(gotNamed))
	}
	nlines := snippetInsns(numeric)
	for i := range gotNamed {
		if gotNumeric[i] != gotNamed[i] {
			src := "?"
			if i < len(nlines) {
				src = nlines[i]
			}
			t.Errorf("word %d (%s): numeric-local %08x, named equivalent %08x", i, src, gotNumeric[i], gotNamed[i])
		}
	}
}

// TestSelfHostArm64AsmLiteralPool64Bit pins the literal pool's width. `ldr Xt,
// =N` is how the emitter materialises any constant too wide for a mov-wide
// immediate, and the pool parsed its value with a 32-bit accumulator: `ldr x0,
// =1234567890123` laid down 1912767691. That is why the arm64 leg failed
// i64_max_to_string, to_string_round_trip, divmod_inline and the u64 half of
// int_byte_swap while the u32/i32 checks inside those same fixtures passed —
// the truncation tracked the CONSTANT's width, not the operation's.
//
// No oracle here: the assertion is arithmetic (the pool must contain the
// constant's 64 bits, little-endian), not an encoding choice.
func TestSelfHostArm64AsmLiteralPool64Bit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	const want uint64 = 1234567890123 // 0x0000011F71FB04CB — needs 41 bits
	const snippet = `.text
.globl _start
_start:
    ldr x0, =1234567890123
    ret
`
	got := assembleSelfHost(t, buildAsmBenchDriver(t, gcc), runner, snippet)

	var found bool
	for i := 0; i+1 < len(got); i++ {
		if uint64(got[i])|uint64(got[i+1])<<32 == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the literal pool does not contain %d (%#016x); assembled words: %08x", want, want, got)
	}
}

// parseAsmWords reads the `word <n> <decimal>` lines the driver prints under
// -words.
func parseAsmWords(t *testing.T, out string) []uint32 {
	t.Helper()
	var ws []uint32
	for _, ln := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ln, "word ") {
			continue
		}
		var idx int
		var val int64
		if _, err := fmt.Sscanf(ln, "word %d %d", &idx, &val); err != nil {
			t.Fatalf("unparsable word line %q: %v", ln, err)
		}
		if idx != len(ws) {
			t.Fatalf("word lines out of order: got index %d at position %d", idx, len(ws))
		}
		ws = append(ws, uint32(uint64(val)))
	}
	if len(ws) == 0 {
		t.Fatalf("driver printed no word lines; output was:\n%s", out)
	}
	return ws
}

// snippetInsns lists the snippet's instruction lines (skipping directives and
// labels) so a divergence can name the source line rather than only an index.
func snippetInsns(src string) []string {
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ":") || strings.HasPrefix(s, "//") {
			continue
		}
		out = append(out, s)
	}
	return out
}
