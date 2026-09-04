package fdlibm

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The self-host compiler emits the same transcendental helpers the native
// backends do, but it cannot import Go, so its three emitters carry their own
// literals for this table. That is the one place the numbers can still drift,
// and drift here is expensive and quiet: #6313 exists because three copies
// still carried the old math after two prior PRs fixed the others, and nothing
// compared a self-host transcendental RESULT against native's — every backend
// was checked against the interpreter with a tolerance, and an
// oracle-with-tolerance suite cannot gate a property tighter than its oracle.
//
// These tests read the Fern sources as data and compare them against Coeffs
// bit for bit, so a coefficient edited on one side alone fails a fast Go test.
// Like internal/platforms' self-host parity tests they deliberately build
// nothing: the price has to stay low enough to run on every change here.
//
// Comparison is on bits, not on the decimal spelling, because the emitters do
// not agree on how to write a double — the asm pair carry fdlibm's own
// spelling (`1.531383769920937332e-01`) and the wasm one normalises
// (`0.1531383769920937332`).

const (
	selfHostAsmX86   = "../../../examples/self_host/asm_ir.fern"
	selfHostAsmArm64 = "../../../examples/self_host/asm_arm64_ir.fern"
	selfHostWasm     = "../../../examples/self_host/wasm_ir.fern"
)

func readSelfHost(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func parseDouble(t *testing.T, path, name, text string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("%s: %s is not a double: %q", path, name, text)
	}
	return v
}

var asmCoeffRe = regexp.MustCompile(`\.Lfc_([A-Za-z0-9_]+): \.double ([^\\"\n]+)`)

// TestSelfHostAsmCoeffsMatch pins both self-host assembly emitters to the
// table entry for entry, in order — they emit a contiguous .rodata block whose
// labels the kernels then reference by name.
func TestSelfHostAsmCoeffsMatch(t *testing.T) {
	for _, path := range []string{selfHostAsmX86, selfHostAsmArm64} {
		t.Run(path, func(t *testing.T) {
			ms := asmCoeffRe.FindAllStringSubmatch(readSelfHost(t, path), -1)
			if len(ms) == 0 {
				t.Fatalf("no .Lfc_* .double lines found in %s — the extraction pattern has gone stale, which would make this test vacuous", path)
			}
			if len(ms) != len(Coeffs) {
				t.Errorf("%s emits %d coefficients, the table has %d", path, len(ms), len(Coeffs))
			}
			for i, m := range ms {
				if i >= len(Coeffs) {
					t.Errorf("%s emits %s, which the table does not have", path, m[1])
					continue
				}
				want := Coeffs[i]
				if m[1] != want.Name {
					t.Errorf("%s coefficient %d is .Lfc_%s, the table's is %s — the order is part of the contract, arm64 addresses the table by offset", path, i, m[1], want.Name)
					continue
				}
				got := parseDouble(t, path, m[1], strings.TrimSpace(m[2]))
				if math.Float64bits(got) != math.Float64bits(want.Val) {
					t.Errorf("%s .Lfc_%s = %v (%q), the table's is %v (%q)", path, m[1], got, m[2], want.Val, want.Text)
				}
			}
		})
	}
}

var quadRe = regexp.MustCompile(`\.quad (0x[0-9a-fA-F]+)`)

// TestSelfHostAsmTwoOverPiMatches pins the Payne-Hanek 2/pi limbs, which each
// asm emitter writes out as literal .quad lines after .Lfc_2opi_bits.
func TestSelfHostAsmTwoOverPiMatches(t *testing.T) {
	for _, path := range []string{selfHostAsmX86, selfHostAsmArm64} {
		t.Run(path, func(t *testing.T) {
			src := readSelfHost(t, path)
			i := strings.Index(src, ".Lfc_2opi_bits:")
			if i < 0 {
				t.Fatalf("no .Lfc_2opi_bits label in %s — the extraction pattern has gone stale", path)
			}
			ms := quadRe.FindAllStringSubmatch(src[i:], -1)
			if len(ms) < len(TwoOverPiBits) {
				t.Fatalf("%s emits %d limbs after .Lfc_2opi_bits, the table has %d", path, len(ms), len(TwoOverPiBits))
			}
			for j, want := range TwoOverPiBits {
				got, err := strconv.ParseUint(strings.TrimPrefix(ms[j][1], "0x"), 16, 64)
				if err != nil {
					t.Fatalf("%s limb %d: %v", path, j, err)
				}
				if got != want {
					t.Errorf("%s 2/pi limb %d = 0x%016x, the table's is 0x%016x", path, j, got, want)
				}
			}
		})
	}
}

// wasmTranscendentals bounds the region of wasm_ir.fern to read: the WAT
// emitters for the five helpers plus the two the trig reduction shares.
const (
	wasmRegionStart = "pub function exp_func()"
	wasmRegionEnd   = "pub function random_bytes_ir_func()"
)

func wasmTranscendentalRegion(t *testing.T) string {
	t.Helper()
	src := readSelfHost(t, selfHostWasm)
	i, j := strings.Index(src, wasmRegionStart), strings.Index(src, wasmRegionEnd)
	if i < 0 || j <= i {
		t.Fatalf("cannot bound the transcendental emitters in %s between %q and %q — the markers have gone stale", selfHostWasm, wasmRegionStart, wasmRegionEnd)
	}
	return src[i:j]
}

