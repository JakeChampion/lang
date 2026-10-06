package sourcelint

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The self-host x86-64 transcendental bundle never writes half a register
// without a reason (#11320). An instruction that writes an xmm register's low
// lane and keeps the rest waits on whatever last wrote that register — in a
// loop, the previous call — and sin, cos, exp and pow ran up to 1.55x slower
// for it. Copies are `movapd`, and a conversion's destination is zeroed
// immediately before it.
//
// No program can observe the difference: the bits are the same either way, so
// every behavioural test, the bit-for-bit self-host-vs-native gate included,
// passes with the old instructions restored. This reads the emitter instead.
// Its kernels are literal strings, so the source text is the emitted text.
//
// Scope: this one bundle. The arm64 and wasm bundles have no partial-register
// writes to check, and native x86-64 is frozen (#11347).
const (
	x86TranscendentalsPath = "../../../compiler/asm_ir.fern"
	x86AssemblerPath       = "../../../compiler/x86_native.fern"
)

// lowLaneWrites are the assembler's scalar SSE instructions that write an xmm
// destination's low lane, keep the rest, and do not otherwise read the
// destination. movsd and movss do so only from a register: a load zeroes the
// rest.
var lowLaneWrites = map[string]bool{
	"movsd": true, "movss": true,
	"cvtsi2sd": true, "cvtsi2sdl": true, "cvtsi2sdq": true,
	"cvtsi2ss": true, "cvtsi2ssl": true, "cvtsi2ssq": true,
	"cvtss2sd": true, "cvtsd2ss": true,
	"sqrtsd": true, "sqrtss": true,
	"roundsd": true, "roundss": true,
}

// otherScalarSSE are the rest of the assembler's mnemonics with ss or sd in
// the name: each reads its destination (arithmetic, min, max, cmpsd), writes
// no xmm register (compares, conversions to an integer register), or writes
// all of it (the packed integer forms).
var otherScalarSSE = map[string]bool{
	"addsd": true, "addss": true, "subsd": true, "subss": true,
	"mulsd": true, "mulss": true, "divsd": true, "divss": true,
	"minsd": true, "minss": true, "maxsd": true, "maxss": true,
	"cmpsd":  true,
	"comisd": true, "comiss": true, "ucomisd": true, "ucomiss": true,
	"cvtsd2si": true, "cvtsd2sil": true, "cvtsd2siq": true,
	"cvtss2si": true, "cvtss2sil": true, "cvtss2siq": true,
	"cvttsd2si": true, "cvttsd2sil": true, "cvttsd2siq": true,
	"cvttss2si": true, "cvttss2sil": true, "cvttss2siq": true,
	"packssdw": true, "packsswb": true, "pmaxsd": true, "pminsd": true,
}

var (
	fernFuncHeader = regexp.MustCompile(`(?m)^(?:pub )?function ([a-z0-9_]+)\(`)
	fernString     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	xmmReg         = regexp.MustCompile(`^%xmm[0-9]+$`)
	assemblerArm   = regexp.MustCompile(`(?m)^\s+"([a-z0-9]+)" =>`)
	// The code between two literals when it splices one identifier in.
	interpolation = regexp.MustCompile(`^\+[A-Za-z_][A-Za-z0-9_]*\+$`)
)

// unknown stands for text the emitter computes: an operand interpolated from
// a parameter, or, on a line of its own, whatever a call between two literals
// emits.
const unknown = "\x00"

// transcendentalBundle returns the bodies of emit_rt_float_transcendentals and
// of every fc_* helper its kernels are built from, comments dropped.
func transcendentalBundle(src string) []string {
	var bodies []string
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
		var b strings.Builder
		for _, line := range strings.SplitAfter(src[loc[0]:end], "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				b.WriteString(line)
			}
		}
		bodies = append(bodies, b.String())
	}
	return bodies
}

// emittedLines is the assembly a function body writes, one instruction,
// label or directive per entry. Literals joined by `+` or by consecutive
// `s = s.write(` statements run together; any other code between two
// literals is unknown text.
func emittedLines(body string) []string {
	var b strings.Builder
	prev := -1
	for _, m := range fernString.FindAllStringSubmatchIndex(body, -1) {
		if prev >= 0 {
			gap := strings.Join(strings.Fields(body[prev:m[0]]), "")
			switch {
			case gap == "+" || gap == ");s=s.write(":
			case interpolation.MatchString(gap):
				b.WriteString(unknown)
			default:
				b.WriteString("\n" + unknown + "\n")
			}
		}
		lit := body[m[2]:m[3]]
		lit = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(lit)
		b.WriteString(lit)
		prev = m[1]
	}
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// instruction splits an AT&T line into its mnemonic and operands.
func instruction(line string) (string, []string) {
	mnem, rest, _ := strings.Cut(line, " ")
	var ops []string
	depth, start := 0, 0
	for i, c := range rest {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				ops = append(ops, strings.TrimSpace(rest[start:i]))
				start = i + 1
			}
		}
	}
	if s := strings.TrimSpace(rest[start:]); s != "" {
		ops = append(ops, s)
	}
	return mnem, ops
}

// zeroes reports whether line is a zeroing idiom for reg.
func zeroes(line, reg string) bool {
	mnem, ops := instruction(line)
	return (mnem == "xorps" || mnem == "xorpd" || mnem == "pxor") &&
		len(ops) == 2 && ops[0] == reg && ops[1] == reg
}

