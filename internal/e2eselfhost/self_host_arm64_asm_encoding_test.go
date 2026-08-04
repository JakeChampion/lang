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
		if s == "" || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ":") {
			continue
		}
		out = append(out, s)
	}
	return out
}