var wasmConstRe = regexp.MustCompile(`f64\.const (-?[0-9][^)\s]*)`)

// wasmStructural are the values the WAT emitters spell inline that are not
// fdlibm coefficients — exact small doubles used for a sign flip or an early
// return, where a transcription error is not possible in the way it is for a
// 20-digit coefficient.
var wasmStructural = map[float64]bool{0: true, -1: true}

// TestSelfHostWasmCoeffsAreTableEntries pins wasm_ir.fern's inline literals.
// It cannot check order or naming the way the asm emitters allow — the WAT is
// emitted as text with the constants inline, so there are no labels to match —
// but it does catch the failure that matters: a coefficient whose digits have
// drifted from the table's is a member of no entry.
func TestSelfHostWasmCoeffsAreTableEntries(t *testing.T) {
	byBits := map[uint64]string{}
	for _, c := range Coeffs {
		byBits[math.Float64bits(c.Val)] = c.Name
	}
	ms := wasmConstRe.FindAllStringSubmatch(wasmTranscendentalRegion(t), -1)
	if len(ms) < len(Coeffs) {
		t.Fatalf("found %d f64.const literals in %s's transcendental emitters, fewer than the table's %d entries — the extraction pattern has gone stale, which would make this test vacuous", len(ms), selfHostWasm, len(Coeffs))
	}
	seen := map[string]bool{}
	for _, m := range ms {
		v := parseDouble(t, selfHostWasm, "f64.const", m[1])
		if name, ok := byBits[math.Float64bits(v)]; ok {
			seen[name] = true
			continue
		}
		if wasmStructural[v] {
			continue
		}
		t.Errorf("%s emits f64.const %s, which is no entry of the table — a coefficient edited here alone is exactly the drift this gate exists for", selfHostWasm, m[1])
	}
	for _, c := range Coeffs {
		if !seen[c.Name] {
			t.Errorf("%s never emits %s (%s); either the kernel stopped needing it — drop it from the table and from the other four emitters — or the literal has drifted", selfHostWasm, c.Name, c.Text)
		}
	}
}

var wasmLimbRe = regexp.MustCompile(`"(-?[0-9]+)"`)

// TestSelfHostWasmTwoOverPiMatches pins the 2/pi limbs trig_limbs_func carries.
// They are written as SIGNED decimal i64 there, so a limb above 2^63 reads
// negative — 21 hand-transcribed 19-digit numbers is a real place to slip.
func TestSelfHostWasmTwoOverPiMatches(t *testing.T) {
	src := readSelfHost(t, selfHostWasm)
	i := strings.Index(src, "pub function trig_limbs_func()")
	if i < 0 {
		t.Fatalf("no trig_limbs_func in %s — the extraction pattern has gone stale", selfHostWasm)
	}
	end := strings.Index(src[i:], "];")
	if end < 0 {
		t.Fatalf("cannot find the end of trig_limbs_func's limb array in %s", selfHostWasm)
	}
	ms := wasmLimbRe.FindAllStringSubmatch(src[i:i+end], -1)
	if len(ms) != len(TwoOverPiBits) {
		t.Fatalf("trig_limbs_func carries %d limbs, the table has %d", len(ms), len(TwoOverPiBits))
	}
	for j, want := range TwoOverPiBits {
		got, err := strconv.ParseInt(ms[j][1], 10, 64)
		if err != nil {
			t.Fatalf("%s limb %d: %v", selfHostWasm, j, err)
		}
		if uint64(got) != want {
			t.Errorf("%s 2/pi limb %d = %d (0x%016x), the table's is 0x%016x", selfHostWasm, j, got, uint64(got), want)
		}
	}
}

// powIntMaxSpellings is the bound as each self-host emitter writes it —
// three assembly/WAT dialects, so the number is the only shared part.
var powIntMaxSpellings = map[string]func(int) string{
	selfHostAsmX86:   func(n int) string { return fmt.Sprintf("cmpq $%d, %%rcx", n) },
	selfHostAsmArm64: func(n int) string { return fmt.Sprintf("cmp x11, #%d", n) },
	selfHostWasm:     func(n int) string { return fmt.Sprintf("(i64.const %d)", n) },
}

// TestSelfHostPowIntMaxMatches pins __fern_pow_f64's repeated-squaring bound
// in the three self-host emitters to PowIntMax.
//
// The bound is not a coefficient, so the tests above cannot see it, and it is
// the one number in this kernel where a stale copy is SILENT: too small only
// sends exactly-representable results down exp(y*log|x|) for tens of ulp, with
// every special case and every small exponent still answering correctly. That
// is what #6405 was — a wrong answer no gate could distinguish from a rounding
// difference, for three weeks.
func TestSelfHostPowIntMaxMatches(t *testing.T) {
	for path, spell := range powIntMaxSpellings {
		src := readSelfHost(t, path)
		if want := spell(PowIntMax); !strings.Contains(src, want) {
			t.Errorf("%s does not emit %q — PowIntMax is %d; either this emitter's bound has drifted or its spelling has, and both are the drift this gate exists for",
				path, want, PowIntMax)
		}
		// A leftover 64 in the same instruction shape is the specific
		// regression: the old bound, still readable as deliberate.
		if stale := spell(64); PowIntMax != 64 && strings.Contains(src, stale) {
			t.Errorf("%s still emits %q, the pre-#6405 bound", path, stale)
		}
	}
}