// lowLaneViolations checks every low-lane write in lines. A write is safe when
// it loads from memory (movsd, movss), when its destination is also a source,
// or when the line before it zeroes the destination. checked counts the writes
// found safe, so a caller can tell an empty scan from a clean one.
func lowLaneViolations(lines []string) (checked int, bad []string) {
	for i, line := range lines {
		mnem, ops := instruction(line)
		if !lowLaneWrites[mnem] || len(ops) == 0 {
			continue // movsd with no operands is the string move
		}
		dst := ops[len(ops)-1]
		switch {
		case (mnem == "movsd" || mnem == "movss") && strings.Contains(ops[0], "("):
		case strings.Contains(dst, "("): // a store
		case !xmmReg.MatchString(dst):
			bad = append(bad, strings.ReplaceAll(line, unknown, "…")+" (its destination is computed, so the scan cannot check it)")
			continue
		case contains(ops[:len(ops)-1], dst):
		case i > 0 && zeroes(lines[i-1], dst):
		default:
			bad = append(bad, line)
			continue
		}
		checked++
	}
	return checked, bad
}

func TestSelfHostX86TranscendentalsWriteWholeRegisters(t *testing.T) {
	src, err := os.ReadFile(x86TranscendentalsPath)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, body := range transcendentalBundle(string(src)) {
		lines = append(lines, emittedLines(body)...)
	}
	if !contains(lines, "__fern_log_f64:") || !contains(lines, "__fern_rem_pio2_large:") {
		t.Fatalf("%s: the transcendental bundle was not found where this test looks for it", x86TranscendentalsPath)
	}
	checked, bad := lowLaneViolations(lines)
	if checked < 5 {
		t.Fatalf("found %d low-lane writes in the bundle; the extraction has gone stale", checked)
	}
	for _, w := range bad {
		t.Errorf("%s: the transcendental bundle emits %q, which writes only the low lane of a register it has not cleared; copy with movapd, and zero a conversion's destination with xorps first", x86TranscendentalsPath, w)
	}
}

// The scan judges an instruction by its operands, not its spelling.
func TestLowLaneViolationsReadsOperandForms(t *testing.T) {
	for _, c := range []struct {
		src string
		bad int
	}{
		{`s = s.write("    movsd %xmm1, %xmm2\n");`, 1},
		{`s = s.write("    movss %xmm1, %xmm2\n");`, 1},
		{`s = s.write("    cvtsi2sd %rax, %xmm3\n");`, 1},
		{`s = s.write("    cvtsi2sdq %rax, %xmm3\n");`, 1},
		{`s = s.write("    sqrtsd %xmm1, %xmm0\n    cvtss2sd %xmm1, %xmm2\n");`, 2},
		{`s = s.write("    xorps %xmm3, %xmm3\n.L1:\n    cvtsi2sd %rax, %xmm3\n");`, 1},
		{`s = s.write("    xorps %xmm3, %xmm3\n" + fc_ld("%xmm0", ".Lc") + "    cvtsi2sd %rax, %xmm3\n");`, 1},
		{`return "    cvtsi2sd %rax, " + reg + "\n";`, 1},

		{`s = s.write("    movapd %xmm1, %xmm2\n    movq %xmm1, %xmm2\n");`, 0},
		{`s = s.write("    movsd 8(%r8), %xmm5\n    movsd %xmm5, -8(%rsp)\n");`, 0},
		{`return "    movsd " + lbl + "(%rip), " + reg + "\n";`, 0},
		{`s = s.write("    xorps %xmm3, %xmm3\n    cvtsi2sd %rax, %xmm3\n");`, 0},
		{`s = s.write("    xorpd %xmm3, %xmm3\n        cvtsi2sdq %rax, %xmm3\n");`, 0},
		{`s = s.write("    pxor %xmm3, %xmm3\n");` + "\n  " + `s = s.write("    cvtsi2sd %rax, %xmm3\n");`, 0},
		{`s = s.write("    roundsd $0, %xmm1, %xmm1\n    sqrtsd %xmm0, %xmm0\n");`, 0},
	} {
		if _, bad := lowLaneViolations(emittedLines(c.src)); len(bad) != c.bad {
			t.Errorf("lowLaneViolations(%s) = %q, want %d", c.src, bad, c.bad)
		}
	}
}

// Every scalar SSE mnemonic the self-host assembler accepts is classified, so
// a new one cannot slip past the scan unread.
func TestLowLaneWriteTableCoversTheAssembler(t *testing.T) {
	src, err := os.ReadFile(x86AssemblerPath)
	if err != nil {
		t.Fatal(err)
	}
	accepted := map[string]bool{}
	for _, m := range assemblerArm.FindAllStringSubmatch(string(src), -1) {
		if strings.Contains(m[1], "ss") || strings.Contains(m[1], "sd") {
			accepted[m[1]] = true
		}
	}
	for mnem := range accepted {
		if !lowLaneWrites[mnem] && !otherScalarSSE[mnem] {
			t.Errorf("%s accepts %s, which is in neither lowLaneWrites nor otherScalarSSE; classify it", x86AssemblerPath, mnem)
		}
	}
	for _, table := range []map[string]bool{lowLaneWrites, otherScalarSSE} {
		for mnem := range table {
			if !accepted[mnem] {
				t.Errorf("%s no longer accepts %s; drop it from the table", x86AssemblerPath, mnem)
			}
		}
	}
}
