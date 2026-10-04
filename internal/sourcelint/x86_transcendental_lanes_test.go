package sourcelint

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The self-host x86-64 transcendental bundle never writes half a register
// (#11320). `movsd` between registers and `cvtsi2sd` write the low lane and
// keep the rest, so each waits on whatever last wrote that register — in a
// loop, the previous call — and sin, cos, exp and pow ran up to 1.55x slower
// for it. Copies are `movapd`, and every conversion's destination is zeroed
// with `xorps` immediately before it.
//
// No program can observe the difference: the bits are the same either way, so
// every behavioural test, the bit-for-bit self-host-vs-native gate included,
// passes with the old instructions restored. This reads the emitter instead.
// Its kernels are literal strings, so the source text is the emitted text.
const x86TranscendentalsPath = "../../examples/self_host/asm_ir.fern"

var (
	fernFuncHeader = regexp.MustCompile(`(?m)^(?:pub )?function ([a-z0-9_]+)\(`)
	// A movsd's operands, up to the end of the instruction.
	movsdOperands = regexp.MustCompile(`movsd ([^\\\n]*)\\n`)
	cvtsi2sdDest  = regexp.MustCompile(`cvtsi2sd %r[a-z0-9]+, (%xmm[0-9]+)`)
)

// transcendentalBundle returns the code of emit_rt_float_transcendentals and
// of every fc_* helper its kernels are built from, comments dropped.
func transcendentalBundle(src string) string {
	var b strings.Builder
	locs := fernFuncHeader.FindAllStringSubmatchIndex(src, -1)
	for i, loc := range locs {
		name := src[loc[2]:loc[3]]
		if name != "emit_rt_float_transcendentals" && !strings.HasPrefix(name, "fc_") {
			continue
		}
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		for _, line := range strings.SplitAfter(src[loc[0]:end], "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				b.WriteString(line)
			}
		}
	}
	return b.String()
}

// halfRegisterWrites lists every instruction in src that writes only the low
// lane of a register it has not cleared.
func halfRegisterWrites(src string) []string {
	var bad []string
	for _, m := range movsdOperands.FindAllStringSubmatch(src, -1) {
		// A memory operand is a load (which zeroes the rest) or a store.
		if !strings.Contains(m[1], "(") {
			bad = append(bad, "movsd "+m[1])
		}
	}
	for _, loc := range cvtsi2sdDest.FindAllStringSubmatchIndex(src, -1) {
		reg := src[loc[2]:loc[3]]
		if !strings.HasSuffix(src[:loc[0]], "xorps "+reg+", "+reg+`\n    `) {
			bad = append(bad, src[loc[0]:loc[1]]+" without an xorps of "+reg+" before it")
		}
	}
	return bad
}

func TestSelfHostX86TranscendentalsWriteWholeRegisters(t *testing.T) {
	src, err := os.ReadFile(x86TranscendentalsPath)
	if err != nil {
		t.Fatal(err)
	}
	bundle := transcendentalBundle(string(src))
	if !strings.Contains(bundle, "__fern_log_f64:") || !strings.Contains(bundle, "__fern_rem_pio2_large:") {
		t.Fatalf("%s: the transcendental bundle was not found where this test looks for it", x86TranscendentalsPath)
	}
	// Non-vacuous: the bundle has conversions to check, and movsd loads the
	// scan must recognise as such.
	if n := len(cvtsi2sdDest.FindAllString(bundle, -1)); n < 5 {
		t.Fatalf("found %d cvtsi2sd in the bundle; the extraction has gone stale", n)
	}
	for _, w := range halfRegisterWrites(bundle) {
		t.Errorf("%s: the transcendental bundle emits %s; copy with movapd, and zero a conversion's destination with xorps first", x86TranscendentalsPath, w)
	}
}

// The scan must still see the instructions it exists to keep out.
func TestHalfRegisterWritesSeesTheOldForms(t *testing.T) {
	old := `s = s.write("    movsd %xmm1, %xmm2\n    cvtsi2sd %rax, %xmm3\n");`
	if got := halfRegisterWrites(old); len(got) != 2 {
		t.Fatalf("halfRegisterWrites(%q) = %q, want the copy and the conversion", old, got)
	}
	fixed := `s = s.write("    movapd %xmm1, %xmm2\n    movsd 8(%r8), %xmm5\n    xorps %xmm3, %xmm3\n    cvtsi2sd %rax, %xmm3\n");`
	if got := halfRegisterWrites(fixed); len(got) != 0 {
		t.Fatalf("halfRegisterWrites(%q) = %q, want nothing", fixed, got)
	}
}
