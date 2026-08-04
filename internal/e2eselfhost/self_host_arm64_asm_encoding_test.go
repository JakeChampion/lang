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
    fcvtzu x0, d0
    fcvtzu w5, d6
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
Lend:
    ret
`

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "arm64_asm_bench_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "arm64_asm_bench_run.fern", "arm64_asm_bench")

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
	// A refused line (p.unknown) means the assembler declined a form the
	// emitter can produce. That is a finding, not a pass: the driver would
	// otherwise report a short word list and the comparison below would
	// misalign.
	if strings.Contains(out.String(), "unknown=") {
		var refused []string
		for _, ln := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(ln, "unknown=") {
				refused = append(refused, strings.TrimPrefix(ln, "unknown="))
			}
		}
		t.Fatalf("the self-host assembler REFUSED %d line(s) of the snippet: %v", len(refused), refused)
	}
	got := parseAsmWords(t, out.String())

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
